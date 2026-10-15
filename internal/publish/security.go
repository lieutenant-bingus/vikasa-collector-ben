package publish

import (
	"fmt"
	"net"
	neturl "net/url"
	"os"
	"strings"
)

// Security is how the collector authenticates to and verifies its broker
// (ADR 0022).
type Security struct {
	// CredsFile is a NATS .creds file (user JWT and nkey seed): the only
	// way the collector takes a broker credential. Never the URL.
	CredsFile string
	// CAFile verifies a tls:// broker against a private CA; empty means
	// the system roots. Verification is never skipped.
	CAFile string
}

// ValidateSecurity enforces ADR 0022 at boot:
//   - no server URL carries credentials — they would be visible to ps, shell
//     history and unit files, and are where #29's leaks came from;
//   - a non-loopback server must be tls://, so nothing crosses a network in
//     plaintext;
//   - the creds file exists and grants nothing to other users, as ssh insists
//     of a private key; the CA file exists.
//
// No error repeats a URL's credentials: a parse failure names only the
// redacted form, since the parser's own message would quote the input.
func ValidateSecurity(servers string, sec Security) error {
	for part := range strings.SplitSeq(servers, ",") {
		raw := strings.TrimSpace(part)
		u, err := neturl.Parse(raw)
		if err != nil || u.Host == "" {
			return fmt.Errorf("-nats %s is not a valid URL (parser detail withheld: it would quote any credentials)", redactURL(raw))
		}
		if u.User != nil {
			return fmt.Errorf("-nats %s: credentials in the URL are refused; use -nats-creds (ADR 0022)", redactURL(raw))
		}
		if !loopback(u.Hostname()) && u.Scheme != "tls" {
			return fmt.Errorf("-nats %s: a non-loopback broker must use tls:// (ADR 0022)", redactURL(raw))
		}
	}
	if sec.CredsFile != "" {
		fi, err := os.Stat(sec.CredsFile)
		if err != nil {
			return fmt.Errorf("nats creds: %w", err)
		}
		if fi.Mode().Perm()&0o007 != 0 {
			return fmt.Errorf("nats creds %s is readable by other users (mode %04o); install it 0640 or stricter", sec.CredsFile, fi.Mode().Perm())
		}
	}
	if sec.CAFile != "" {
		if _, err := os.Stat(sec.CAFile); err != nil {
			return fmt.Errorf("nats CA: %w", err)
		}
	}
	return nil
}

func loopback(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
