// Package app wires the server together: config, store, migrations, admin
// bootstrap, and the HTTPS listener with graceful shutdown.
package app

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"

	"golang.org/x/time/rate"

	"lanweave/internal/server/api"
	"lanweave/internal/server/auth"
	"lanweave/internal/server/config"
	"lanweave/internal/server/logging"
	"lanweave/internal/server/status"
	"lanweave/internal/server/store"
)

// Options controls a server run.
type Options struct {
	ConfigPath string
	Version    string
	// Ready, if set, is called with the actual bound address once the server is
	// listening. Used by tests to discover an ephemeral port.
	Ready func(addr string)
}

// Run loads configuration and serves until ctx is cancelled, then shuts down
// gracefully. It returns a non-nil error on any fatal startup or serving failure.
func Run(ctx context.Context, opts Options) error {
	cfg, err := config.Load(opts.ConfigPath)
	if err != nil {
		return err
	}
	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("invalid config: %w", err)
	}

	log := logging.Setup(cfg.Log.Level)
	log.Info("starting", "version", opts.Version)
	log.Info("config loaded", "path", opts.ConfigPath, "listen", cfg.Server.Listen)

	st, err := store.Open(cfg.DBPath())
	if err != nil {
		return err
	}
	defer st.Close()

	if err := st.Migrate(log); err != nil {
		return err
	}

	if err := auth.EnsureAdmin(ctx, st.Users(), cfg, log); err != nil {
		return fmt.Errorf("admin bootstrap: %w", err)
	}

	wgServer, nftMgr, err := setupDataPlane(cfg, log)
	if err != nil {
		return err
	}
	defer wgServer.Close() // releases handles; leaves the interface up (FR-016)

	// Rebuild client peers and zone isolation rules from the database so registered
	// nodes and zone memberships survive a restart (the database is the source of
	// truth, FR-018/FR-017).
	if err := rebuildNodePeers(ctx, st.Nodes(), wgServer, log); err != nil {
		return fmt.Errorf("rebuild node peers: %w", err)
	}
	if err := rebuildZoneRules(ctx, st.Zones(), nftMgr, log); err != nil {
		return fmt.Errorf("rebuild zone rules: %w", err)
	}

	// Poll the live device for per-peer handshake times so the API can report node
	// online status. The snapshot is derived, ephemeral state (rebuilt every poll);
	// the goroutine stops when ctx is cancelled at shutdown.
	onlineTracker := status.New(wgServer.Handshakes, status.DefaultInterval, log)
	go onlineTracker.Run(ctx)

	// In plaintext mode (tls = false, for a TLS-terminating reverse proxy) no
	// certificate is loaded or required; tlsConfig stays nil. The safe default
	// is HTTPS, so any config that did not explicitly opt out lands here.
	var tlsConfig *tls.Config
	if cfg.Server.TLSEnabled() {
		cert, err := tls.LoadX509KeyPair(cfg.Server.TLSCert, cfg.Server.TLSKey)
		if err != nil {
			return fmt.Errorf("TLS certificate load failed: %w", err)
		}
		tlsConfig = &tls.Config{
			MinVersion:   tls.VersionTLS12,
			Certificates: []tls.Certificate{cert},
		}
	}

	jwtTTL, err := time.ParseDuration(cfg.Auth.JWTTTL)
	if err != nil {
		return fmt.Errorf("invalid auth.jwt_ttl: %w", err)
	}
	jwtMgr := auth.NewJWTManager(cfg.Auth.JWTSecret.Reveal(), jwtTTL)

	// invite_ttl has no default: empty means never-expire (zero duration). Validate
	// has already rejected a non-empty value that fails to parse or is negative.
	var inviteTTL time.Duration
	if cfg.Auth.InviteTTL != "" {
		inviteTTL, err = time.ParseDuration(cfg.Auth.InviteTTL)
		if err != nil {
			return fmt.Errorf("invalid auth.invite_ttl: %w", err)
		}
	}

	limiter := rate.NewLimiter(rate.Limit(cfg.RateLimit.RPS), cfg.RateLimit.Burst)
	handler := api.NewRouter(api.Options{
		Version:              opts.Version,
		WG:                   wgServer,
		NetFW:                nftMgr,
		WGConfig:             cfg.WireGuard,
		Status:               onlineTracker,
		Limiter:              limiter,
		Logger:               log,
		Store:                st,
		JWT:                  jwtMgr,
		MaxDevicesPerUser:    *cfg.Limits.MaxDevicesPerUser,
		MaxOwnedZonesPerUser: *cfg.Limits.MaxOwnedZonesPerUser,
		InviteTTL:            inviteTTL,
		APIDocs:              cfg.Server.APIDocsEnabled(),
	})

	srv := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
		TLSConfig:         tlsConfig,
	}

	// Bind the listener explicitly so the real address is known before serving
	// (supports ephemeral ports in tests). On bind failure no server runs.
	ln, err := net.Listen("tcp", cfg.Server.Listen)
	if err != nil {
		return fmt.Errorf("listen %s: %w", cfg.Server.Listen, err)
	}
	serveLn := ln
	if cfg.Server.TLSEnabled() {
		serveLn = tls.NewListener(ln, tlsConfig)
		log.Info("https listening", "addr", ln.Addr().String())
	} else {
		if cfg.Server.WarnPlaintextExposure() {
			log.Warn("plaintext HTTP on a non-loopback address; terminate TLS at a reverse proxy and do not expose this listener publicly", "addr", ln.Addr().String())
		}
		log.Info("http listening (plaintext)", "addr", ln.Addr().String())
	}
	if opts.Ready != nil {
		opts.Ready(ln.Addr().String())
	}

	errCh := make(chan error, 1)
	go func() {
		err := srv.Serve(serveLn)
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
			return
		}
		errCh <- nil
	}()

	select {
	case <-ctx.Done():
		log.Info("shutdown initiated")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			log.Error("graceful shutdown failed", "error", err.Error())
			return err
		}
		log.Info("shutdown complete")
		return nil
	case err := <-errCh:
		if err != nil {
			return fmt.Errorf("server error: %w", err)
		}
		return nil
	}
}
