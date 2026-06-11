// Command lanweave-routerd is the headless lanweave client for OpenWrt
// routers: a tunnel daemon plus non-interactive CLI subcommands covering the
// full client lifecycle (onboard, zones, status, logout). Feature 031.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/netip"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"lanweave/internal/client/apiclient"
	"lanweave/internal/client/keyring"
	"lanweave/internal/client/onboard"
	"lanweave/internal/client/state"
	"lanweave/internal/router/daemon"
	"lanweave/internal/router/engine"

	"log/slog"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

const defaultDataDir = "/etc/lanweave"

// env carries everything a subcommand needs; constructed once per invocation
// so tests can drive commands with private dirs and captured streams.
type env struct {
	dataDir  string
	insecure bool
	stdin    io.Reader
	stdout   io.Writer
	stderr   io.Writer
}

func (e *env) statePath() string   { return filepath.Join(e.dataDir, "state.json") }
func (e *env) keysDir() string     { return filepath.Join(e.dataDir, "keys") }
func (e *env) keys() keyring.Store { return keyring.OpenAt(e.keysDir()) }

func (e *env) fail(format string, args ...any) int {
	fmt.Fprintf(e.stderr, "error: "+format+"\n", args...)
	return 1
}

func usage(w io.Writer) {
	fmt.Fprint(w, `usage: lanweave-routerd [--data-dir DIR] [--insecure] <command> [flags]

commands:
  setup            --server URL [--pin SHA256]   configure the server
  login            --username U                  sign in (password on stdin)
  register-account --username U --invite CODE    create account (password on stdin)
  register         --name NODE                   register this device (platform=openwrt)
  trust            FINGERPRINT                   pin the server certificate (TOFU)
  run                                            run the tunnel daemon (foreground)
  down                                           tear the tunnel interface down
  status                                         show daemon/tunnel/ip/handshake/zones
  zone create|join|leave|list|members ...        manage zones (passwords on stdin)
  logout           [--force]                     deregister this device and wipe local state
`)
}

// run is the testable entry point.
func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	e := &env{dataDir: defaultDataDir, stdin: stdin, stdout: stdout, stderr: stderr}

	// Global flags may precede the subcommand.
	for len(args) > 0 && strings.HasPrefix(args[0], "--") {
		switch {
		case args[0] == "--insecure":
			e.insecure = true
			args = args[1:]
		case args[0] == "--data-dir" && len(args) > 1:
			e.dataDir = args[1]
			args = args[2:]
		case strings.HasPrefix(args[0], "--data-dir="):
			e.dataDir = strings.TrimPrefix(args[0], "--data-dir=")
			args = args[1:]
		case args[0] == "--help" || args[0] == "-h":
			usage(stdout)
			return 0
		default:
			return e.fail("unknown global flag %q", args[0])
		}
	}
	if len(args) == 0 {
		usage(stderr)
		return 1
	}

	cmd, rest := args[0], args[1:]
	switch cmd {
	case "setup":
		return cmdSetup(e, rest)
	case "login":
		return cmdLogin(e, rest, false)
	case "register-account":
		return cmdLogin(e, rest, true)
	case "register":
		return cmdRegister(e, rest)
	case "trust":
		return cmdTrust(e, rest)
	case "run":
		return cmdRun(e)
	case "down":
		return cmdDown(e)
	case "status":
		return cmdStatus(e)
	case "zone":
		return cmdZone(e, rest)
	case "logout":
		return cmdLogout(e, rest)
	case "help", "--help", "-h":
		usage(stdout)
		return 0
	default:
		usage(stderr)
		return e.fail("unknown command %q", cmd)
	}
}

// loadLoose reads the state record without the completeness validation —
// pre-onboard steps (setup/login/trust) legitimately work on a partial record.
func loadLoose(path string) (state.Record, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return state.Record{}, nil
	}
	if err != nil {
		return state.Record{}, err
	}
	var r state.Record
	if err := json.Unmarshal(b, &r); err != nil {
		return state.Record{}, fmt.Errorf("parse state: %w", err)
	}
	return r, nil
}

// newClient builds an API client for the recorded server, honoring the TOFU
// pin and the --insecure escape hatch, and loads any cached session tokens.
func (e *env) newClient(rec state.Record) (*apiclient.Client, error) {
	if rec.ServerURL == "" {
		return nil, errors.New("no server configured; run `setup --server URL` first")
	}
	var opts []apiclient.Option
	switch {
	case e.insecure:
		opts = append(opts, apiclient.WithInsecure())
	case rec.PinnedCertSHA256 != "":
		opts = append(opts, apiclient.WithPinnedCert(rec.PinnedCertSHA256))
	}
	c := apiclient.New(rec.ServerURL, opts...)
	keys := e.keys()
	if tok, err := keys.Get(keyring.SessionTokenName); err == nil {
		c.SetToken(string(tok))
	}
	if rt, err := keys.Get(keyring.RefreshTokenName); err == nil {
		c.SetRefreshToken(string(rt))
	}
	return c, nil
}

// persistTokens caches the client's current tokens (they may have been renewed
// by a silent refresh during the command).
func (e *env) persistTokens(c *apiclient.Client) {
	keys := e.keys()
	if c.Token() != "" {
		_ = keys.Set(keyring.SessionTokenName, []byte(c.Token()))
	}
	if c.RefreshToken() != "" {
		_ = keys.Set(keyring.RefreshTokenName, []byte(c.RefreshToken()))
	}
}

// readSecret reads a password/invite from stdin (everything up to EOF, trimmed).
func (e *env) readSecret() (string, error) {
	b, err := io.ReadAll(io.LimitReader(e.stdin, 4096))
	if err != nil {
		return "", fmt.Errorf("read secret from stdin: %w", err)
	}
	s := strings.TrimRight(string(b), "\r\n")
	if s == "" {
		return "", errors.New("empty secret on stdin")
	}
	return s, nil
}

// friendly maps apiclient typed errors (and TOFU cert errors) to actionable
// single-line messages; secrets never appear here.
func friendly(err error) string {
	var ce *apiclient.CertError
	if errors.As(err, &ce) {
		if ce.Changed {
			return fmt.Sprintf("server certificate CHANGED (new fingerprint %s); if this is expected, re-run `trust %s`", ce.Fingerprint, ce.Fingerprint)
		}
		return fmt.Sprintf("server certificate is not trusted (fingerprint %s); verify it and run `trust %s`", ce.Fingerprint, ce.Fingerprint)
	}
	return err.Error()
}

func cmdSetup(e *env, args []string) int {
	fs := flag.NewFlagSet("setup", flag.ContinueOnError)
	fs.SetOutput(e.stderr)
	server := fs.String("server", "", "server URL (https://host[:port])")
	pin := fs.String("pin", "", "pre-trusted certificate SHA-256 fingerprint")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if *server == "" {
		return e.fail("--server is required")
	}
	rec, err := loadLoose(e.statePath())
	if err != nil {
		return e.fail("%s", err)
	}
	rec.ServerURL = strings.TrimRight(*server, "/")
	if *pin != "" {
		rec.PinnedCertSHA256 = strings.ToLower(*pin)
	}
	if err := state.Save(e.statePath(), rec); err != nil {
		return e.fail("%s", err)
	}
	fmt.Fprintf(e.stdout, "server set to %s\n", rec.ServerURL)
	return 0
}

func cmdTrust(e *env, args []string) int {
	if len(args) != 1 || args[0] == "" {
		return e.fail("usage: trust <sha256-fingerprint>")
	}
	rec, err := loadLoose(e.statePath())
	if err != nil {
		return e.fail("%s", err)
	}
	if rec.ServerURL == "" {
		return e.fail("no server configured; run `setup --server URL` first")
	}
	rec.PinnedCertSHA256 = strings.ToLower(args[0])
	if err := state.Save(e.statePath(), rec); err != nil {
		return e.fail("%s", err)
	}
	fmt.Fprintf(e.stdout, "certificate pinned (%s)\n", rec.PinnedCertSHA256)
	return 0
}

// cmdLogin handles both `login` and `register-account` (createAccount=true).
func cmdLogin(e *env, args []string, createAccount bool) int {
	name := "login"
	if createAccount {
		name = "register-account"
	}
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(e.stderr)
	username := fs.String("username", "", "account username")
	invite := fs.String("invite", "", "invite code (register-account only)")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if *username == "" {
		return e.fail("--username is required")
	}
	if createAccount && *invite == "" {
		return e.fail("--invite is required")
	}
	password, err := e.readSecret()
	if err != nil {
		return e.fail("%s", err)
	}
	rec, err := loadLoose(e.statePath())
	if err != nil {
		return e.fail("%s", err)
	}
	c, err := e.newClient(rec)
	if err != nil {
		return e.fail("%s", err)
	}
	if createAccount {
		if err := c.Register(*invite, *username, password); err != nil {
			return e.fail("%s", friendly(err))
		}
	}
	if err := c.Login(*username, password); err != nil {
		return e.fail("%s", friendly(err))
	}
	e.persistTokens(c)
	fmt.Fprintf(e.stdout, "signed in as %s\n", *username)
	return 0
}

func cmdRegister(e *env, args []string) int {
	fs := flag.NewFlagSet("register", flag.ContinueOnError)
	fs.SetOutput(e.stderr)
	nodeName := fs.String("name", "", "this device's node name")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if *nodeName == "" {
		return e.fail("--name is required")
	}
	rec, err := loadLoose(e.statePath())
	if err != nil {
		return e.fail("%s", err)
	}
	if rec.NodeID != 0 || rec.NodeName != "" {
		return e.fail("this device is already registered as %q; run `logout` first", rec.NodeName)
	}
	c, err := e.newClient(rec)
	if err != nil {
		return e.fail("%s", err)
	}
	if c.Token() == "" && c.RefreshToken() == "" {
		return e.fail("not signed in; run `login` first")
	}
	p := &onboard.Provisioner{
		API:              c,
		Keys:             e.keys(),
		StatePath:        e.statePath(),
		ServerURL:        rec.ServerURL,
		PinnedCertSHA256: rec.PinnedCertSHA256,
		Platform:         "openwrt",
	}
	newRec, err := p.ProvisionDevice(*nodeName)
	if err != nil {
		return e.fail("%s", friendly(err))
	}
	e.persistTokens(c)
	fmt.Fprintf(e.stdout, "device registered: %s (node %d, ip %s)\n", newRec.NodeName, newRec.NodeID, newRec.IP)
	return 0
}

// engineFromState builds the tunnel engine from the onboarded record + key.
func (e *env) engineFromState() (*engine.Engine, state.Record, error) {
	rec, err := state.Load(e.statePath())
	if err != nil {
		return nil, state.Record{}, fmt.Errorf("not onboarded (%w); complete setup/login/register first", err)
	}
	priv, err := e.keys().Get(keyring.DeviceKeyName)
	if err != nil {
		return nil, state.Record{}, errors.New("device key missing; re-run `register`")
	}
	addr, err := netip.ParseAddr(rec.IP)
	if err != nil {
		return nil, state.Record{}, fmt.Errorf("state ip %q invalid: %w", rec.IP, err)
	}
	network, err := netip.ParsePrefix(rec.Network)
	if err != nil {
		return nil, state.Record{}, fmt.Errorf("state network %q invalid: %w", rec.Network, err)
	}
	return engine.New(engine.Config{
		PrivateKey:   string(priv),
		Address:      addr,
		Network:      network,
		ServerPubKey: rec.ServerPublicKey,
		Endpoint:     rec.Endpoint,
		Keepalive:    25 * time.Second,
	}), rec, nil
}

func cmdRun(e *env) int {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	return cmdRunCtx(e, ctx)
}

// cmdRunCtx is the daemon body with an injectable context (tests cancel it
// instead of sending signals to the test process).
func cmdRunCtx(e *env, ctx context.Context) int {
	eng, _, err := e.engineFromState()
	if err != nil {
		return e.fail("%s", err)
	}
	log := slog.New(slog.NewTextHandler(e.stderr, nil))
	d := &daemon.Daemon{Engine: eng, Log: log}
	if err := d.Run(ctx); err != nil {
		return e.fail("%s", err)
	}
	return 0
}

func cmdDown(e *env) int {
	if err := engine.New(engine.Config{}).Down(); err != nil {
		return e.fail("%s", err)
	}
	fmt.Fprintln(e.stdout, "tunnel down")
	return 0
}

func cmdStatus(e *env) int {
	rec, err := loadLoose(e.statePath())
	if err != nil {
		return e.fail("%s", err)
	}
	eng := engine.New(engine.Config{ServerPubKey: rec.ServerPublicKey})

	daemonState := "stopped"
	tunnel := "disconnected"
	lastHS := "never"
	if hs, err := eng.LastHandshake(); err == nil {
		daemonState = "running" // interface exists → the daemon owns it
		if !hs.IsZero() {
			lastHS = hs.UTC().Format(time.RFC3339)
			if time.Since(hs) < 3*time.Minute {
				tunnel = "connected"
			}
		}
	}

	zones := "unavailable"
	if c, err := e.newClient(rec); err == nil {
		if list, err := c.ListZones(); err == nil {
			names := make([]string, 0, len(list.Zones))
			for _, z := range list.Zones {
				names = append(names, z.Name)
			}
			sort.Strings(names)
			zones = strings.Join(names, ",")
			if zones == "" {
				zones = "none"
			}
			e.persistTokens(c)
		}
	}

	ip := rec.IP
	if ip == "" {
		ip = "unassigned"
	}
	fmt.Fprintf(e.stdout, "daemon: %s\ntunnel: %s\nip: %s\nlast_handshake: %s\nzones: %s\n",
		daemonState, tunnel, ip, lastHS, zones)
	return 0
}

func cmdZone(e *env, args []string) int {
	if len(args) == 0 {
		return e.fail("usage: zone create|join|leave|list|members ...")
	}
	rec, err := state.Load(e.statePath())
	if err != nil {
		return e.fail("not onboarded (%s)", err)
	}
	c, err := e.newClient(rec)
	if err != nil {
		return e.fail("%s", err)
	}
	defer e.persistTokens(c)

	sub, rest := args[0], args[1:]
	switch sub {
	case "create":
		if len(rest) != 1 {
			return e.fail("usage: zone create <name>  (password on stdin)")
		}
		password, err := e.readSecret()
		if err != nil {
			return e.fail("%s", err)
		}
		z, err := c.CreateZone(rest[0], rec.NodeID, password)
		if err != nil {
			return e.fail("%s", friendly(err))
		}
		fmt.Fprintf(e.stdout, "zone %s created; this device joined\n", z.Name)
		return 0
	case "join":
		if len(rest) != 1 {
			return e.fail("usage: zone join <name>  (password on stdin)")
		}
		password, err := e.readSecret()
		if err != nil {
			return e.fail("%s", err)
		}
		if err := c.JoinZone(rest[0], rec.NodeID, password); err != nil {
			return e.fail("%s", friendly(err))
		}
		fmt.Fprintf(e.stdout, "joined zone %s\n", rest[0])
		return 0
	case "leave":
		if len(rest) != 1 {
			return e.fail("usage: zone leave <name>")
		}
		if err := c.LeaveZone(rest[0], rec.NodeID); err != nil {
			return e.fail("%s", friendly(err))
		}
		fmt.Fprintf(e.stdout, "left zone %s\n", rest[0])
		return 0
	case "list":
		list, err := c.ListZones()
		if err != nil {
			return e.fail("%s", friendly(err))
		}
		if len(list.Zones) == 0 {
			fmt.Fprintln(e.stdout, "no zones")
			return 0
		}
		for _, z := range list.Zones {
			role := "member"
			if z.IsOwner {
				role = "owner"
			}
			fmt.Fprintf(e.stdout, "%s\t%s\n", z.Name, role)
		}
		return 0
	case "members":
		if len(rest) != 1 {
			return e.fail("usage: zone members <name>")
		}
		members, err := c.ZoneMembers(rest[0])
		if err != nil {
			return e.fail("%s", friendly(err))
		}
		for _, m := range members.Members {
			fmt.Fprintf(e.stdout, "%s\t%s\t%s\n", m.NodeName, m.IP, m.Owner)
		}
		return 0
	default:
		return e.fail("unknown zone subcommand %q", sub)
	}
}

func cmdLogout(e *env, args []string) int {
	fs := flag.NewFlagSet("logout", flag.ContinueOnError)
	fs.SetOutput(e.stderr)
	force := fs.Bool("force", false, "skip server-side deregistration (leaves an orphan node)")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	rec, err := loadLoose(e.statePath())
	if err != nil {
		return e.fail("%s", err)
	}

	if !*force {
		c, err := e.newClient(rec)
		if err != nil {
			return e.fail("%s", err)
		}
		if rec.NodeID == 0 {
			return e.fail("no registered device in state; use --force to wipe local data only")
		}
		// 025 semantics: deregister remotely first, with a short retry window;
		// an unreachable server blocks logout so no orphan node is left behind.
		var lastErr error
		for attempt := 0; attempt < 3; attempt++ {
			if attempt > 0 {
				time.Sleep(time.Second)
			}
			lastErr = c.DeleteNode(rec.NodeID)
			if lastErr == nil {
				break
			}
		}
		if lastErr != nil {
			return e.fail("could not deregister this device (%s); fix connectivity or use --force (leaves an orphan node)", friendly(lastErr))
		}
		if err := c.Logout(); err != nil { // revoke the refresh token (idempotent)
			fmt.Fprintf(e.stderr, "warning: refresh token revocation failed: %s\n", friendly(err))
		}
	} else {
		fmt.Fprintln(e.stderr, "warning: --force leaves an orphan node on the server")
	}

	// Local wipe + tunnel teardown (same order regardless of force).
	if err := engine.New(engine.Config{}).Down(); err != nil {
		fmt.Fprintf(e.stderr, "warning: tunnel teardown failed: %s\n", err)
	}
	keys := e.keys()
	wipeErr := errors.Join(
		keys.Delete(keyring.DeviceKeyName),
		keys.Delete(keyring.SessionTokenName),
		keys.Delete(keyring.RefreshTokenName),
		state.Clear(e.statePath()),
	)
	if wipeErr != nil {
		return e.fail("local wipe incomplete: %s", wipeErr)
	}
	fmt.Fprintln(e.stdout, "logged out; local state cleared")
	return 0
}
