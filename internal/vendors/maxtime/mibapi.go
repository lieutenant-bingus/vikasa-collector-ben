package maxtime

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"

	"github.com/Vikasa2M/vikasa-collector/sdk/transport/httpapi"
)

// authScheme is how the controller's web API wants to be authenticated.
type authScheme string

const (
	authNone       authScheme = ""
	authBasic      authScheme = "basic"      // HTTP Basic on every request (curl -u)
	authMaxProfile authScheme = "maxprofile" // MaxProfile login, then a session token header
)

// maxProfileLoginPath is the MaxProfile password login. It answers a session
// token which the MIB API takes in tokenHeader.
const (
	maxProfileLoginPath = "/maxprofile/accounts/loginWithPassword"
	tokenHeader         = "accounts-access-token"
)

// rows is one MIB object as the API answers it: row index → value. A scalar
// has the single row "1".
type rows map[string]string

// mibAPI reads MIB objects through MaxTime's JSON MIB API:
//
//	GET <prefix>/api/mibs/<name>[,<name>...]/  →  [{"name": "...", "data": {"<row>": "<value>"}}]
//
// It is the adapter's side of the transport boundary: httpapi moves bytes,
// this knows what the controller's API looks like.
type mibAPI struct {
	client   httpapi.Client
	prefix   string // e.g. "/maxtime" or "/maxtime-cv"
	auth     authScheme
	username string
	password string
	batch    bool

	mu    sync.Mutex
	token string
}

// errAuth marks a request the controller refused on credentials. It is a
// whole-device failure, not a facet one: no facet is readable.
var errAuth = errors.New("controller refused the credentials")

// get reads names and returns what the controller answered, keyed by name.
// A name the controller did not answer is absent from the result, never an
// empty entry: the facet code decides what absence means. The error is
// non-nil only when nothing could be read at all.
func (m *mibAPI) get(ctx context.Context, names ...string) (map[string]rows, error) {
	if m.batch {
		return m.getOnce(ctx, names)
	}
	out := map[string]rows{}
	var firstErr error
	for _, n := range names {
		got, err := m.getOnce(ctx, []string{n})
		if err != nil {
			if errors.Is(err, errAuth) || ctx.Err() != nil {
				return nil, err
			}
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		for k, v := range got {
			out[k] = v
		}
	}
	if len(out) == 0 && firstErr != nil {
		return nil, firstErr
	}
	return out, nil
}

func (m *mibAPI) getOnce(ctx context.Context, names []string) (map[string]rows, error) {
	req := httpapi.Request{Path: m.prefix + "/api/mibs/" + strings.Join(names, ",") + "/"}
	resp, err := m.do(ctx, req)
	if err != nil {
		return nil, err
	}
	if !resp.OK() {
		return nil, fmt.Errorf("GET %s: HTTP %d", req.Path, resp.Status)
	}
	return decodeMIBs(resp.Body)
}

// do sends req with the configured credentials. A MaxProfile session that
// has expired answers 401; do logs in again once and retries.
func (m *mibAPI) do(ctx context.Context, req httpapi.Request) (httpapi.Response, error) {
	switch m.auth {
	case authBasic:
		req.Header = http.Header{"Authorization": {"Basic " + base64.StdEncoding.EncodeToString([]byte(m.username+":"+m.password))}}
	case authMaxProfile:
		tok, err := m.session(ctx)
		if err != nil {
			return httpapi.Response{}, err
		}
		req.Header = http.Header{}
		req.Header.Set(tokenHeader, tok)
		resp, err := m.client.Do(ctx, req)
		if err != nil || resp.Status != http.StatusUnauthorized {
			return refused(req, resp, err)
		}
		m.dropSession(tok)
		if tok, err = m.session(ctx); err != nil {
			return httpapi.Response{}, err
		}
		req.Header = http.Header{}
		req.Header.Set(tokenHeader, tok)
	}
	resp, err := m.client.Do(ctx, req)
	return refused(req, resp, err)
}

// refused turns a 401 or 403 into errAuth, so a wrong password fails the
// whole poll — and the device's comm health — rather than every facet.
func refused(req httpapi.Request, resp httpapi.Response, err error) (httpapi.Response, error) {
	if err == nil && (resp.Status == http.StatusUnauthorized || resp.Status == http.StatusForbidden) {
		return resp, fmt.Errorf("GET %s: HTTP %d: %w", req.Path, resp.Status, errAuth)
	}
	return resp, err
}

func (m *mibAPI) session(ctx context.Context) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.token != "" {
		return m.token, nil
	}
	var body struct {
		User struct {
			Username string `json:"username"`
		} `json:"user"`
		Password string `json:"password"`
	}
	body.User.Username, body.Password = m.username, m.password
	payload, err := json.Marshal(body)
	if err != nil {
		return "", err
	}
	// The MaxProfile endpoint takes the JSON document itself, query-escaped,
	// as a form-encoded body.
	resp, err := m.client.Do(ctx, httpapi.Request{
		Method: http.MethodPost,
		Path:   maxProfileLoginPath,
		Header: http.Header{"Content-Type": {"application/x-www-form-urlencoded"}},
		Body:   []byte(url.QueryEscape(string(payload))),
	})
	if err != nil {
		return "", fmt.Errorf("maxprofile login: %w", err)
	}
	if !resp.OK() {
		return "", fmt.Errorf("maxprofile login: HTTP %d: %w", resp.Status, errAuth)
	}
	var out struct {
		Data struct {
			Tokens struct {
				AccessToken string `json:"accessToken"`
			} `json:"tokens"`
		} `json:"data"`
	}
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		return "", fmt.Errorf("maxprofile login: decode: %w", err)
	}
	if out.Data.Tokens.AccessToken == "" {
		return "", fmt.Errorf("maxprofile login: no access token: %w", errAuth)
	}
	m.token = out.Data.Tokens.AccessToken
	return m.token, nil
}

func (m *mibAPI) dropSession(tok string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.token == tok {
		m.token = ""
	}
}

// decodeMIBs parses the API's answer. Values arrive as JSON strings on the
// firmware recorded so far; a number is accepted too, rather than failing a
// poll over a quoting change.
func decodeMIBs(body []byte) (map[string]rows, error) {
	var recs []struct {
		Name string                     `json:"name"`
		Data map[string]json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(body, &recs); err != nil {
		return nil, fmt.Errorf("decode MIB answer: %w", err)
	}
	out := make(map[string]rows, len(recs))
	for _, r := range recs {
		if r.Name == "" || r.Data == nil {
			continue // not an answer for any object we can name
		}
		rs := make(rows, len(r.Data))
		for idx, raw := range r.Data {
			var s string
			if err := json.Unmarshal(raw, &s); err != nil {
				s = strings.TrimSpace(string(raw))
			}
			rs[idx] = s
		}
		out[r.Name] = rs
	}
	return out, nil
}

// scalar is row "1" of an object, as an integer.
func (r rows) scalar(name string) (int64, error) {
	v, ok := r["1"]
	if !ok {
		return 0, fmt.Errorf("%s: row 1 absent", name)
	}
	n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%s: %q is not an integer", name, v)
	}
	return n, nil
}

// table is every row of an object, keyed by its 1-based integer index.
func (r rows) table(name string) (map[uint32]int64, error) {
	out := make(map[uint32]int64, len(r))
	for idx, v := range r {
		i, err := strconv.ParseUint(idx, 10, 32)
		if err != nil || i == 0 {
			return nil, fmt.Errorf("%s: row index %q", name, idx)
		}
		n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
		if err != nil {
			return nil, fmt.Errorf("%s row %s: %q is not an integer", name, idx, v)
		}
		out[uint32(i)] = n
	}
	return out, nil
}
