// Command collector is the OpenITS cabinet edge collector: polls local
// devices via registered vendor adapters and publishes CloudEvents to the
// cabinet-local NATS JetStream.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"runtime/debug"
	"syscall"

	"github.com/Vikasa2M/vikasa-collector/internal/app"
	"github.com/Vikasa2M/vikasa-collector/internal/config"
	"github.com/Vikasa2M/vikasa-collector/internal/manage"
	"github.com/Vikasa2M/vikasa-collector/internal/publish"
	"github.com/Vikasa2M/vikasa-collector/sdk/adapter"
)

var version = "dev" // set via -ldflags "-X main.version=..."

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	// The first signal starts a graceful, bounded shutdown. Restoring default
	// handling right then means a second one kills the process outright, for
	// an operator who will not wait out the drain.
	go func() {
		<-ctx.Done()
		stop()
	}()
	err := run(ctx, os.Args[1:], os.Stdout, os.Stderr)
	stop()
	if err != nil && !errors.Is(err, flag.ErrHelp) {
		fmt.Fprintln(os.Stderr, err)
	}
	os.Exit(exitCode(err))
}

// usageError marks a mistake in how the binary was invoked, as opposed to a
// bad config or a runtime failure. It exits 2, so a supervisor can tell "the
// unit file is wrong" from "the cabinet is wrong".
type usageError struct{ err error }

func (e usageError) Error() string { return e.err.Error() }
func (e usageError) Unwrap() error { return e.err }

func exitCode(err error) int {
	switch {
	case err == nil, errors.Is(err, flag.ErrHelp):
		return 0
	case errors.As(err, new(usageError)):
		return 2
	default:
		return 1
	}
}

// run is main without the process: flags come from args, output goes to the
// given writers, and failure is a returned error rather than os.Exit, so the
// whole command line is testable.
func run(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("collector", flag.ContinueOnError)
	fs.SetOutput(stderr)
	cfgPath := fs.String("config", "", "path to collector.yaml (required)")
	natsURL := fs.String("nats", "nats://127.0.0.1:4222", "NATS URL; never put credentials in it (use -nats-creds); non-loopback must be tls://")
	natsCreds := fs.String("nats-creds", "", "NATS .creds file (user JWT + nkey seed); must not be readable by other users")
	natsCA := fs.String("nats-ca", "", "CA certificate to verify a tls:// broker; default is the system roots")
	logLevel := fs.String("log-level", "info", "log level: debug, info, warn, error")
	logFormat := fs.String("log-format", "text", "log format: text, json")
	statusAddr := fs.String("status-addr", "127.0.0.1:9471", "loopback address for /livez, /readyz, /status; empty disables")
	showVersion := fs.Bool("version", false, "print version and exit")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return err
		}
		return usageError{err}
	}

	info, _ := debug.ReadBuildInfo()
	ver := resolveVersion(version, info)
	if *showVersion {
		_, err := fmt.Fprintln(stdout, ver)
		return err
	}
	if *cfgPath == "" {
		return usageError{errors.New("-config is required")}
	}
	log, err := newLogger(stderr, *logLevel, *logFormat)
	if err != nil {
		return usageError{err}
	}
	if err := manage.ValidateAddr(*statusAddr); err != nil {
		return usageError{err}
	}
	natsSec := publish.Security{CredsFile: *natsCreds, CAFile: *natsCA}
	if err := publish.ValidateSecurity(*natsURL, natsSec); err != nil {
		return usageError{err}
	}

	reg := adapter.NewRegistry()
	RegisterAdapters(reg)
	cfg, err := config.Load(*cfgPath, reg)
	if err != nil {
		return err
	}
	return app.Run(ctx, cfg, reg, app.Options{NATSURL: *natsURL, NATS: natsSec, Version: ver, Logger: log, StatusAddr: *statusAddr})
}

// newLogger builds the process logger from the -log-* flags. An unknown
// value is a usage error at boot, not a silent fallback to the default.
func newLogger(w io.Writer, level, format string) (*slog.Logger, error) {
	var lvl slog.Level
	if err := lvl.UnmarshalText([]byte(level)); err != nil {
		return nil, fmt.Errorf("-log-level %q: want debug, info, warn or error", level)
	}
	opts := &slog.HandlerOptions{Level: lvl}
	switch format {
	case "text":
		return slog.New(slog.NewTextHandler(w, opts)), nil
	case "json":
		return slog.New(slog.NewJSONHandler(w, opts)), nil
	default:
		return nil, fmt.Errorf("-log-format %q: want text or json", format)
	}
}

// resolveVersion prefers the ldflags stamp, then the module version a
// `go install pkg@vX.Y.Z` build records, and only then "dev".
func resolveVersion(stamped string, info *debug.BuildInfo) string {
	if stamped != "dev" || info == nil {
		return stamped
	}
	if v := info.Main.Version; v != "" && v != "(devel)" {
		return v
	}
	return stamped
}
