// Command collector is the OpenITS cabinet edge collector: polls local
// devices via registered vendor adapters and publishes CloudEvents to the
// cabinet-local NATS JetStream.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/Vikasa2M/vikasa-collector/internal/app"
	"github.com/Vikasa2M/vikasa-collector/internal/config"
	"github.com/Vikasa2M/vikasa-collector/internal/vendors/ntcip"
	"github.com/Vikasa2M/vikasa-collector/sdk/adapter"
)

var version = "dev" // set via -ldflags "-X main.version=..."

// RegisterAdapters wires every compiled-in adapter into the registry. This is
// the one place the binary decides which vendors it ships with; contributing an
// adapter means adding a line here plus internal/vendors/<vendor>/<kind>/.
func RegisterAdapters(r *adapter.Registry) {
	ntcip.RegisterTo(r)
}

func main() {
	cfgPath := flag.String("config", "", "path to collector.yaml (required)")
	natsURL := flag.String("nats", "nats://127.0.0.1:4222", "local NATS URL")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Println(version)
		return
	}
	if *cfgPath == "" {
		fmt.Fprintln(os.Stderr, "-config is required")
		os.Exit(2)
	}

	reg := adapter.NewRegistry()
	RegisterAdapters(reg)

	cfg, err := config.Load(*cfgPath, reg)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := app.Run(ctx, cfg, reg, *natsURL, version); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
