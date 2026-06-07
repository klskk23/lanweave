package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/vishvananda/netlink"
	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"

	"lanweave/internal/server/config"
	"lanweave/internal/server/ipam"
	"lanweave/internal/server/status"
	"lanweave/internal/server/store"
	"lanweave/internal/server/wg"
	"lanweave/internal/testutil"
)

// TestStatusTrackerReadsRealDevice (privileged) wires the tracker to a real
// wg.Server.Handshakes source. It proves the source path works end-to-end (a
// registered node's peer appears with the zero, never-handshaked time and no read
// error — SC-007), and that the tracker's poll loop reports such a node offline
// with no last handshake. A literal online=true is the manual quickstart scenario.
func TestStatusTrackerReadsRealDevice(t *testing.T) {
	testutil.RequireNetAdmin(t)
	ctx := context.Background()
	log := slog.New(slog.NewJSONHandler(io.Discard, nil))

	st, err := store.Open(filepath.Join(t.TempDir(), "db.sqlite"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(nil); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	user, err := st.Users().CreateAdmin(ctx, "alice", "hash")
	if err != nil {
		t.Fatal(err)
	}
	first, last, _ := ipam.PoolRange("100.127.0.0/16")
	nodeKey, _ := wgtypes.GeneratePrivateKey()
	nodePub := nodeKey.PublicKey().String()
	node, err := st.Nodes().Create(ctx, user.ID, "laptop", nodePub, first, last, 0)
	if err != nil {
		t.Fatal(err)
	}

	b := make([]byte, 4)
	_, _ = rand.Read(b)
	name := "wgt" + hex.EncodeToString(b)
	cfg := config.WireGuardConfig{Network: "100.127.0.0/16", ListenPort: 0, Interface: name, MTU: 1420, Endpoint: "x:1"}
	serverKey, _ := wgtypes.GeneratePrivateKey()
	srv, err := wg.EnsureInterface(cfg, serverKey, log)
	if err != nil {
		t.Fatalf("ensure interface: %v", err)
	}
	t.Cleanup(func() {
		_ = srv.Close()
		if l, e := netlink.LinkByName(name); e == nil {
			_ = netlink.LinkDel(l)
		}
	})
	if err := srv.AddPeer(nodePub, node.IP); err != nil {
		t.Fatalf("add peer: %v", err)
	}

	// Source path against the real device: the registered peer appears with the
	// zero (never-handshaked) time and the read does not error.
	hs, err := srv.Handshakes()
	if err != nil {
		t.Fatalf("handshakes: %v", err)
	}
	ts, ok := hs[nodePub]
	if !ok {
		t.Fatal("registered node's peer missing from device handshakes")
	}
	if !ts.IsZero() {
		t.Errorf("never-handshaked node should report zero time, got %v", ts)
	}

	// End-to-end through the tracker's poll loop: after polling, the node is offline.
	tr := status.New(srv.Handshakes, 5*time.Millisecond, log)
	pollCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	go tr.Run(pollCtx)
	time.Sleep(50 * time.Millisecond) // let the immediate first poll complete
	if tr.Online(nodePub) {
		t.Error("never-handshaked node should be offline")
	}
	if _, ok := tr.LastHandshake(nodePub); ok {
		t.Error("never-handshaked node should report no last handshake")
	}
}
