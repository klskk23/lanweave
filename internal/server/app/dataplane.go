package app

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"

	"lanweave/internal/server/config"
	"lanweave/internal/server/netfw"
	"lanweave/internal/server/store"
	"lanweave/internal/server/wg"
)

// setupDataPlane brings up the server's WireGuard interface, enables forwarding,
// and rebuilds the nftables isolation table. It returns the wg.Server whose Close
// must be called on shutdown (which releases handles but leaves the interface up).
// Any failure is fatal to startup (the relay must not serve half-configured).
func setupDataPlane(cfg *config.Config, log *slog.Logger) (*wg.Server, error) {
	keyPath := filepath.Join(cfg.Server.DataDir, "wg_private")
	key, generated, err := wg.LoadOrGenerateKey(keyPath)
	if err != nil {
		return nil, fmt.Errorf("server key: %w", err)
	}
	log.Info("server key ready", "generated", generated, "path", keyPath)

	srv, err := wg.EnsureInterface(cfg.WireGuard, key, log)
	if err != nil {
		return nil, fmt.Errorf("wireguard interface: %w", err)
	}

	if err := netfw.EnableIPv4Forward(); err != nil {
		_ = srv.Close()
		return nil, fmt.Errorf("ipv4 forwarding: %w", err)
	}
	log.Info("ipv4 forwarding enabled")

	if err := netfw.NewManager(cfg.NFTables.Table).Rebuild(log); err != nil {
		_ = srv.Close()
		return nil, fmt.Errorf("nftables setup: %w", err)
	}
	return srv, nil
}

// rebuildNodePeers restores every registered node as a WireGuard peer from the
// database, so nodes survive a relay restart (FR-018). The database is the source
// of truth; this replaces the device's peer set with exactly the stored nodes.
func rebuildNodePeers(ctx context.Context, repo *store.NodeRepo, srv *wg.Server, log *slog.Logger) error {
	nodes, err := repo.AllForPeers(ctx)
	if err != nil {
		return err
	}
	peers := make([]wg.PeerConfig, 0, len(nodes))
	for _, n := range nodes {
		peers = append(peers, wg.PeerConfig{PublicKey: n.PubKey, IP: n.IP})
	}
	if err := srv.ReplacePeers(peers); err != nil {
		return err
	}
	log.Info("node peers rebuilt from database", "count", len(peers))
	return nil
}
