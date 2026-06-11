package testutil

import (
	"fmt"
	"runtime"
	"testing"

	"github.com/vishvananda/netlink"
	"github.com/vishvananda/netns"
)

// InNS runs fn on a locked OS thread inside the given network namespace.
// Sockets and netlink handles created inside keep operating on that namespace
// from any goroutine afterwards (they bind their namespace at creation).
func InNS(ns netns.NsHandle, fn func() error) error {
	errCh := make(chan error, 1)
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		orig, err := netns.Get()
		if err != nil {
			errCh <- fmt.Errorf("get current ns: %w", err)
			return
		}
		defer orig.Close()
		if err := netns.Set(ns); err != nil {
			errCh <- fmt.Errorf("enter ns: %w", err)
			return
		}
		defer func() { _ = netns.Set(orig) }()
		errCh <- fn()
	}()
	return <-errCh
}

// NewChildNS creates a new anonymous network namespace (the calling thread's
// namespace is restored) and registers its handle for cleanup.
func NewChildNS(t *testing.T) netns.NsHandle {
	t.Helper()
	var handle netns.NsHandle
	done := make(chan error, 1)
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		orig, err := netns.Get()
		if err != nil {
			done <- err
			return
		}
		defer orig.Close()
		h, err := netns.New() // creates AND enters
		if err != nil {
			done <- err
			return
		}
		handle = h
		done <- netns.Set(orig)
	}()
	if err := <-done; err != nil {
		t.Fatalf("create child netns: %v", err)
	}
	t.Cleanup(func() { _ = handle.Close() })
	return handle
}

// ConnectNS wires two namespaces with a veth pair: hostName/hostCIDR stay in
// hostNS, peerName/peerCIDR move into peerNS; lo is brought up in both.
func ConnectNS(t *testing.T, hostNS, peerNS netns.NsHandle, hostName, hostCIDR, peerName, peerCIDR string) {
	t.Helper()
	if err := InNS(hostNS, func() error {
		if err := ensureLoUp(); err != nil {
			return err
		}
		veth := &netlink.Veth{LinkAttrs: netlink.LinkAttrs{Name: hostName}, PeerName: peerName}
		if err := netlink.LinkAdd(veth); err != nil {
			return fmt.Errorf("veth add: %w", err)
		}
		peerLink, err := netlink.LinkByName(peerName)
		if err != nil {
			return fmt.Errorf("peer link: %w", err)
		}
		if err := netlink.LinkSetNsFd(peerLink, int(peerNS)); err != nil {
			return fmt.Errorf("move peer: %w", err)
		}
		return addrUp(hostName, hostCIDR)
	}); err != nil {
		t.Fatalf("host-side veth: %v", err)
	}
	if err := InNS(peerNS, func() error {
		if err := ensureLoUp(); err != nil {
			return err
		}
		return addrUp(peerName, peerCIDR)
	}); err != nil {
		t.Fatalf("peer-side veth: %v", err)
	}
}

func ensureLoUp() error {
	lo, err := netlink.LinkByName("lo")
	if err != nil {
		return err
	}
	return netlink.LinkSetUp(lo)
}

func addrUp(name, cidr string) error {
	link, err := netlink.LinkByName(name)
	if err != nil {
		return err
	}
	addr, err := netlink.ParseAddr(cidr)
	if err != nil {
		return err
	}
	if err := netlink.AddrAdd(link, addr); err != nil {
		return fmt.Errorf("addr %s: %w", cidr, err)
	}
	return netlink.LinkSetUp(link)
}
