// Command lanweaved is the lanweave relay server daemon.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"lanweave/internal/server/app"
	"lanweave/internal/server/config"
)

// version is set at build time via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	var configPath string
	flag.StringVar(&configPath, "config", "", "path to config.toml")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	err := app.Run(ctx, app.Options{
		ConfigPath: config.Resolve(configPath),
		Version:    version,
	})
	if err != nil {
		// The logger may not be configured if startup failed early, so report
		// the fatal error to stderr as well.
		fmt.Fprintln(os.Stderr, "fatal:", err)
		os.Exit(1)
	}
}
