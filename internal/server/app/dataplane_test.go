package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"io"
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/vishvananda/netlink"
	"golang.zx2c4.com/wireguard/wgctrl"
	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"

	"lanweave/internal/server/config"
	"lanweave/internal/server/ipam"
	"lanweave/internal/server/store"
	"lanweave/internal/server/wg"
	"lanweave/internal/testutil"
)

// TestRebuildNodePeers verifies FR-018/SC-007: node peers are restored from the
// database (privileged: real WireGuard).
func TestRebuildNodePeers(t *testing.T) {
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
	k1, _ := wgtypes.GeneratePrivateKey()
	k2, _ := wgtypes.GeneratePrivateKey()
	if _, err := st.Nodes().Create(ctx, user.ID, "n1", k1.PublicKey().String(), "unknown", first, last, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Nodes().Create(ctx, user.ID, "n2", k2.PublicKey().String(), "unknown", first, last, 0); err != nil {
		t.Fatal(err)
	}

	// Bring up a fresh interface with NO peers, then rebuild from the DB.
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

	if err := rebuildNodePeers(ctx, st.Nodes(), st.Announcements(), srv, log); err != nil {
		t.Fatalf("rebuild: %v", err)
	}

	wgc, _ := wgctrl.New()
	defer wgc.Close()
	dev, err := wgc.Device(name)
	if err != nil {
		t.Fatalf("device: %v", err)
	}
	if len(dev.Peers) != 2 {
		t.Fatalf("rebuilt peers = %d, want 2", len(dev.Peers))
	}
	got := map[wgtypes.Key]bool{}
	for _, p := range dev.Peers {
		got[p.PublicKey] = true
	}
	if !got[k1.PublicKey()] || !got[k2.PublicKey()] {
		t.Error("rebuilt peer set does not match the stored nodes")
	}
}
