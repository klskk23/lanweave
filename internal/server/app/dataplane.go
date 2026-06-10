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

// setupDataPlane brings up the server's WireGuard interface and enables IP
// forwarding, returning the wg.Server (whose Close must be called on shutdown) and
// the netfw.Manager. The nftables isolation table is built separately from the
// database (rebuildZoneRules) so it can be populated with the current zones.
// Any failure is fatal to startup (the relay must not serve half-configured).
func setupDataPlane(cfg *config.Config, log *slog.Logger) (*wg.Server, *netfw.Manager, error) {
	keyPath := filepath.Join(cfg.Server.DataDir, "wg_private")
	key, generated, err := wg.LoadOrGenerateKey(keyPath)
	if err != nil {
		return nil, nil, fmt.Errorf("server key: %w", err)
	}
	log.Info("server key ready", "generated", generated, "path", keyPath)

	srv, err := wg.EnsureInterface(cfg.WireGuard, key, log)
	if err != nil {
		return nil, nil, fmt.Errorf("wireguard interface: %w", err)
	}

	if err := netfw.EnableIPv4Forward(); err != nil {
		_ = srv.Close()
		return nil, nil, fmt.Errorf("ipv4 forwarding: %w", err)
	}
	log.Info("ipv4 forwarding enabled")

	return srv, netfw.NewManager(cfg.NFTables.Table), nil
}

// rebuildZoneRules rebuilds the nftables isolation table from the database so the
// sets/rules exactly match the recorded zone memberships (FR-017) and announced
// synthetic routes (feature 030, FR-011). With no zones this produces the empty
// default-deny skeleton (feature 003).
func rebuildZoneRules(ctx context.Context, repo *store.ZoneRepo, anns *store.AnnouncementRepo, mgr *netfw.Manager, log *slog.Logger) error {
	states, err := repo.AllForRebuild(ctx)
	if err != nil {
		return err
	}
	routesByZone, err := anns.RoutesByZone(ctx)
	if err != nil {
		return err
	}
	zones := make([]netfw.ZoneState, 0, len(states))
	for _, s := range states {
		zones = append(zones, netfw.ZoneState{ID: s.ID, MemberIPs: s.MemberIPs, RouteCIDRs: routesByZone[s.ID]})
	}
	return mgr.Rebuild(zones, log)
}

// rebuildNodePeers restores every registered node as a WireGuard peer from the
// database, so nodes survive a relay restart (FR-018). The database is the source
// of truth; this replaces the device's peer set with exactly the stored nodes.
func rebuildNodePeers(ctx context.Context, repo *store.NodeRepo, anns *store.AnnouncementRepo, srv *wg.Server, log *slog.Logger) error {
	nodes, err := repo.AllForPeers(ctx)
	if err != nil {
		return err
	}
	routesByNode, err := anns.RoutesByNode(ctx)
	if err != nil {
		return err
	}
	peers := make([]wg.PeerConfig, 0, len(nodes))
	for _, n := range nodes {
		peers = append(peers, wg.PeerConfig{PublicKey: n.PubKey, IP: n.IP, Routes: routesByNode[n.ID]})
	}
	if err := srv.ReplacePeers(peers); err != nil {
		return err
	}
	log.Info("node peers rebuilt from database", "count", len(peers))
	return nil
}
