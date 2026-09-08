package maxtime

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/Vikasa2M/vikasa-collector/sdk/adapter"
	"github.com/Vikasa2M/vikasa-collector/sdk/model"
)

func TestASCLogDecodeRecordedSample(t *testing.T) {
	raw, err := os.ReadFile("testdata/live/asclog-sample.xml")
	if err != nil {
		t.Fatal(err)
	}
	records, err := parseASCLogXML(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(records) < 20 {
		t.Fatalf("sample has %d events, want a recorded subset", len(records))
	}

	var (
		phases, overlaps, unmapped int
	)
	for _, rec := range records {
		ev := decodeIndiana(rec.ID, rec.TypeID, rec.Parameter, model.Base{DeviceID: "asc-1", OccurredAt: rec.Time})
		switch ev.(type) {
		case model.PhaseLogEvent:
			phases++
		case model.OverlapLogEvent:
			overlaps++
		case model.UnmappedControllerLogEvent:
			unmapped++
		case model.DetectorTransition:
			// none expected in this sample window
		default:
			t.Fatalf("unexpected event type %T", ev)
		}
	}
	if phases == 0 || overlaps == 0 || unmapped == 0 {
		t.Fatalf("decode coverage: phases=%d overlaps=%d unmapped=%d", phases, overlaps, unmapped)
	}
}

func TestASCLogFetchCheckpointsAndEmitsOnlyNew(t *testing.T) {
	raw, err := os.ReadFile("testdata/live/asclog-sample.xml")
	if err != nil {
		t.Fatal(err)
	}
	var serveNewer bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/asclog/xml/full" {
			http.NotFound(w, r)
			return
		}
		if !serveNewer {
			_, _ = w.Write(raw)
			return
		}
		// Append one synthetic event with a higher ID than the sample max.
		_, _ = w.Write([]byte(`<EventResponses><EventResponse>` +
			`<Event ID="9999999" TimeStamp="09-08-2026 12:00:00.0" EventTypeID="1" Parameter="2"/>` +
			`</EventResponse></EventResponses>`))
	}))
	defer server.Close()

	log, err := newASCLog("asc-1", server.URL+"/v1/asclog/xml/full", time.Second)
	if err != nil {
		t.Fatal(err)
	}

	first, err := log.Fetch(context.Background())
	if err != nil {
		t.Fatalf("prime Fetch: %v", err)
	}
	if len(first) != 0 {
		t.Fatalf("first fetch must prime only, got %d events", len(first))
	}

	second, err := log.Fetch(context.Background())
	if err != nil {
		t.Fatalf("unchanged Fetch: %v", err)
	}
	if len(second) != 0 {
		t.Fatalf("unchanged window must emit nothing, got %d", len(second))
	}

	serveNewer = true
	third, err := log.Fetch(context.Background())
	if err != nil {
		t.Fatalf("newer Fetch: %v", err)
	}
	if len(third) != 1 {
		t.Fatalf("newer window events = %d, want 1", len(third))
	}
	got, ok := third[0].(model.PhaseLogEvent)
	if !ok {
		t.Fatalf("event type = %T, want PhaseLogEvent", third[0])
	}
	if got.LogID != 9999999 || got.Code != model.PhaseLogBeginGreen || got.PhaseNumber != 2 {
		t.Fatalf("event = %+v", got)
	}
}

func TestASCLogDescriptorClaimsEvents(t *testing.T) {
	log, err := newASCLog("asc-1", "http://controller/v1/asclog/xml/full", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	got := log.Descriptor()
	want := adapter.Descriptor{Vendor: "maxtime", DeviceKind: "asc", Caps: adapter.CapEvents}
	if got != want {
		t.Fatalf("Descriptor = %+v, want %+v", got, want)
	}
}

func TestRegisterToEnablesASCLogByDefault(t *testing.T) {
	registry := adapter.NewRegistry()
	RegisterTo(registry)
	a, err := registry.Build("maxtime", "asc", "asc-1", map[string]any{
		"http": map[string]any{"base_url": "http://controller/maxtime"},
	})
	if err != nil {
		t.Fatal(err)
	}
	caps := a.Descriptor().Caps
	if !caps.Has(adapter.CapState) || !caps.Has(adapter.CapEvents) {
		t.Fatalf("Caps = %v, want CapState|CapEvents", caps)
	}
	if _, ok := a.(adapter.EventReader); !ok {
		t.Fatal("registered adapter must implement EventReader when asclog enabled")
	}
}

func TestRegisterToCanDisableASCLog(t *testing.T) {
	registry := adapter.NewRegistry()
	RegisterTo(registry)
	a, err := registry.Build("maxtime", "asc", "asc-1", map[string]any{
		"http": map[string]any{"base_url": "http://controller/maxtime", "asclog": false},
	})
	if err != nil {
		t.Fatal(err)
	}
	if a.Descriptor().Caps.Has(adapter.CapEvents) {
		t.Fatal("asclog:false must not claim CapEvents")
	}
	if _, ok := a.(adapter.EventReader); ok {
		t.Fatal("asclog:false must not implement EventReader")
	}
	if _, ok := a.(adapter.StateReader); !ok {
		t.Fatal("asclog:false must still implement StateReader")
	}
}
