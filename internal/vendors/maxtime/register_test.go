package maxtime

import (
	"strings"
	"testing"
	"time"

	"github.com/Vikasa2M/vikasa-collector/sdk/adapter"
)

func block(kv map[string]any) map[string]any { return map[string]any{"http": kv} }

func TestParseHTTPBlock(t *testing.T) {
	cfg, err := parseHTTPBlock(block(map[string]any{
		"base_url": "https://10.0.0.5/maxtime-cv/", "auth": "basic", "username": "u", "password": "p",
		"timeout": "3s", "insecure_skip_verify": true, "mib_batch": false,
	}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.dial.BaseURL != "https://10.0.0.5" || cfg.prefix != "/maxtime-cv" || cfg.auth != authBasic ||
		cfg.dial.Timeout != 3*time.Second || !cfg.dial.InsecureSkipVerify || cfg.batch {
		t.Errorf("cfg = %+v", cfg)
	}
	cfg, err = parseHTTPBlock(block(map[string]any{"base_url": "http://10.0.0.5"}))
	if err != nil || cfg.prefix != "/maxtime" || !cfg.batch || cfg.auth != authNone {
		t.Errorf("defaults: %+v, %v", cfg, err)
	}
}

func TestParseHTTPBlockRejects(t *testing.T) {
	for name, tc := range map[string]struct {
		conn map[string]any
		want string
	}{
		"no block":         {map[string]any{}, "connection.http block required"},
		"unknown key":      {block(map[string]any{"base_url": "http://h", "asc_log": true}), "asc_log"},
		"no base url":      {block(map[string]any{}), "base_url required"},
		"creds in url":     {block(map[string]any{"base_url": "http://u:p@h"}), "must not carry credentials"},
		"user, no auth":    {block(map[string]any{"base_url": "http://h", "username": "u", "password": "p"}), "auth required"},
		"auth, no pw":      {block(map[string]any{"base_url": "http://h", "auth": "basic", "username": "u"}), "needs username and password"},
		"bad auth":         {block(map[string]any{"base_url": "http://h", "auth": "digest", "username": "u", "password": "p"}), "want basic or maxprofile"},
		"bad timeout":      {block(map[string]any{"base_url": "http://h", "timeout": 5}), "duration string"},
		"zero timeout":     {block(map[string]any{"base_url": "http://h", "timeout": "0s"}), "positive"},
		"string bool":      {block(map[string]any{"base_url": "http://h", "mib_batch": "no"}), "true or false"},
		"stray top key":    {map[string]any{"http": map[string]any{"base_url": "http://h"}, "snmp": map[string]any{}}, "snmp"},
		"query in baseurl": {block(map[string]any{"base_url": "http://h/maxtime?x=1"}), "query"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := parseHTTPBlock(tc.conn)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want it to mention %q", err, tc.want)
			}
		})
	}
}

func TestRegistered(t *testing.T) {
	r := adapter.NewRegistry()
	RegisterTo(r)
	a, err := r.Build("maxtime", "asc", "asc-1", block(map[string]any{"base_url": "http://127.0.0.1:1"}))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = a.Close() }()
	if _, ok := a.(adapter.StateReader); !ok || a.Descriptor().Key() != "maxtime-asc" {
		t.Errorf("built %T %s", a, a.Descriptor().Key())
	}
}
