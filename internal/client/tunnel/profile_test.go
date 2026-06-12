package tunnel

import (
	"net/netip"
	"strings"
	"testing"

	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"

	"lanweave/internal/client/state"
)

func validRecord(t *testing.T) (state.Record, string) {
	t.Helper()
	priv, err := wgtypes.GeneratePrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	srv, err := wgtypes.GeneratePrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	rec := state.Record{
		ServerURL: "https://vpn.example.com", NodeName: "laptop", IP: "100.127.0.5",
		ServerPublicKey: srv.PublicKey().String(), Endpoint: "vpn.example.com:51820",
		Network: "100.127.0.0/16",
	}
	return rec, priv.String()
}

func TestBuildUAPIConfig(t *testing.T) {
	rec, priv := validRecord(t)
	cfg, err := BuildUAPIConfig(rec, priv)
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	// Keys are hex (64 chars), not base64.
	for _, key := range []string{"private_key=", "public_key="} {
		line := findLine(t, cfg, key)
		val := strings.TrimPrefix(line, key)
		if len(val) != 64 || strings.ContainsAny(val, "+/=") {
			t.Errorf("%s value not 64-char hex: %q", key, val)
		}
	}
	if !strings.Contains(cfg, "endpoint=vpn.example.com:51820\n") {
		t.Error("endpoint missing")
	}
	if !strings.Contains(cfg, "persistent_keepalive_interval=25\n") {
		t.Error("keepalive must be 25")
	}
	// Split tunnel: exactly one allowed_ip, equal to the network, never 0.0.0.0/0.
	if n := strings.Count(cfg, "allowed_ip="); n != 1 {
		t.Errorf("allowed_ip count = %d, want 1", n)
	}
	if !strings.Contains(cfg, "allowed_ip=100.127.0.0/16\n") {
		t.Error("allowed_ip must equal the VPN network")
	}
	if strings.Contains(cfg, "0.0.0.0/0") {
		t.Error("must not full-tunnel (0.0.0.0/0)")
	}
}

func TestBuildUAPIConfigErrors(t *testing.T) {
	base, priv := validRecord(t)
	cases := map[string]func(*state.Record, *string){
		"bad device key":   func(_ *state.Record, p *string) { *p = "not-base64!!" },
		"bad server key":   func(r *state.Record, _ *string) { r.ServerPublicKey = "nope" },
		"missing endpoint": func(r *state.Record, _ *string) { r.Endpoint = "" },
		"bad network":      func(r *state.Record, _ *string) { r.Network = "garbage" },
		"bad ip":           func(r *state.Record, _ *string) { r.IP = "999.999.0.0" },
	}
	for name, mutate := range cases {
		rec, p := base, priv
		mutate(&rec, &p)
		if _, err := BuildUAPIConfig(rec, p); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

func TestServerVPNIP(t *testing.T) {
	ip, ok := serverVPNIP("100.127.0.0/16")
	if !ok || ip.String() != "100.127.0.1" {
		t.Errorf("serverVPNIP = %v ok=%v, want 100.127.0.1", ip, ok)
	}
	if _, ok := serverVPNIP("garbage"); ok {
		t.Error("invalid network should not yield an IP")
	}
}

func findLine(t *testing.T, cfg, prefix string) string {
	t.Helper()
	for line := range strings.SplitSeq(cfg, "\n") {
		if strings.HasPrefix(line, prefix) {
			return line
		}
	}
	t.Fatalf("config missing line %q", prefix)
	return ""
}

func TestBuildPeerUpdate(t *testing.T) {
	pub := "mLuPHGOFgWMjkTRyXSPHTAccVRkAW0KX8K2y8kfKgkM=" // any valid b64 key
	network := netip.MustParsePrefix("100.127.0.0/16")
	extras := []netip.Prefix{netip.MustParsePrefix("100.100.1.0/24"), netip.MustParsePrefix("100.100.2.0/23")}

	got, err := BuildPeerUpdate(pub, network, extras)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(got), "\n")
	if !strings.HasPrefix(lines[0], "public_key=") {
		t.Errorf("line 0 = %q, want public_key", lines[0])
	}
	if lines[1] != "replace_allowed_ips=true" {
		t.Errorf("line 1 = %q, want replace_allowed_ips=true", lines[1])
	}
	want := []string{"allowed_ip=100.127.0.0/16", "allowed_ip=100.100.1.0/24", "allowed_ip=100.100.2.0/23"}
	for i, w := range want {
		if lines[2+i] != w {
			t.Errorf("line %d = %q, want %q", 2+i, lines[2+i], w)
		}
	}
	if strings.Contains(got, "private_key") || strings.Contains(got, "endpoint") {
		t.Error("peer update must not carry private_key/endpoint (in-place update only)")
	}

	if _, err := BuildPeerUpdate("not-a-key", network, nil); err == nil {
		t.Error("invalid server key accepted")
	}
}

func TestRouteDiff(t *testing.T) {
	p := func(s string) netip.Prefix { return netip.MustParsePrefix(s) }
	for _, tc := range []struct {
		name             string
		current, desired []netip.Prefix
		wantAdd, wantDel int
	}{
		{"from empty", nil, []netip.Prefix{p("100.100.1.0/24")}, 1, 0},
		{"to empty", []netip.Prefix{p("100.100.1.0/24")}, nil, 0, 1},
		{"no change", []netip.Prefix{p("100.100.1.0/24")}, []netip.Prefix{p("100.100.1.0/24")}, 0, 0},
		{"swap", []netip.Prefix{p("100.100.1.0/24")}, []netip.Prefix{p("100.100.2.0/24")}, 1, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			add, del := routeDiff(tc.current, tc.desired)
			if len(add) != tc.wantAdd || len(del) != tc.wantDel {
				t.Errorf("diff = +%d/-%d, want +%d/-%d", len(add), len(del), tc.wantAdd, tc.wantDel)
			}
		})
	}
}

func TestCanonicalPrefixes(t *testing.T) {
	p := func(s string) netip.Prefix { return netip.MustParsePrefix(s) }
	got := canonicalPrefixes([]netip.Prefix{p("100.100.2.7/24"), p("100.100.1.0/24"), p("100.100.2.0/24")})
	if len(got) != 2 || got[0] != p("100.100.1.0/24") || got[1] != p("100.100.2.0/24") {
		t.Errorf("canonical = %v, want masked+deduped+sorted", got)
	}
}
