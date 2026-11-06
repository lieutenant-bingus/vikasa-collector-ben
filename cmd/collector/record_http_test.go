package main

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Vikasa2M/vikasa-collector/sdk/transport/httpapi/httpfixture"
)

// A MaxTime with the event log configured dials two HTTP clients: the state
// poll's and the log's. Both must record into the one fixture (the lab found
// a state-only recording came out empty when asclog was set).
func TestRecordingAMaxTimeWithItsLogConfigured(t *testing.T) {
	lab, err := httpfixture.Read("../../internal/vendors/maxtime/testdata/asc-lab-2.15.1.json")
	if err != nil {
		t.Fatal(err)
	}
	const logBody = `<EventResponses><EventResponse><Event ID="1" TimeStamp="11-04-2026 14:55:15.4" EventTypeID="150" Parameter="5"/></EventResponse></EventResponses>`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/asclog/xml/full" {
			_, _ = w.Write([]byte(logBody))
			return
		}
		// Any MIB batch gets the lab answer: what is under test is that the
		// recorder keeps it, not which objects the adapter asks for.
		if !strings.HasPrefix(r.URL.Path, "/maxtime/api/mibs/") {
			http.NotFound(w, r)
			return
		}
		for k, x := range lab.Exchanges {
			if strings.HasPrefix(k, "GET /maxtime/api/mibs/") {
				_, _ = w.Write([]byte(x.Body))
				return
			}
		}
	}))
	defer srv.Close()

	cfg := fmt.Sprintf(`
collector_id: cabinet-042-collector
region: us-ga
agency: metro
agency_unit: d01
site: cabinet-042
model_version: openits/v1
devices:
  - id: asc-1
    vendor: maxtime
    device_kind: asc
    connection:
      http:
        base_url: %s/maxtime
        asclog: { timezone: America/New_York }
`, srv.URL)
	cfgPath := filepath.Join(t.TempDir(), "collector.yaml")
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, tc := range map[string]struct {
		extra []string
		want  int
	}{
		"state only": {nil, 1},
		"with log":   {[]string{"-events"}, 2},
	} {
		t.Run(name, func(t *testing.T) {
			out := filepath.Join(t.TempDir(), "f.json")
			msg, err := record(t, cfgPath, out, tc.extra...)
			if err != nil {
				t.Fatalf("record: %v\n%s", err, msg)
			}
			f, err := httpfixture.Read(out)
			if err != nil {
				t.Fatal(err)
			}
			if len(f.Exchanges) != tc.want {
				t.Fatalf("%d exchanges, want %d: %v", len(f.Exchanges), tc.want, f.Exchanges)
			}
			for k := range f.Exchanges {
				if strings.Contains(k, "127.0.0.1") {
					t.Errorf("address in key %s", k)
				}
			}
		})
	}
}

// An HTTP recording whose body names the deployment is refused and writes
// nothing, the HTTP twin of TestRecordingRefusesIdentifyingValuesUntilRedacted.
// The controller here answers the lab batch with one extra object carrying
// the identifying value, the way a description or location string would.
func TestHTTPRecordingRefusesIdentifyingValues(t *testing.T) {
	lab, err := httpfixture.Read("../../internal/vendors/maxtime/testdata/asc-lab-2.15.1.json")
	if err != nil {
		t.Fatal(err)
	}
	var batch string
	for k, x := range lab.Exchanges {
		if strings.HasPrefix(k, "GET /maxtime/api/mibs/") {
			batch = x.Body
		}
	}
	for what, value := range map[string]string{
		"site":           "Cabinet-042",
		"device address": "127.0.0.1",
	} {
		t.Run(what, func(t *testing.T) {
			body := `[{"name":"intDescription","data":{"1":"` + value + `"}},` + strings.TrimPrefix(strings.TrimSpace(batch), "[")
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if !strings.HasPrefix(r.URL.Path, "/maxtime/api/mibs/") {
					http.NotFound(w, r)
					return
				}
				_, _ = w.Write([]byte(body))
			}))
			defer srv.Close()
			cfg := fmt.Sprintf(`
collector_id: cabinet-042-collector
region: us-ga
agency: metro
agency_unit: d01
site: cabinet-042
model_version: openits/v1
devices:
  - id: asc-1
    vendor: maxtime
    device_kind: asc
    connection:
      http:
        base_url: %s/maxtime
`, srv.URL)
			cfgPath := filepath.Join(t.TempDir(), "collector.yaml")
			if err := os.WriteFile(cfgPath, []byte(cfg), 0o600); err != nil {
				t.Fatal(err)
			}
			out := filepath.Join(t.TempDir(), "f.json")
			msg, err := record(t, cfgPath, out)
			if err == nil || !strings.Contains(err.Error(), "contains the "+what) {
				t.Fatalf("err = %v; want the %s refused\n%s", err, what, msg)
			}
			if _, statErr := os.Stat(out); !errors.Is(statErr, os.ErrNotExist) {
				t.Fatal("a refused recording must write nothing")
			}
		})
	}
}
