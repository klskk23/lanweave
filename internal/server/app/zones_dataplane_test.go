package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"net/netip"
	"path/filepath"
	"testing"

	"github.com/google/nftables"
	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"

	"lanweave/internal/server/ipam"
	"lanweave/internal/server/netfw"
	"lanweave/internal/server/store"
	"lanweave/internal/testutil"
)

// TestRebuildZoneRules verifies FR-017/FR-018 against the real kernel: zone rules
// are rebuilt from the DB to match memberships, and once a node leaves a zone
// (as a node deletion does) its address is absent from the rebuilt set.
func TestRebuildZoneRules(t *testing.T) {
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

	owner, _ := st.Users().CreateAdmin(ctx, "alice", "hash")
	first, last, _ := ipam.PoolRange("100.127.0.0/16")
	k1, _ := wgtypes.GeneratePrivateKey()
	k2, _ := wgtypes.GeneratePrivateKey()
	n1, _ := st.Nodes().Create(ctx, owner.ID, "n1", k1.PublicKey().String(), first, last)
	n2, _ := st.Nodes().Create(ctx, owner.ID, "n2", k2.PublicKey().String(), first, last)
	z, _ := st.Zones().Create(ctx, owner.ID, "z", "hash")
	_ = st.Zones().Join(ctx, z.ID, n1.ID)
	_ = st.Zones().Join(ctx, z.ID, n2.ID)

	b := make([]byte, 4)
	_, _ = rand.Read(b)
	table := "lwzdp" + hex.EncodeToString(b)
	mgr := netfw.NewManager(table)
	t.Cleanup(func() {
		if conn, e := nftables.New(); e == nil {
			conn.DelTable(&nftables.Table{Family: nftables.TableFamilyINet, Name: table})
			_ = conn.Flush()
		}
	})

	// Rebuild from the DB → the zone set contains both members (FR-017).
	if err := rebuildZoneRules(ctx, st.Zones(), mgr, log); err != nil {
		t.Fatalf("rebuild: %v", err)
	}
	if !setContains(t, table, z.ID, n1.IP) || !setContains(t, table, z.ID, n2.IP) {
		t.Fatal("rebuilt zone set missing members")
	}

	// Remove n1 from the zone (DB), as a node deletion's cascade would, then rebuild:
	// n1's address must be gone, n2's retained (FR-018 — no recycled inheritance).
	if err := st.Zones().Leave(ctx, z.ID, n1.ID); err != nil {
		t.Fatalf("leave: %v", err)
	}
	if err := rebuildZoneRules(ctx, st.Zones(), mgr, log); err != nil {
		t.Fatalf("rebuild after leave: %v", err)
	}
	if setContains(t, table, z.ID, n1.IP) {
		t.Error("removed node still in the rebuilt set (would let a recycled address inherit the zone)")
	}
	if !setContains(t, table, z.ID, n2.IP) {
		t.Error("remaining member lost from the rebuilt set")
	}
}

func setContains(t *testing.T, table string, zoneID int64, ip netip.Addr) bool {
	t.Helper()
	conn, err := nftables.New()
	if err != nil {
		t.Fatalf("conn: %v", err)
	}
	s, err := conn.GetSetByName(&nftables.Table{Family: nftables.TableFamilyINet, Name: table}, fmt.Sprintf("zone_%d", zoneID))
	if err != nil {
		t.Fatalf("get set: %v", err)
	}
	elems, _ := conn.GetSetElements(s)
	want := ip.As4()
	for _, e := range elems {
		if len(e.Key) == 4 && [4]byte(e.Key) == want {
			return true
		}
	}
	return false
}

// TestOwnerOpsRebuild (privileged) verifies FR-013/SC-005: a kicked member and a
// deleted zone are reflected after the startup rebuild (rules match the DB).
func TestOwnerOpsRebuild(t *testing.T) {
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

	owner, _ := st.Users().CreateAdmin(ctx, "alice", "hash")
	first, last, _ := ipam.PoolRange("100.127.0.0/16")
	k1, _ := wgtypes.GeneratePrivateKey()
	k2, _ := wgtypes.GeneratePrivateKey()
	k3, _ := wgtypes.GeneratePrivateKey()
	n1, _ := st.Nodes().Create(ctx, owner.ID, "n1", k1.PublicKey().String(), first, last)
	n2, _ := st.Nodes().Create(ctx, owner.ID, "n2", k2.PublicKey().String(), first, last)
	n3, _ := st.Nodes().Create(ctx, owner.ID, "n3", k3.PublicKey().String(), first, last)
	zKeep, _ := st.Zones().Create(ctx, owner.ID, "keep", "hash")
	zDel, _ := st.Zones().Create(ctx, owner.ID, "del", "hash")
	_ = st.Zones().Join(ctx, zKeep.ID, n1.ID)
	_ = st.Zones().Join(ctx, zKeep.ID, n2.ID)
	_ = st.Zones().Join(ctx, zDel.ID, n3.ID)

	b := make([]byte, 4)
	_, _ = rand.Read(b)
	table := "lwown" + hex.EncodeToString(b)
	mgr := netfw.NewManager(table)
	t.Cleanup(func() {
		if conn, e := nftables.New(); e == nil {
			conn.DelTable(&nftables.Table{Family: nftables.TableFamilyINet, Name: table})
			_ = conn.Flush()
		}
	})

	// Owner operations at the data layer: kick n2 from zKeep, delete zDel.
	_ = st.Zones().Leave(ctx, zKeep.ID, n2.ID)
	_ = st.Zones().Delete(ctx, zDel.ID)

	// Rebuild from the DB → rules must match: zKeep has n1 not n2, zDel is gone.
	if err := rebuildZoneRules(ctx, st.Zones(), mgr, log); err != nil {
		t.Fatalf("rebuild: %v", err)
	}
	if !setContains(t, table, zKeep.ID, n1.IP) {
		t.Error("kept member missing after rebuild")
	}
	if setContains(t, table, zKeep.ID, n2.IP) {
		t.Error("kicked member present after rebuild")
	}
	conn, _ := nftables.New()
	if _, err := conn.GetSetByName(&nftables.Table{Family: nftables.TableFamilyINet, Name: table}, fmt.Sprintf("zone_%d", zDel.ID)); err == nil {
		t.Error("deleted zone's set present after rebuild")
	}
}
