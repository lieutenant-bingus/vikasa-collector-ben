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
		return &asc{deviceID: deviceID, api: cfg.api(c), now: time.Now}, nil
	})
}

type httpConfig struct {
	dial     httpapi.DialConfig
	prefix   string
	auth     authScheme
	username string
	password string
	batch    bool
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
		"base_url", "auth", "username", "password", "timeout", "insecure_skip_verify", "mib_batch"); err != nil {
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
	if cfg.dial.InsecureSkipVerify, err = optBool(raw, "insecure_skip_verify", false); err != nil {
		return httpConfig{}, err
	}
	if cfg.batch, err = optBool(raw, "mib_batch", true); err != nil {
		return httpConfig{}, err
	}
	return cfg, nil
}

func optBool(raw map[string]any, key string, def bool) (bool, error) {
	v, ok := raw[key]
	if !ok {
		return def, nil
	}
	b, ok := v.(bool)
	if !ok {
		return false, fmt.Errorf("%s.%s must be true or false", blockPath, key)
	}
	return b, nil
}
