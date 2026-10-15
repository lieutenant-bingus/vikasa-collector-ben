package publish

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ADR 0022 decisions 1 and 2, enforced at boot (ADR 0014): no secret in the
// URL, no plaintext off loopback, no credential file others can read.
func TestValidateSecurity(t *testing.T) {
	dir := t.TempDir()
	write := func(name string, mode os.FileMode) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte("x"), mode); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(p, mode); err != nil {
			t.Fatal(err)
		}
		return p
	}
	good := write("ok.creds", 0o640)
	worldReadable := write("bad.creds", 0o644)
	ca := write("ca.pem", 0o644)

	cases := []struct {
		name, url string
		sec       Security
		wantErr   string // "" means valid
	}{
		{"loopback plaintext", "nats://127.0.0.1:4222", Security{}, ""},
		{"localhost plaintext", "nats://localhost:4222", Security{}, ""},
		{"ipv6 loopback", "nats://[::1]:4222", Security{}, ""},
		{"remote tls", "tls://broker.example:4222", Security{CAFile: ca}, ""},
		{"creds file", "nats://127.0.0.1:4222", Security{CredsFile: good}, ""},
		{"userinfo password", "nats://alice:hunter2@127.0.0.1:4222", Security{}, "credentials in the URL"},
		{"userinfo token", "nats://s3cr3t@127.0.0.1:4222", Security{}, "credentials in the URL"},
		{"userinfo in second server", "nats://127.0.0.1:4222,nats://bob:x@127.0.0.1:4223", Security{}, "credentials in the URL"},
		{"remote plaintext", "nats://10.0.0.5:4222", Security{}, "tls://"},
		{"remote plaintext by name", "nats://broker.example:4222", Security{}, "tls://"},
		{"world-readable creds", "nats://127.0.0.1:4222", Security{CredsFile: worldReadable}, "readable by other users"},
		{"missing creds", "nats://127.0.0.1:4222", Security{CredsFile: filepath.Join(dir, "nope")}, "nats creds"},
		{"missing ca", "tls://broker.example:4222", Security{CAFile: filepath.Join(dir, "nope")}, "nats CA"},
		{"unparseable", "nats://alice:hun ter2@x:1", Security{}, "not a valid URL"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateSecurity(tc.url, tc.sec)
			switch {
			case tc.wantErr == "" && err != nil:
				t.Fatalf("ValidateSecurity = %v, want ok", err)
			case tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)):
				t.Fatalf("ValidateSecurity = %v, want an error containing %q", err, tc.wantErr)
			}
			if err != nil && (strings.Contains(err.Error(), "hunter2") || strings.Contains(err.Error(), "s3cr3t") || strings.Contains(err.Error(), "ter2")) {
				t.Fatalf("the error leaks the credential: %v", err)
			}
		})
	}
}
