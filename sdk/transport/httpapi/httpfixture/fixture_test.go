package httpfixture_test

import (
	"context"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Vikasa2M/vikasa-collector/sdk/transport/httpapi"
	"github.com/Vikasa2M/vikasa-collector/sdk/transport/httpapi/httpapitest"
	"github.com/Vikasa2M/vikasa-collector/sdk/transport/httpapi/httpfixture"
)

var prov = httpfixture.Provenance{
	Adapter: "maxtime-asc", Make: "Q-Free", Model: "MaxTime", Firmware: "2.x",
	Scheme: "http", Recorded: "2026-11-04", RecordedBy: "test",
}

func TestRecorderNeverWritesNonGetOrCredentials(t *testing.T) {
	inner := &httpapitest.Static{}
	inner.Set("POST /login", 200, `{"token":"SECRET"}`)
	inner.Set("GET /mibs?mibs=A", 200, `{"data":{"A":{"0":1}}}`)
	rec := httpfixture.NewRecorder(inner)
	ctx := context.Background()
	_, _ = rec.Do(ctx, httpapi.Request{Method: http.MethodPost, Path: "/login", Body: []byte("pw=hunter2")})
	_, _ = rec.Do(ctx, httpapi.Request{Path: "/mibs", Query: map[string][]string{"mibs": {"A"}}, Header: http.Header{"Authorization": {"Bearer SECRET"}}})

	f := rec.Fixture(prov)
	path := filepath.Join(t.TempDir(), "f.json")
	if err := httpfixture.Write(path, f); err != nil {
		t.Fatal(err)
	}
	back, err := httpfixture.Read(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(back.Exchanges) != 1 || back.Unrecorded[0] != "POST /login" {
		t.Fatalf("fixture = %+v", back)
	}
	for k, x := range back.Exchanges {
		if strings.Contains(k+x.Body, "SECRET") || strings.Contains(k+x.Body, "hunter2") {
			t.Fatalf("credential reached the fixture: %s %s", k, x.Body)
		}
	}

	replay := httpapitest.LoadFixture(t, path)
	resp, _ := replay.Do(ctx, httpapi.Request{Path: "/mibs", Query: map[string][]string{"mibs": {"A"}}})
	if string(resp.Body) != `{"data":{"A":{"0":1}}}` {
		t.Errorf("replay body = %q", resp.Body)
	}
	if resp, _ := replay.Do(ctx, httpapi.Request{Path: "/other"}); resp.Status != http.StatusNotFound {
		t.Errorf("unrecorded path status = %d, want 404", resp.Status)
	}
}

func TestValidate(t *testing.T) {
	bad := httpfixture.Fixture{Exchanges: map[string]httpfixture.Exchange{"POST /x": {Status: 200}}}
	err := bad.Validate()
	if err == nil || !strings.Contains(err.Error(), "only GET") || !strings.Contains(err.Error(), "provenance.adapter") {
		t.Fatalf("Validate = %v", err)
	}
}

func TestWrapRecordsIntoTheSameFixture(t *testing.T) {
	a, b := &httpapitest.Static{}, &httpapitest.Static{}
	a.Set("GET /mibs", 200, "poll")
	b.Set("GET /log", 200, "log")
	rec := httpfixture.NewRecorder(a)
	second := rec.Wrap(b)
	ctx := context.Background()
	_, _ = rec.Do(ctx, httpapi.Request{Path: "/mibs"})
	_, _ = second.Do(ctx, httpapi.Request{Path: "/log"})
	f := rec.Fixture(prov)
	if f.Exchanges["GET /mibs"].Body != "poll" || f.Exchanges["GET /log"].Body != "log" {
		t.Fatalf("exchanges = %+v", f.Exchanges)
	}
}
