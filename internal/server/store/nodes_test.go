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

	n1, err := st.Nodes().Create(ctx, uid, "laptop", freshPubKey(t), first, last)
	if err != nil {
		t.Fatalf("create 1: %v", err)
	}
	if n1.IP.String() != "100.127.0.2" {
		t.Errorf("first node ip = %s, want 100.127.0.2", n1.IP)
	}
	n2, err := st.Nodes().Create(ctx, uid, "phone", freshPubKey(t), first, last)
	if err != nil {
		t.Fatalf("create 2: %v", err)
	}
	if n2.IP.String() != "100.127.0.3" {
		t.Errorf("second node ip = %s, want 100.127.0.3", n2.IP)
	}

	// Duplicate name (same user) → ErrNodeNameTaken.
	if _, err := st.Nodes().Create(ctx, uid, "laptop", freshPubKey(t), first, last); !errors.Is(err, store.ErrNodeNameTaken) {
		t.Errorf("dup name: got %v, want ErrNodeNameTaken", err)
	}
	// Duplicate pubkey (any user) → ErrPubKeyTaken.
	if _, err := st.Nodes().Create(ctx, uid, "other", n1.PubKey, first, last); !errors.Is(err, store.ErrPubKeyTaken) {
		t.Errorf("dup pubkey: got %v, want ErrPubKeyTaken", err)
	}
	// Same name, different user → allowed.
	bob := seedUser(t, st, "bob")
	if _, err := st.Nodes().Create(ctx, bob, "laptop", freshPubKey(t), first, last); err != nil {
		t.Errorf("same name different user should be allowed: %v", err)
	}
}

func TestNodeListByUser(t *testing.T) {
	st := newStoreT(t)
	ctx := context.Background()
	alice := seedUser(t, st, "alice")
	bob := seedUser(t, st, "bob")
	first, last := poolBounds(t)

	if _, err := st.Nodes().Create(ctx, alice, "a1", freshPubKey(t), first, last); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Nodes().Create(ctx, alice, "a2", freshPubKey(t), first, last); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Nodes().Create(ctx, bob, "b1", freshPubKey(t), first, last); err != nil {
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

	n, _ := st.Nodes().Create(ctx, alice, "laptop", freshPubKey(t), first, last)

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
	n2, _ := st.Nodes().Create(ctx, alice, "tablet", freshPubKey(t), first, last)
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
		n, err := st.Nodes().Create(ctx, uid, fmt.Sprintf("n%d", i), freshPubKey(t), first, last)
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
	n, _ := st.Nodes().Create(ctx, uid, "new", freshPubKey(t), first, last)
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
			node, err := st.Nodes().Create(ctx, uid, fmt.Sprintf("node%d", i), freshPubKey(t), first, last)
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

func TestNodePoolExhaustion(t *testing.T) {
	st := newStoreT(t)
	ctx := context.Background()
	uid := seedUser(t, st, "alice")
	// /30 → exactly one client address (.2).
	first, last, err := ipam.PoolRange("10.0.0.0/30")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.Nodes().Create(ctx, uid, "first", freshPubKey(t), first, last); err != nil {
		t.Fatalf("first create should succeed: %v", err)
	}
	_, err = st.Nodes().Create(ctx, uid, "second", freshPubKey(t), first, last)
	if !errors.Is(err, store.ErrPoolExhausted) {
		t.Fatalf("second create: got %v, want ErrPoolExhausted", err)
	}
}
