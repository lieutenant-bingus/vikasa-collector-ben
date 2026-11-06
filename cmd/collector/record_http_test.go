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
