package maxtime

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/Vikasa2M/vikasa-collector/sdk/adapter"
	"github.com/Vikasa2M/vikasa-collector/sdk/transport/httpapi"
)

// RegisterTo registers maxtime-asc. The connection block is
//
//	connection:
//	  http:
//	    base_url: "http://192.168.0.20/maxtime"  # controller + MIB API prefix
//	    auth: basic                              # basic | maxprofile; required with username
//	    username: "..."
//	    password: "..."
//	    timeout: "5s"                            # optional, per request
//	    insecure_skip_verify: false              # optional, https with a self-signed cert
//	    mib_batch: true                          # optional; false = one GET per object
//	    asclog:                                  # optional; the high-resolution log
//	      timezone: "America/New_York"           # required: the controller clock's zone
//	      interval: "15s"                        # optional
//	      timeout: "60s"                         # optional, per log request
//	      path: "/v1/asclog/xml/full"            # optional
//	      since: true                            # optional; false = download the whole ring
//	      enabled: true                          # optional; false keeps the block, reads nothing
//
// base_url's path is the MIB API prefix: /maxtime on the standard build,
// /maxtime-cv on the CV build. It defaults to /maxtime. Credentials live in
// collector.yaml, which is therefore a secret (ADR 0022), and never in the
// URL.
func RegisterTo(r *adapter.Registry) {
	r.Register(ascDescriptor, func(deviceID string, conn map[string]any) (adapter.Adapter, error) {
		cfg, err := parseHTTPBlock(conn)
		if err != nil {
			return nil, err // Registry.Build adds the key; app adds the device
		}
		c, err := httpapi.Dial(cfg.dial)
		if err != nil {
			return nil, err
		}
		a := &asc{deviceID: deviceID, api: cfg.api(c), now: time.Now}
		if cfg.log == nil {
			return a, nil
		}
		// The log has its own client: a first read downloads the whole ring
		// (about 500 KB on the 2.15.1 lab controller, more than 5 s), which
		// must not stretch the state poll's per-request timeout.
		ld := cfg.dial
		ld.Timeout = cfg.log.timeout
		lc, err := httpapi.Dial(ld)
		if err != nil {
			_ = c.Close()
			return nil, err
		}
		return &ascWithLog{asc: a, log: &ascLog{api: cfg.api(lc), cfg: *cfg.log, now: time.Now}}, nil
	})
}

type httpConfig struct {
	dial     httpapi.DialConfig
	prefix   string
	auth     authScheme
	username string
	password string
	batch    bool
	log      *logConfig // nil: the high-resolution log is not read
}

func (c httpConfig) api(client httpapi.Client) *mibAPI {
	return &mibAPI{client: client, prefix: c.prefix, auth: c.auth, username: c.username, password: c.password, batch: c.batch}
}

const blockPath = "connection.http"

// parseHTTPBlock is the trust boundary for the connection block (ADR 0014):
// an unknown key, or a known key of the wrong type, fails boot.
func parseHTTPBlock(conn map[string]any) (httpConfig, error) {
	if err := adapter.OnlyKeys("connection", conn, "http"); err != nil {
		return httpConfig{}, err
	}
	raw, ok := conn["http"].(map[string]any)
	if !ok {
		return httpConfig{}, errors.New("connection.http block required")
	}
	if err := adapter.OnlyKeys(blockPath, raw,
		"base_url", "auth", "username", "password", "timeout", "insecure_skip_verify", "mib_batch", "asclog"); err != nil {
		return httpConfig{}, err
	}
	base, err := adapter.OptString(blockPath, raw, "base_url")
	if err != nil {
		return httpConfig{}, err
	}
	if base == "" {
		return httpConfig{}, errors.New("connection.http.base_url required")
	}
	u, err := url.Parse(base)
	if err != nil {
		return httpConfig{}, fmt.Errorf("connection.http.base_url: %w", err)
	}
	if u.User != nil {
		return httpConfig{}, errors.New("connection.http.base_url must not carry credentials; use username and password")
	}
	if u.RawQuery != "" || u.Fragment != "" {
		return httpConfig{}, errors.New("connection.http.base_url must not carry a query or fragment")
	}
	cfg := httpConfig{prefix: strings.TrimSuffix(u.Path, "/"), batch: true}
	if cfg.prefix == "" {
		cfg.prefix = "/maxtime"
	}
	u.Path = ""
	cfg.dial = httpapi.DialConfig{BaseURL: u.String(), Timeout: httpapi.DefaultTimeout}

	auth, err := adapter.OptString(blockPath, raw, "auth")
	if err != nil {
		return httpConfig{}, err
	}
	if cfg.username, err = adapter.OptString(blockPath, raw, "username"); err != nil {
		return httpConfig{}, err
	}
	if cfg.password, err = adapter.OptString(blockPath, raw, "password"); err != nil {
		return httpConfig{}, err
	}
	switch authScheme(auth) {
	case authNone:
		if cfg.username != "" || cfg.password != "" {
			return httpConfig{}, errors.New("connection.http.auth required with username/password: basic or maxprofile")
		}
	case authBasic, authMaxProfile:
		if cfg.username == "" || cfg.password == "" {
			return httpConfig{}, fmt.Errorf("connection.http.auth %s needs username and password", auth)
		}
		cfg.auth = authScheme(auth)
	default:
		return httpConfig{}, fmt.Errorf("connection.http.auth %q: want basic or maxprofile", auth)
	}

	if v, ok := raw["timeout"]; ok {
		s, ok := v.(string)
		if !ok {
			return httpConfig{}, errors.New("connection.http.timeout must be a duration string, e.g. \"5s\"")
		}
		d, err := time.ParseDuration(s)
		if err != nil || d <= 0 {
			return httpConfig{}, fmt.Errorf("connection.http.timeout %q: want a positive duration", s)
		}
		cfg.dial.Timeout = d
	}
	if cfg.dial.InsecureSkipVerify, err = optBool(blockPath, raw, "insecure_skip_verify", false); err != nil {
		return httpConfig{}, err
	}
	if cfg.batch, err = optBool(blockPath, raw, "mib_batch", true); err != nil {
		return httpConfig{}, err
	}
	if cfg.log, err = parseLogBlock(raw); err != nil {
		return httpConfig{}, err
	}
	return cfg, nil
}

const logPath = blockPath + ".asclog"

// parseLogBlock reads connection.http.asclog. Its presence enables the log.
// timezone is required rather than defaulted to the collector host's: the
// controller logs its own wall clock, and a wrong zone shifts every event.
func parseLogBlock(raw map[string]any) (*logConfig, error) {
	v, ok := raw["asclog"]
	if !ok {
		return nil, nil
	}
	b, ok := v.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s must be a mapping", logPath)
	}
	if err := adapter.OnlyKeys(logPath, b, "enabled", "timezone", "interval", "timeout", "path", "since"); err != nil {
		return nil, err
	}
	enabled, err := optBool(logPath, b, "enabled", true)
	if err != nil {
		return nil, err
	}
	if !enabled {
		return nil, nil
	}
	cfg := &logConfig{path: defaultLogPath, interval: 15 * time.Second, timeout: 60 * time.Second}
	tz, err := adapter.OptString(logPath, b, "timezone")
	if err != nil {
		return nil, err
	}
	if tz == "" {
		return nil, fmt.Errorf("%s.timezone required: the controller clock's IANA zone, e.g. \"America/New_York\"", logPath)
	}
	if cfg.loc, err = time.LoadLocation(tz); err != nil {
		return nil, fmt.Errorf("%s.timezone %q: %w", logPath, tz, err)
	}
	if iv, ok := b["interval"]; ok {
		s, ok := iv.(string)
		d, err := time.ParseDuration(s)
		if !ok || err != nil || d < time.Second {
			return nil, fmt.Errorf("%s.interval %v: want a duration of at least 1s", logPath, iv)
		}
		cfg.interval = d
	}
	if tv, ok := b["timeout"]; ok {
		s, ok := tv.(string)
		d, err := time.ParseDuration(s)
		if !ok || err != nil || d <= 0 {
			return nil, fmt.Errorf("%s.timeout %v: want a positive duration", logPath, tv)
		}
		cfg.timeout = d
	}
	p, err := adapter.OptString(logPath, b, "path")
	if err != nil {
		return nil, err
	}
	if p != "" {
		if !strings.HasPrefix(p, "/") || strings.ContainsAny(p, "?#") {
			return nil, fmt.Errorf("%s.path %q: want an absolute path with no query", logPath, p)
		}
		cfg.path = p
	}
	if cfg.since, err = optBool(logPath, b, "since", true); err != nil {
		return nil, err
	}
	return cfg, nil
}

func optBool(path string, raw map[string]any, key string, def bool) (bool, error) {
	v, ok := raw[key]
	if !ok {
		return def, nil
	}
	b, ok := v.(bool)
	if !ok {
		return false, fmt.Errorf("%s.%s must be true or false", path, key)
	}
	return b, nil
}
