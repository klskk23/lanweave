package main

import (
	"errors"
	"flag"
	"fmt"
	"net/netip"
	"strings"
	"text/tabwriter"

	"github.com/vishvananda/netlink"

	"lanweave/internal/client/apiclient"
	"lanweave/internal/client/routesync"
	"lanweave/internal/client/state"
	"lanweave/internal/router/engine"
	"lanweave/internal/router/natctl"
	"lanweave/internal/server/ipam"
)

// cmdAnnounce dispatches the announcement family (feature 032).
func cmdAnnounce(e *env, args []string) int {
	if len(args) == 0 {
		return e.fail("usage: announce add|remove|list ...")
	}
	rec, err := state.Load(e.statePath())
	if err != nil {
		return e.fail("not onboarded (%s)", err)
	}
	if rec.NodeID == 0 {
		return e.fail("device id unknown; re-run `register` (state predates feature 031)")
	}
	c, err := e.newClient(rec)
	if err != nil {
		return e.fail("%s", err)
	}
	defer e.persistTokens(c)

	switch args[0] {
	case "add":
		return cmdAnnounceAdd(e, args[1:], rec, c)
	case "remove":
		return cmdAnnounceRemove(e, args[1:], rec, c)
	case "list":
		return cmdAnnounceList(e, args[1:], rec, c)
	default:
		return e.fail("unknown announce subcommand %q", args[0])
	}
}

func announceFlags(name string, args []string, stderr func(string, ...any) int) (subnet string, zone string, ok bool, code int) {
	// stdlib flag stops at the first positional argument, but the contract
	// reads naturally as `announce add <subnet> --zone <name>` — lift the
	// positional out first, then parse the flags.
	var flags []string
	for _, a := range args {
		if !strings.HasPrefix(a, "-") && subnet == "" && (len(flags) == 0 || flags[len(flags)-1] != "--zone") {
			subnet = a
			continue
		}
		flags = append(flags, a)
	}
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	zoneFlag := fs.String("zone", "", "target zone name")
	if err := fs.Parse(flags); err != nil {
		return "", "", false, 1
	}
	if subnet == "" || fs.NArg() != 0 || *zoneFlag == "" {
		return "", "", false, stderr("usage: announce %s <subnet> --zone <name>", name)
	}
	return subnet, *zoneFlag, true, 0
}

func cmdAnnounceAdd(e *env, args []string, rec state.Record, c *apiclient.Client) int {
	subnet, zone, ok, code := announceFlags("add", args, e.fail)
	if !ok {
		return code
	}

	ann, err := c.CreateAnnouncement(zone, rec.NodeID, subnet)
	if err != nil {
		return e.fail("%s", friendly(err))
	}
	synthetic, err := netip.ParsePrefix(ann.Synthetic)
	if err != nil {
		return e.fail("server sent invalid synthetic block %q", ann.Synthetic)
	}

	// FR-008: the synthetic block must not collide with any local network —
	// otherwise the router could not address it. Compensate the remote
	// attachment so no black-hole announcement survives (FR-005).
	if iface, clash := localOverlap(synthetic); clash {
		compensate(e, c, zone, ann.ID)
		return e.fail("synthetic block %s overlaps local network on %s; announcement withdrawn (ask the operator to re-plan announce.pool)", ann.Synthetic, iface)
	}

	// Local translation rebuild from the source of truth (also covers FR-005:
	// failure → compensate the fresh attachment).
	if err := e.rebuildFromServer(rec, c); err != nil {
		compensate(e, c, zone, ann.ID)
		return e.fail("local translation setup failed (%s); announcement withdrawn", err)
	}

	// Advisory notes (never blocking): unattached subnet, daemon not running.
	real, _ := netip.ParsePrefix(ann.Subnet)
	if _, attached := localOverlap(real); !attached {
		fmt.Fprintf(e.stderr, "note: ensure this router can reach %s\n", ann.Subnet)
	}
	if _, err := engine.New(engine.Config{Iface: e.iface, ServerPubKey: rec.ServerPublicKey}).LastHandshake(); err != nil {
		fmt.Fprintln(e.stderr, "note: tunnel is not running; members cannot reach the subnet until the daemon starts")
	}

	fmt.Fprintf(e.stdout, "announced %s -> %s (zone %s, id %d)\n", ann.Subnet, ann.Synthetic, zone, ann.ID)
	return 0
}

func cmdAnnounceRemove(e *env, args []string, rec state.Record, c *apiclient.Client) int {
	subnet, zone, ok, code := announceFlags("remove", args, e.fail)
	if !ok {
		return code
	}
	list, err := c.ListAnnouncements(zone)
	if err != nil {
		return e.fail("%s", friendly(err))
	}
	var id int64
	for _, a := range list.Announcements {
		if a.NodeID == rec.NodeID && a.Subnet == subnet {
			id = a.ID
			break
		}
	}
	if id == 0 {
		return e.fail("%s is not announced to zone %s by this device", subnet, zone)
	}
	if err := c.DeleteAnnouncement(zone, id); err != nil {
		return e.fail("%s", friendly(err))
	}
	if err := e.rebuildFromServer(rec, c); err != nil {
		fmt.Fprintf(e.stderr, "warning: local rule cleanup failed (%s); the reconcile loop will converge\n", err)
	}
	fmt.Fprintf(e.stdout, "withdrawn %s from zone %s\n", subnet, zone)
	return 0
}

func cmdAnnounceList(e *env, args []string, rec state.Record, c *apiclient.Client) int {
	fs := flag.NewFlagSet("list", flag.ContinueOnError)
	fs.SetOutput(e.stderr)
	all := fs.Bool("all", false, "also show zone-mates' announcements this device can reach")
	if err := fs.Parse(args); err != nil {
		return 1
	}

	zones, err := c.ListZones()
	if err != nil {
		return e.fail("%s", friendly(err))
	}
	memberOf := routesync.MemberZones(zones.Zones, c.ZoneMembers, rec.IP)
	entries, err := routesync.Fetch(memberOf, c.ListAnnouncements)
	if err != nil {
		return e.fail("%s", friendly(err))
	}
	mine := routesync.Mine(entries, rec.NodeID)
	if !*all && len(mine) == 0 {
		fmt.Fprintln(e.stdout, "no announcements")
		return 0
	}
	if *all && len(entries) == 0 {
		fmt.Fprintln(e.stdout, "no announcements")
		return 0
	}

	rules := make([]natctl.Rule, 0, len(mine))
	for _, en := range mine {
		rules = append(rules, natctl.Rule{Synthetic: en.Synthetic, Real: en.Subnet})
	}
	current, err := e.natTable().Current()
	if err != nil {
		fmt.Fprintf(e.stderr, "warning: could not read local translation table (%s)\n", err)
	}
	natOK := err == nil && sameRules(current, rules)

	w := tabwriter.NewWriter(e.stdout, 0, 4, 2, ' ', 0)
	if !*all {
		fmt.Fprintln(w, "SUBNET\tSYNTHETIC\tZONES\tRULES")
		for _, en := range mine {
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", en.Subnet, en.Synthetic, strings.Join(en.Zones, ","), okPending(natOK))
		}
		_ = w.Flush()
		return 0
	}

	fmt.Fprintln(w, "MINE\tSUBNET\tSYNTHETIC\tZONES\tANNOUNCER\tSTATUS")
	for _, en := range entries {
		if en.NodeID == rec.NodeID {
			fmt.Fprintf(w, "yes\t%s\t%s\t%s\t(this)\t%s\n", en.Subnet, en.Synthetic, strings.Join(en.Zones, ","), okPending(natOK))
			continue
		}
		status := "pending"
		if consumerRoutePresent(e.iface, en.Synthetic) {
			status = "routed"
		}
		fmt.Fprintf(w, "no\t%s\t%s\t%s\t%s\t%s\n", en.Subnet, en.Synthetic, strings.Join(en.Zones, ","), en.Announcer, status)
	}
	_ = w.Flush()
	return 0
}

func okPending(ok bool) string {
	if ok {
		return "ok"
	}
	return "pending"
}

// consumerRoutePresent reports whether the synthetic block is routed via the
// tunnel interface (the daemon's reconcile applied it).
func consumerRoutePresent(iface string, p netip.Prefix) bool {
	name := iface
	if name == "" {
		name = engine.DefaultIface
	}
	link, err := netlink.LinkByName(name)
	if err != nil {
		return false
	}
	routes, err := netlink.RouteList(link, netlink.FAMILY_V4)
	if err != nil {
		return false
	}
	for _, r := range routes {
		if r.Dst != nil && r.Dst.String() == p.Masked().String() {
			return true
		}
	}
	return false
}

// rebuildFromServer recomputes the desired set and swaps the local table.
func (e *env) rebuildFromServer(rec state.Record, c *apiclient.Client) error {
	rules, _, err := e.desired(c, rec)
	if err != nil {
		return err
	}
	network, err := netip.ParsePrefix(rec.Network)
	if err != nil {
		return fmt.Errorf("state network %q invalid: %w", rec.Network, err)
	}
	return e.natTable().Rebuild(network, rules)
}

// compensate withdraws a freshly created attachment after a local failure
// (FR-005); its own failure is loudly warned — the server-side dataplane and
// the reconcile loop bound the damage.
func compensate(e *env, c *apiclient.Client, zone string, id int64) {
	if err := c.DeleteAnnouncement(zone, id); err != nil &&
		!errors.Is(err, apiclient.ErrZoneNotFound) && !errors.Is(err, apiclient.ErrNotMember) {
		fmt.Fprintf(e.stderr, "warning: compensation withdrawal failed (%s); the server still routes this announcement\n", friendly(err))
	}
}

// localOverlap reports whether the prefix overlaps any local interface network
// and names the first offending interface.
func localOverlap(p netip.Prefix) (string, bool) {
	links, err := netlink.LinkList()
	if err != nil {
		return "", false
	}
	target := ipam.BlockFromPrefix(p)
	for _, link := range links {
		addrs, err := netlink.AddrList(link, netlink.FAMILY_V4)
		if err != nil {
			continue
		}
		for _, a := range addrs {
			ones, _ := a.IPNet.Mask.Size()
			addr, ok := netip.AddrFromSlice(a.IPNet.IP.To4())
			if !ok {
				continue
			}
			local := ipam.BlockFromPrefix(netip.PrefixFrom(addr, ones))
			if ipam.Overlaps(target, local) {
				return link.Attrs().Name, true
			}
		}
	}
	return "", false
}

func sameRules(a, b []natctl.Rule) bool {
	if len(a) != len(b) {
		return false
	}
	set := map[natctl.Rule]bool{}
	for _, r := range a {
		set[r] = true
	}
	for _, r := range b {
		if !set[r] {
			return false
		}
	}
	return true
}
