// Package httpapitest provides the fixture-replay fake used by HTTP adapter
// golden tests (ADR 0008): recorded request→response maps stand in for a
// live device.
package httpapitest

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/Vikasa2M/vikasa-collector/sdk/transport/httpapi"
	"github.com/Vikasa2M/vikasa-collector/sdk/transport/httpapi/httpfixture"
)

// Static replays fixed responses keyed by httpapi.Request.Key. A request
// with no entry gets a 404, which is how a device answers a path it does not
// serve. A fixture never holds a login answer, so a test that drives an
// authenticating adapter adds one with Set.
type Static struct {
	Responses map[string]httpapi.Response

	// Err, when set, fails every request as if the device were unreachable.
	Err error
	// FailCall fails a specific request by its 1-based call number.
	FailCall map[int]error

	mu    sync.Mutex
	calls int
	seen  []httpapi.Request
}

var _ httpapi.Client = (*Static)(nil)

// Do answers req from Responses.
func (s *Static) Do(_ context.Context, req httpapi.Request) (httpapi.Response, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	s.seen = append(s.seen, req)
	if s.Err != nil {
		return httpapi.Response{}, s.Err
	}
	if err, ok := s.FailCall[s.calls]; ok {
		return httpapi.Response{}, err
	}
	if r, ok := s.Responses[req.Key()]; ok {
		return r, nil
	}
	return httpapi.Response{Status: http.StatusNotFound, Body: []byte("not found")}, nil
}

func (s *Static) Close() error { return nil }

// Requests returns every request made so far, in order, headers included, so
// a test can check an adapter authenticated the way it should.
func (s *Static) Requests() []httpapi.Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]httpapi.Request(nil), s.seen...)
}

// Set adds or replaces the answer for key ("GET /path?query").
func (s *Static) Set(key string, status int, body string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Responses == nil {
		s.Responses = map[string]httpapi.Response{}
	}
	s.Responses[key] = httpapi.Response{Status: status, Body: []byte(body)}
}

// LoadFixture reads a recorded fixture into a Static, failing t if the file
// does not load. A fixture that does not load is a broken test, not a
// skipped one.
func LoadFixture(t testing.TB, path string) *Static {
	t.Helper()
	f, err := httpfixture.Read(path)
	if err != nil {
		t.Fatalf("load fixture: %v", err)
	}
	s := &Static{Responses: map[string]httpapi.Response{}}
	for k, x := range f.Exchanges {
		h := http.Header{}
		if x.ContentType != "" {
			h.Set("Content-Type", x.ContentType)
		}
		s.Responses[k] = httpapi.Response{Status: x.Status, Header: h, Body: []byte(x.Body)}
	}
	return s
}

// Key builds a request key from a method and path, for tests that set
// responses by hand.
func Key(method, pathAndQuery string) string {
	return fmt.Sprintf("%s %s", strings.ToUpper(method), pathAndQuery)
}
