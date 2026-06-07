package store_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"

	"lanweave/internal/server/ipam"
	"lanweave/internal/server/store"
)

// poolBounds for 100.127.0.0/16 → first client 100.127.0.2.
func poolBounds(t *testing.T) (uint32, uint32) {
	t.Helper()
	first, last, err := ipam.PoolRange("100.127.0.0/16")
	if err != nil {
		t.Fatal(err)
	}
	return first, last
}

func freshPubKey(t *testing.T) string {
	t.Helper()
	k, err := wgtypes.GeneratePrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	return k.PublicKey().String()
}

func seedUser(t *testing.T, st *store.Store, name string) int64 {
	t.Helper()
	u, err := st.Users().CreateAdmin(context.Background(), name, "hash")
	if err != nil {
		t.Fatalf("seed user: %v", err)
	}
	return u.ID
}

func TestNodeCreateAscendsAndConflicts(t *testing.T) {
	st := newStoreT(t)
	ctx := context.Background()
	uid := seedUser(t, st, "alice")
	first, last := poolBounds(t)

	n1, err := st.Nodes().Create(ctx, uid, "laptop", freshPubKey(t), first, last, 0)
	if err != nil {
		t.Fatalf("create 1: %v", err)
	}
	if n1.IP.String() != "100.127.0.2" {
		t.Errorf("first node ip = %s, want 100.127.0.2", n1.IP)
	}
	n2, err := st.Nodes().Create(ctx, uid, "phone", freshPubKey(t), first, last, 0)
	if err != nil {
		t.Fatalf("create 2: %v", err)
	}
	if n2.IP.String() != "100.127.0.3" {
		t.Errorf("second node ip = %s, want 100.127.0.3", n2.IP)
	}

	// Duplicate name (same user) → ErrNodeNameTaken.
	if _, err := st.Nodes().Create(ctx, uid, "laptop", freshPubKey(t), first, last, 0); !errors.Is(err, store.ErrNodeNameTaken) {
		t.Errorf("dup name: got %v, want ErrNodeNameTaken", err)
	}
	// Duplicate pubkey (any user) → ErrPubKeyTaken.
	if _, err := st.Nodes().Create(ctx, uid, "other", n1.PubKey, first, last, 0); !errors.Is(err, store.ErrPubKeyTaken) {
		t.Errorf("dup pubkey: got %v, want ErrPubKeyTaken", err)
	}
	// Same name, different user → allowed.
	bob := seedUser(t, st, "bob")
	if _, err := st.Nodes().Create(ctx, bob, "laptop", freshPubKey(t), first, last, 0); err != nil {
		t.Errorf("same name different user should be allowed: %v", err)
	}
}

func TestNodeListByUser(t *testing.T) {
	st := newStoreT(t)
	ctx := context.Background()
	alice := seedUser(t, st, "alice")
	bob := seedUser(t, st, "bob")
	first, last := poolBounds(t)

	if _, err := st.Nodes().Create(ctx, alice, "a1", freshPubKey(t), first, last, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Nodes().Create(ctx, alice, "a2", freshPubKey(t), first, last, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Nodes().Create(ctx, bob, "b1", freshPubKey(t), first, last, 0); err != nil {
		t.Fatal(err)
	}

	aliceNodes, err := st.Nodes().ListByUser(ctx, alice)
	if err != nil {
		t.Fatal(err)
	}
	if len(aliceNodes) != 2 {
		t.Fatalf("alice has %d nodes, want 2", len(aliceNodes))
	}
	if aliceNodes[0].Name != "a2" { // newest first
		t.Errorf("newest-first expected a2, got %s", aliceNodes[0].Name)
	}
	for _, n := range aliceNodes {
		if n.UserID != alice {
			t.Errorf("alice's list contains a node owned by %d", n.UserID)
		}
	}
}

func TestNodeDeleteOwned(t *testing.T) {
	st := newStoreT(t)
	ctx := context.Background()
	alice := seedUser(t, st, "alice")
	bob := seedUser(t, st, "bob")
	first, last := poolBounds(t)

	n, _ := st.Nodes().Create(ctx, alice, "laptop", freshPubKey(t), first, last, 0)

	// Bob cannot delete Alice's node.
	if _, err := st.Nodes().DeleteOwned(ctx, bob, n.ID); !errors.Is(err, store.ErrNodeNotFound) {
		t.Errorf("cross-user delete: got %v, want ErrNodeNotFound", err)
	}
	// Nonexistent id.
	if _, err := st.Nodes().DeleteOwned(ctx, alice, 99999); !errors.Is(err, store.ErrNodeNotFound) {
		t.Errorf("nonexistent delete: got %v, want ErrNodeNotFound", err)
	}
	// Owner deletes; pubkey returned; address freed (reused next).
	pub, err := st.Nodes().DeleteOwned(ctx, alice, n.ID)
	if err != nil || pub != n.PubKey {
		t.Fatalf("delete owned: pub=%q err=%v", pub, err)
	}
	n2, _ := st.Nodes().Create(ctx, alice, "tablet", freshPubKey(t), first, last, 0)
	if n2.IP.String() != "100.127.0.2" {
		t.Errorf("freed address not reused: got %s, want 100.127.0.2", n2.IP)
	}
}

func TestNodeRecycleLowest(t *testing.T) {
	st := newStoreT(t)
	ctx := context.Background()
	uid := seedUser(t, st, "alice")
	first, last := poolBounds(t)

	var ids []int64
	for i := range 3 {
		n, err := st.Nodes().Create(ctx, uid, fmt.Sprintf("n%d", i), freshPubKey(t), first, last, 0)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, n.ID) // .2, .3, .4
	}
	// Delete the middle (.3).
	if _, err := st.Nodes().DeleteOwned(ctx, uid, ids[1]); err != nil {
		t.Fatal(err)
	}
	// Next allocation reuses the lowest free (.3).
	n, _ := st.Nodes().Create(ctx, uid, "new", freshPubKey(t), first, last, 0)
	if n.IP.String() != "100.127.0.3" {
		t.Errorf("recycle: got %s, want 100.127.0.3", n.IP)
	}
}

func TestNodeConcurrentDistinctAddresses(t *testing.T) {
	st := newStoreT(t)
	ctx := context.Background()
	uid := seedUser(t, st, "alice")
	first, last := poolBounds(t)

	const n = 50
	var wg sync.WaitGroup
	type res struct {
		ip  string
		err error
	}
	results := make(chan res, n)
	wg.Add(n)
	for i := range n {
		go func(i int) {
			defer wg.Done()
			node, err := st.Nodes().Create(ctx, uid, fmt.Sprintf("node%d", i), freshPubKey(t), first, last, 0)
			if err != nil {
				results <- res{err: err}
				return
			}
			results <- res{ip: node.IP.String()}
		}(i)
	}
	wg.Wait()
	close(results)

	seen := map[string]bool{}
	for r := range results {
		if r.err != nil {
			t.Fatalf("unexpected error: %v", r.err)
		}
		if seen[r.ip] {
			t.Fatalf("duplicate address allocated: %s", r.ip)
		}
		seen[r.ip] = true
	}
	if len(seen) != n {
		t.Fatalf("expected %d distinct addresses, got %d", n, len(seen))
	}
}

func TestNodeGetOwned(t *testing.T) {
	st := newStoreT(t)
	ctx := context.Background()
	alice := seedUser(t, st, "alice")
	bob := seedUser(t, st, "bob")
	first, last := poolBounds(t)
	n, _ := st.Nodes().Create(ctx, alice, "laptop", freshPubKey(t), first, last, 0)

	got, err := st.Nodes().GetOwned(ctx, alice, n.ID)
	if err != nil || got.ID != n.ID || got.IP != n.IP {
		t.Fatalf("GetOwned(owner): %+v %v", got, err)
	}
	if _, err := st.Nodes().GetOwned(ctx, bob, n.ID); !errors.Is(err, store.ErrNodeNotFound) {
		t.Errorf("GetOwned(non-owner): got %v, want ErrNodeNotFound", err)
	}
	if _, err := st.Nodes().GetOwned(ctx, alice, 99999); !errors.Is(err, store.ErrNodeNotFound) {
		t.Errorf("GetOwned(missing): got %v, want ErrNodeNotFound", err)
	}
}

func TestNodeGetByID(t *testing.T) {
	st := newStoreT(t)
	ctx := context.Background()
	alice := seedUser(t, st, "alice")
	first, last := poolBounds(t)
	n, _ := st.Nodes().Create(ctx, alice, "laptop", freshPubKey(t), first, last, 0)

	// GetByID is unscoped (any owner).
	got, err := st.Nodes().GetByID(ctx, n.ID)
	if err != nil || got.ID != n.ID || got.IP != n.IP {
		t.Fatalf("GetByID: %+v %v", got, err)
	}
	if _, err := st.Nodes().GetByID(ctx, 99999); !errors.Is(err, store.ErrNodeNotFound) {
		t.Errorf("GetByID(missing): got %v, want ErrNodeNotFound", err)
	}
}

// TestNodeCreateDeviceLimit covers the device cap: refused at the cap with no row
// inserted, allowed one below, and a delete frees exactly one slot (FR-003/005, SC-003).
func TestNodeCreateDeviceLimit(t *testing.T) {
	st := newStoreT(t)
	ctx := context.Background()
	uid := seedUser(t, st, "alice")
	first, last := poolBounds(t)
	const limit = 2

	// Up to the cap → success.
	for i := range limit {
		if _, err := st.Nodes().Create(ctx, uid, fmt.Sprintf("d%d", i), freshPubKey(t), first, last, limit); err != nil {
			t.Fatalf("create %d (under cap): %v", i, err)
		}
	}
	// At the cap → ErrDeviceLimitReached, nothing inserted.
	if _, err := st.Nodes().Create(ctx, uid, "over", freshPubKey(t), first, last, limit); !errors.Is(err, store.ErrDeviceLimitReached) {
		t.Fatalf("at cap: got %v, want ErrDeviceLimitReached", err)
	}
	if nodes, _ := st.Nodes().ListByUser(ctx, uid); len(nodes) != limit {
		t.Fatalf("after refusal: %d nodes, want %d (nothing inserted)", len(nodes), limit)
	}

	// Delete one → exactly one slot freed: one replacement succeeds, a second is refused.
	nodes, _ := st.Nodes().ListByUser(ctx, uid)
	if _, err := st.Nodes().DeleteOwned(ctx, uid, nodes[0].ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := st.Nodes().Create(ctx, uid, "replacement", freshPubKey(t), first, last, limit); err != nil {
		t.Fatalf("create after delete: %v", err)
	}
	if _, err := st.Nodes().Create(ctx, uid, "again", freshPubKey(t), first, last, limit); !errors.Is(err, store.ErrDeviceLimitReached) {
		t.Fatalf("second extra create: got %v, want ErrDeviceLimitReached", err)
	}
}

// TestNodeCreateUnlimited proves maxDevices <= 0 disables the cap (FR-007).
func TestNodeCreateUnlimited(t *testing.T) {
	st := newStoreT(t)
	ctx := context.Background()
	uid := seedUser(t, st, "alice")
	first, last := poolBounds(t)
	for i := range 12 { // well past the default of 10
		if _, err := st.Nodes().Create(ctx, uid, fmt.Sprintf("d%d", i), freshPubKey(t), first, last, 0); err != nil {
			t.Fatalf("unlimited create %d: %v", i, err)
		}
	}
}

// TestNodeCreateLimitConcurrent is the SC-010 boundary test: with the user one below
// the cap, many concurrent creates yield exactly one success and never exceed the cap.
// The atomic conditional insert under SQLite's writer lock is what makes this hold.
func TestNodeCreateLimitConcurrent(t *testing.T) {
	st := newStoreT(t)
	ctx := context.Background()
	uid := seedUser(t, st, "alice")
	first, last := poolBounds(t)
	const limit = 5
	for i := range limit - 1 {
		if _, err := st.Nodes().Create(ctx, uid, fmt.Sprintf("seed%d", i), freshPubKey(t), first, last, limit); err != nil {
			t.Fatalf("seed %d: %v", i, err)
		}
	}

	const racers = 20
	keys := make([]string, racers)
	for i := range keys {
		keys[i] = freshPubKey(t)
	}
	var wg sync.WaitGroup
	errs := make(chan error, racers)
	wg.Add(racers)
	for i := range racers {
		go func(i int) {
			defer wg.Done()
			_, err := st.Nodes().Create(ctx, uid, fmt.Sprintf("race%d", i), keys[i], first, last, limit)
			errs <- err
		}(i)
	}
	wg.Wait()
	close(errs)

	var ok, limited int
	for err := range errs {
		switch {
		case err == nil:
			ok++
		case errors.Is(err, store.ErrDeviceLimitReached):
			limited++
		default:
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if ok != 1 {
		t.Errorf("concurrent at boundary: %d succeeded, want exactly 1", ok)
	}
	if limited != racers-1 {
		t.Errorf("refused = %d, want %d (every loser sees ErrDeviceLimitReached)", limited, racers-1)
	}
	if nodes, _ := st.Nodes().ListByUser(ctx, uid); len(nodes) != limit {
		t.Errorf("final node count = %d, want %d (never exceeds cap)", len(nodes), limit)
	}
}

func TestNodePoolExhaustion(t *testing.T) {
	st := newStoreT(t)
	ctx := context.Background()
	uid := seedUser(t, st, "alice")
	// /30 → exactly one client address (.2).
	first, last, err := ipam.PoolRange("10.0.0.0/30")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.Nodes().Create(ctx, uid, "first", freshPubKey(t), first, last, 0); err != nil {
		t.Fatalf("first create should succeed: %v", err)
	}
	_, err = st.Nodes().Create(ctx, uid, "second", freshPubKey(t), first, last, 0)
	if !errors.Is(err, store.ErrPoolExhausted) {
		t.Fatalf("second create: got %v, want ErrPoolExhausted", err)
	}
}
