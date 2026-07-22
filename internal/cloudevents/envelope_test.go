package cloudevents

import (
	"testing"
	"time"
)

var tenant = Tenant{Agency: "metro-atlanta", Site: "cabinet-042"}

func TestTenantValidate(t *testing.T) {
	for _, bad := range []Tenant{
		{Agency: "Metro", Site: "cab"}, {Agency: "metro", Site: "cab.1"},
		{Agency: "", Site: "cab"}, {Agency: "metro", Site: "cab*"},
	} {
		if bad.Validate() == nil {
			t.Errorf("Validate(%+v) should fail", bad)
		}
	}
	if tenant.Validate() != nil {
		t.Errorf("Validate(%+v) should pass", tenant)
	}
}

func TestContentAddressedID(t *testing.T) {
	at := time.Date(2026, 7, 12, 10, 0, 0, 0, time.UTC)
	a := New("openits-collector.health.collector-started.v1", "//metro/cab", at, "application/json", []byte(`{"v":1}`))
	b := New("openits-collector.health.collector-started.v1", "//metro/cab", at, "application/json", []byte(`{"v":1}`))
	c := New("openits-collector.health.collector-started.v1", "//metro/cab", at, "application/json", []byte(`{"v":2}`))
	if a.ID != b.ID {
		t.Error("identical inputs must produce identical IDs (JetStream dedup)")
	}
	if a.ID == c.ID {
		t.Error("different payloads must produce different IDs")
	}
	if len(a.ID) != 64 {
		t.Errorf("ID should be 64 hex chars, got %d", len(a.ID))
	}
	if a.SpecVersion != "1.0" {
		t.Errorf("specversion = %q", a.SpecVersion)
	}
}

func TestSourceFor(t *testing.T) {
	if got := SourceFor(tenant, "asc-1"); got != "//metro-atlanta/cabinet-042/asc-1" {
		t.Errorf("SourceFor = %q", got)
	}
	if got := SourceFor(tenant, ""); got != "//metro-atlanta/cabinet-042" {
		t.Errorf("SourceFor(collector) = %q", got)
	}
}
