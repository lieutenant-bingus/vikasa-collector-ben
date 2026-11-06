// Package httpapi is the HTTP transport for adapters whose device answers a
// request/response API over HTTP or HTTPS rather than SNMP — for example a
// controller's JSON MIB API or its XML event-log download.
//
// It is deliberately small: a Client issues one request and hands back the
// status and the body, bounded in size and time. What the body means, what a
// non-2xx status means, and how a device authenticates are the adapter's
// concern. A transport error is returned only when no response arrived (the
// address is unreachable, the deadline passed, the body was too large); a
// device that answered 401 or 500 answered, and the adapter decides what that
// says about the facet it was reading.
//
// Like the SNMP transport, it is read-oriented: the collector is collect-only
// (ADR 0004). POST exists because some devices put a read behind a login
// exchange; an adapter must not use it to change device state.
package httpapi

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"time"
)

// DefaultTimeout bounds one request when DialConfig.Timeout is zero.
const DefaultTimeout = 5 * time.Second

// DefaultMaxBodyBytes bounds one response body when DialConfig.MaxBodyBytes
// is zero. A controller's full event-log download is the largest body an
// adapter is expected to read; 16 MiB is well above that and well below what
// would hurt a cabinet computer.
const DefaultMaxBodyBytes = 16 << 20

// ErrBodyTooLarge is returned when a response body exceeds MaxBodyBytes.
var ErrBodyTooLarge = errors.New("httpapi: response body exceeds limit")

// Request is one HTTP request, relative to the client's base URL.
type Request struct {
	Method string     // GET when empty
	Path   string     // joined to the base URL's path, e.g. "/maxtime/api/mibs"
	Query  url.Values // optional
	Header http.Header
	Body   []byte // sent as-is; only meaningful for POST
}

// Key is the request's identity for fixtures and fakes: method, path and the
// encoded (sorted) query. Headers and bodies are not part of it — they carry
// credentials, and a recording must never hold one.
func (r Request) Key() string {
	m := r.Method
	if m == "" {
		m = http.MethodGet
	}
	k := m + " " + r.Path
	if q := r.Query.Encode(); q != "" {
		k += "?" + q
	}
	return k
}

// Response is what the device answered.
type Response struct {
	Status int
	Header http.Header
	Body   []byte
}

// OK reports a 2xx status.
func (r Response) OK() bool { return r.Status >= 200 && r.Status < 300 }

// Client issues requests to one device.
type Client interface {
	Do(ctx context.Context, req Request) (Response, error)
	Close() error
}

// DialConfig is how to reach one device.
type DialConfig struct {
	// BaseURL is scheme://host[:port][/prefix]. The scheme must be http or
	// https.
	BaseURL string
	// Timeout bounds each request, including reading the body. Zero means
	// DefaultTimeout.
	Timeout time.Duration
	// MaxBodyBytes bounds each response body. Zero means DefaultMaxBodyBytes.
	MaxBodyBytes int64
	// InsecureSkipVerify accepts any TLS certificate. Cabinet controllers
	// commonly present a self-signed certificate on the cabinet LAN (ADR
	// 0022 treats the device leg as a trusted local segment); this is the
	// operator's explicit choice, never a default.
	InsecureSkipVerify bool
}

// Dial validates cfg and returns a Client. It makes no request: HTTP has no
// session to open, and the first Read finds out whether the device answers.
func Dial(cfg DialConfig) (Client, error) {
	base, err := url.Parse(cfg.BaseURL)
	if err != nil {
		return nil, fmt.Errorf("httpapi: base url: %w", err)
	}
	if base.Scheme != "http" && base.Scheme != "https" {
		return nil, fmt.Errorf("httpapi: base url scheme %q: want http or https", base.Scheme)
	}
	if base.Host == "" {
		return nil, errors.New("httpapi: base url has no host")
	}
	if base.User != nil {
		// ADR 0022: a credential in a URL reaches logs and `ps`.
		return nil, errors.New("httpapi: base url must not carry credentials")
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = DefaultTimeout
	}
	if cfg.MaxBodyBytes <= 0 {
		cfg.MaxBodyBytes = DefaultMaxBodyBytes
	}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.Proxy = nil // a cabinet device is on the local segment, never behind a proxy
	if cfg.InsecureSkipVerify {
		tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // operator opt-in, see DialConfig
	}
	var c Client = &client{
		base:    base,
		maxBody: cfg.MaxBodyBytes,
		hc: &http.Client{
			Transport: tr,
			Timeout:   cfg.Timeout,
			// A device redirect is not followed: it would carry the request's
			// credentials to wherever the device pointed.
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	}
	if wrap := dialHook.Load(); wrap != nil {
		c = (*wrap)(cfg, c)
	}
	return c, nil
}

var dialHook atomic.Pointer[func(DialConfig, Client) Client]

// InterceptDial makes every later Dial pass its config and Client through
// wrap, until the returned func undoes it. It exists for the collector's
// fixture recorder, exactly as snmp.InterceptDial does; the polling path
// never sets it.
func InterceptDial(wrap func(DialConfig, Client) Client) (undo func()) {
	dialHook.Store(&wrap)
	return func() { dialHook.Store(nil) }
}

type client struct {
	base    *url.URL
	maxBody int64
	hc      *http.Client
}

func (c *client) Do(ctx context.Context, req Request) (Response, error) {
	method := req.Method
	if method == "" {
		method = http.MethodGet
	}
	if method != http.MethodGet && method != http.MethodPost {
		return Response{}, fmt.Errorf("httpapi: method %s not allowed (collect-only)", method)
	}
	u := *c.base
	u.Path = strings.TrimSuffix(c.base.Path, "/") + "/" + strings.TrimPrefix(req.Path, "/")
	u.RawQuery = req.Query.Encode()
	var body io.Reader
	if req.Body != nil {
		body = bytes.NewReader(req.Body)
	}
	hr, err := http.NewRequestWithContext(ctx, method, u.String(), body)
	if err != nil {
		return Response{}, fmt.Errorf("httpapi: %w", err)
	}
	for k, vs := range req.Header {
		for _, v := range vs {
			hr.Header.Add(k, v)
		}
	}
	resp, err := c.hc.Do(hr)
	if err != nil {
		return Response{}, fmt.Errorf("httpapi: %s %s: %w", method, req.Path, scrub(err))
	}
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(io.LimitReader(resp.Body, c.maxBody+1))
	if err != nil {
		return Response{}, fmt.Errorf("httpapi: %s %s: read body: %w", method, req.Path, err)
	}
	if int64(len(b)) > c.maxBody {
		return Response{}, fmt.Errorf("%w (%d bytes) on %s %s", ErrBodyTooLarge, c.maxBody, method, req.Path)
	}
	return Response{Status: resp.StatusCode, Header: resp.Header, Body: b}, nil
}

func (c *client) Close() error {
	c.hc.CloseIdleConnections()
	return nil
}

// scrub drops the URL from a *url.Error so a query string never reaches a
// log line; the method and path are already in the wrapping message.
func scrub(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Err
	}
	return err
}
