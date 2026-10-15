package main

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"runtime/debug"
	"strings"
	"testing"
	"time"
)

func TestRunPrintsVersion(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if err := run(t.Context(), []string{"-version"}, &stdout, &stderr); err != nil {
		t.Fatalf("run(-version) = %v", err)
	}
	if strings.TrimSpace(stdout.String()) == "" {
		t.Fatal("run(-version) printed nothing")
	}
}

// Usage mistakes exit 2 and config/runtime failures exit 1, so a supervisor
// can tell "the unit file is wrong" from "the cabinet is wrong".
func TestRunExitCodes(t *testing.T) {
	cfg := filepath.Join("..", "..", "collector.yaml")
	cases := []struct {
		name string
		args []string
		want int
	}{
		{"missing -config", nil, 2},
		{"unknown flag", []string{"-config", cfg, "-bogus"}, 2},
		{"bad -log-level", []string{"-config", cfg, "-log-level", "loud"}, 2},
		{"bad -log-format", []string{"-config", cfg, "-log-format", "xml"}, 2},
		{"non-loopback -status-addr", []string{"-config", cfg, "-status-addr", "0.0.0.0:9471"}, 2},
		{"credentials in -nats", []string{"-config", cfg, "-nats", "nats://alice:pw@127.0.0.1:4222"}, 2},
		{"plaintext remote -nats", []string{"-config", cfg, "-nats", "nats://10.0.0.5:4222"}, 2},
		{"missing -nats-creds file", []string{"-config", cfg, "-nats-creds", "/nonexistent/x.creds"}, 2},
		{"unreadable config", []string{"-config", filepath.Join(t.TempDir(), "nope.yaml")}, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			// Every case here must fail before the collector starts running; a
			// deadline turns a regression into a failure instead of a hang.
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			err := run(ctx, tc.args, &stdout, &stderr)
			if got := exitCode(err); got != tc.want {
				t.Fatalf("exitCode(run(%q)) = %d (err %v), want %d", tc.args, got, err, tc.want)
			}
		})
	}
}

func TestNewLoggerJSONAtDebug(t *testing.T) {
	var buf bytes.Buffer
	log, err := newLogger(&buf, "debug", "json")
	if err != nil {
		t.Fatal(err)
	}
	log.Debug("probe", "device", "asc-1")
	var rec map[string]any
	if err := json.Unmarshal(buf.Bytes(), &rec); err != nil {
		t.Fatalf("json format produced non-JSON %q: %v", buf.String(), err)
	}
	if rec["level"] != "DEBUG" || rec["device"] != "asc-1" {
		t.Fatalf("record = %v, want level DEBUG and device asc-1", rec)
	}
}

func TestNewLoggerLevelFilters(t *testing.T) {
	var buf bytes.Buffer
	log, err := newLogger(&buf, "warn", "text")
	if err != nil {
		t.Fatal(err)
	}
	log.Info("hidden")
	log.Warn("shown")
	if out := buf.String(); strings.Contains(out, "hidden") || !strings.Contains(out, "shown") {
		t.Fatalf("level warn wrote %q", out)
	}
}

// An unstamped `go install` build should report its module version rather
// than every such binary in the fleet claiming to be "dev".
func TestResolveVersion(t *testing.T) {
	bi := func(v string) *debug.BuildInfo { return &debug.BuildInfo{Main: debug.Module{Version: v}} }
	cases := []struct {
		name    string
		stamped string
		info    *debug.BuildInfo
		want    string
	}{
		{"ldflags wins", "v1.2.3", bi("v0.9.0"), "v1.2.3"},
		{"module version fallback", "dev", bi("v0.2.0"), "v0.2.0"},
		{"devel build stays dev", "dev", bi("(devel)"), "dev"},
		{"no build info", "dev", nil, "dev"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := resolveVersion(tc.stamped, tc.info); got != tc.want {
				t.Fatalf("resolveVersion() = %q, want %q", got, tc.want)
			}
		})
	}
}

// The surface is unauthenticated, so a non-loopback address is refused at
// boot as a usage error — for that reason, not as an unknown flag.
func TestRunRejectsNonLoopbackStatusAddr(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := run(t.Context(), []string{"-config", filepath.Join("..", "..", "collector.yaml"), "-status-addr", "0.0.0.0:9471"}, &stdout, &stderr)
	if exitCode(err) != 2 || err == nil || !strings.Contains(err.Error(), "loopback") {
		t.Fatalf("run = %v (exit %d), want a usage error about loopback", err, exitCode(err))
	}
}
