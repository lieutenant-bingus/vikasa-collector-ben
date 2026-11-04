package httpapi_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Vikasa2M/vikasa-collector/sdk/transport/httpapi"
)

func TestDialRejectsBadBaseURL(t *testing.T) {
	for _, u := range []string{"", "ftp://h", "http://", "http://user:pw@h", "://bad"} {
		if _, err := httpapi.Dial(httpapi.DialConfig{BaseURL: u}); err == nil {
			t.Errorf("Dial(%q): want error", u)
		}
	}
}

func TestDoJoinsPathQueryAndHeaders(t *testing.T) {
	var got *http.Request
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	c, err := httpapi.Dial(httpapi.DialConfig{BaseURL: srv.URL + "/prefix/"})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	resp, err := c.Do(context.Background(), httpapi.Request{
		Path:   "/maxtime/api/mibs",
		Query:  url.Values{"mibs": {"A,B"}},
		Header: http.Header{"Authorization": {"Bearer t"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !resp.OK() || string(resp.Body) != `{"ok":true}` {
		t.Fatalf("resp = %d %q", resp.Status, resp.Body)
	}
	if got.URL.Path != "/prefix/maxtime/api/mibs" || got.URL.Query().Get("mibs") != "A,B" {
		t.Errorf("request URL = %s", got.URL)
	}
	if got.Header.Get("Authorization") != "Bearer t" {
		t.Errorf("Authorization header not sent")
	}
}

func TestNon2xxIsAResponseNotAnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	c, _ := httpapi.Dial(httpapi.DialConfig{BaseURL: srv.URL})
	resp, err := c.Do(context.Background(), httpapi.Request{Path: "/x"})
	if err != nil || resp.Status != http.StatusUnauthorized || resp.OK() {
		t.Fatalf("got %d, %v; want 401 and no error", resp.Status, err)
	}
}

func TestBodyLimit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat("x", 101)))
	}))
	defer srv.Close()
	c, _ := httpapi.Dial(httpapi.DialConfig{BaseURL: srv.URL, MaxBodyBytes: 100})
	if _, err := c.Do(context.Background(), httpapi.Request{Path: "/"}); !errors.Is(err, httpapi.ErrBodyTooLarge) {
		t.Fatalf("err = %v, want ErrBodyTooLarge", err)
	}
}

func TestTimeoutAndUnreachableAreErrors(t *testing.T) {
	block := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		select {
		case <-block:
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()
	defer close(block)
	c, _ := httpapi.Dial(httpapi.DialConfig{BaseURL: srv.URL, Timeout: 50 * time.Millisecond})
	if _, err := c.Do(context.Background(), httpapi.Request{Path: "/"}); err == nil {
		t.Fatal("want timeout error")
	}
}

func TestRedirectNotFollowedAndQueryNotLogged(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://elsewhere.invalid/", http.StatusFound)
	}))
	defer srv.Close()
	c, _ := httpapi.Dial(httpapi.DialConfig{BaseURL: srv.URL})
	resp, err := c.Do(context.Background(), httpapi.Request{Path: "/"})
	if err != nil || resp.Status != http.StatusFound {
		t.Fatalf("got %d, %v; want the 302 itself", resp.Status, err)
	}

	dead, _ := httpapi.Dial(httpapi.DialConfig{BaseURL: "http://127.0.0.1:1", Timeout: 200 * time.Millisecond})
	_, err = dead.Do(context.Background(), httpapi.Request{Path: "/p", Query: url.Values{"secret": {"s3cr3t"}}})
	if err == nil || strings.Contains(err.Error(), "s3cr3t") {
		t.Fatalf("err = %v; want an error that does not carry the query", err)
	}
}

func TestOnlyGetAndPost(t *testing.T) {
	c, _ := httpapi.Dial(httpapi.DialConfig{BaseURL: "http://127.0.0.1:1"})
	for _, m := range []string{http.MethodPut, http.MethodDelete, http.MethodPatch} {
		if _, err := c.Do(context.Background(), httpapi.Request{Method: m, Path: "/"}); err == nil || !strings.Contains(err.Error(), "collect-only") {
			t.Errorf("%s: err = %v, want collect-only refusal", m, err)
		}
	}
}

func TestRequestKey(t *testing.T) {
	r := httpapi.Request{Path: "/a", Query: url.Values{"b": {"2"}, "a": {"1"}}}
	if k := r.Key(); k != "GET /a?a=1&b=2" {
		t.Errorf("Key = %q", k)
	}
	r.Method = http.MethodPost
	if k := r.Key(); k != "POST /a?a=1&b=2" {
		t.Errorf("Key = %q", k)
	}
}
