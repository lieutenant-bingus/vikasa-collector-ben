package manage

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Vikasa2M/vikasa-collector/internal/stats"
)

type fakeSource struct{ ready atomic.Bool }

func (f *fakeSource) Ready() bool { return f.ready.Load() }
func (f *fakeSource) Status() Status {
	return Status{Version: "v1.2.3", BootID: "abc", Ready: f.ready.Load(),
		Devices: []stats.DeviceStatus{{ID: "asc-1", ConsecutiveFailures: 2, LastError: "timeout"}}}
}

func get(t *testing.T, h http.Handler, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), method, path, nil)
	req.Host = "127.0.0.1:9471" // httptest defaults to example.com, which is refused
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestReadyzFollowsTheSource(t *testing.T) {
	src := &fakeSource{}
	h := Handler(src)
	if code := get(t, h, http.MethodGet, "/readyz").Code; code != http.StatusServiceUnavailable {
		t.Fatalf("/readyz before ready = %d, want 503", code)
	}
	src.ready.Store(true)
	if code := get(t, h, http.MethodGet, "/readyz").Code; code != http.StatusOK {
		t.Fatalf("/readyz when ready = %d, want 200", code)
	}
}

// /livez is deliberately weak: it answers whether the process responds, and
// is 200 even before readiness — restarting an unready-but-alive collector
// is exactly the restart loop the readiness latch exists to prevent.
func TestLivezIsUpRegardlessOfReadiness(t *testing.T) {
	if code := get(t, Handler(&fakeSource{}), http.MethodGet, "/livez").Code; code != http.StatusOK {
		t.Fatalf("/livez = %d, want 200", code)
	}
}

func TestStatusIsJSON(t *testing.T) {
	rec := get(t, Handler(&fakeSource{}), http.MethodGet, "/status")
	if rec.Code != http.StatusOK || !strings.HasPrefix(rec.Header().Get("Content-Type"), "application/json") {
		t.Fatalf("/status = %d %q", rec.Code, rec.Header().Get("Content-Type"))
	}
	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"version", "boot_id", "ready", "uptime_s", "broker", "counters", "devices"} {
		if _, ok := got[k]; !ok {
			t.Errorf("/status lacks %q: %s", k, rec.Body.String())
		}
	}
}

// Read-only surface: nothing here accepts a write.
func TestEndpointsAreGETOnly(t *testing.T) {
	h := Handler(&fakeSource{})
	for _, path := range []string{"/readyz", "/livez", "/status"} {
		if code := get(t, h, http.MethodPost, path).Code; code != http.StatusMethodNotAllowed {
			t.Errorf("POST %s = %d, want 405", path, code)
		}
	}
}

// The surface is local by design (management-surface spec §1): it carries
// device errors and identity, and is not authenticated. A non-loopback
// address is refused at boot rather than exposing it to the cabinet LAN.
func TestValidateAddrIsLoopbackOnly(t *testing.T) {
	for addr, ok := range map[string]bool{
		"":                true, // disabled
		"127.0.0.1:8081":  true,
		"[::1]:8081":      true,
		"localhost:8081":  true,
		"0.0.0.0:8081":    false,
		":8081":           false,
		"10.0.0.5:8081":   false,
		"example.com:80":  false,
		"127.0.0.1":       false, // no port
		"127.0.0.1:99999": false,
	} {
		if err := ValidateAddr(addr); (err == nil) != ok {
			t.Errorf("ValidateAddr(%q) = %v, want ok=%v", addr, err, ok)
		}
	}
}

// DNS rebinding: a page in a browser on this host can re-point its own
// domain at 127.0.0.1 and read the surface — the loopback bind alone does
// not stop it, because the request arrives over loopback. The Host header
// still names the attacker's domain, so only loopback hosts are answered.
func TestOnlyLoopbackHostHeadersAreServed(t *testing.T) {
	h := Handler(&fakeSource{})
	for host, want := range map[string]int{
		"127.0.0.1:9471":         http.StatusOK,
		"localhost:9471":         http.StatusOK,
		"[::1]:9471":             http.StatusOK,
		"127.0.0.1":              http.StatusOK,
		"evil.example:9471":      http.StatusMisdirectedRequest,
		"evil.example":           http.StatusMisdirectedRequest,
		"10.0.0.5:9471":          http.StatusMisdirectedRequest,
		"localhost.evil.example": http.StatusMisdirectedRequest,
		"127.0.0.1.nip.io:9471":  http.StatusMisdirectedRequest,
	} {
		for _, path := range []string{"/status", "/readyz", "/livez"} {
			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, nil)
			req.Host = host
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if path == "/readyz" && want == http.StatusOK {
				want = http.StatusServiceUnavailable // fakeSource is not ready
			}
			if rec.Code != want {
				t.Errorf("GET %s with Host %q = %d, want %d", path, host, rec.Code, want)
			}
			if path == "/readyz" && want == http.StatusServiceUnavailable {
				want = http.StatusOK
			}
		}
	}
}
