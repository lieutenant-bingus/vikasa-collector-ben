package model

import "testing"

func TestFaultSetIsAFacet(t *testing.T) {
	var f Facet = FaultSet{Faults: []Fault{{
		ID: "mmu-fault", Severity: SeverityCritical, Category: CategoryConflict,
		Description: "MMU fault detected",
	}}}
	if f.FacetKind() != KindFaultSet {
		t.Fatalf("FacetKind() = %q, want %q", f.FacetKind(), KindFaultSet)
	}
	if got := f.(FaultSet).Faults[0].ID; got != "mmu-fault" {
		t.Fatalf("ID = %q", got)
	}
}

// Severity order mirrors the catalog's FAULT_SEVERITY_* (INFO=0..CRITICAL=4)
// so the Plan 2 mapping is a straight table. The type stays collector-owned:
// upstream renumbering must not move it.
func TestFaultSeverityString(t *testing.T) {
	cases := map[FaultSeverity]string{
		SeverityInfo: "info", SeverityWarning: "warning", SeverityMinor: "minor",
		SeverityMajor: "major", SeverityCritical: "critical",
		FaultSeverity(99): "unknown",
	}
	for sev, want := range cases {
		if got := sev.String(); got != want {
			t.Errorf("%d.String() = %q, want %q", sev, got, want)
		}
	}
	if SeverityInfo != 0 || SeverityCritical != 4 {
		t.Errorf("severity order must mirror the catalog: info=%d critical=%d", SeverityInfo, SeverityCritical)
	}
}

func TestFaultCategoryString(t *testing.T) {
	cases := map[FaultCategory]string{
		CategoryUnknown: "unknown", CategoryConflict: "conflict",
		CategoryCabinet: "cabinet", CategoryPower: "power",
		CategoryCommunication: "communication", CategoryDetector: "detector",
		CategoryLamp: "lamp", FaultCategory(99): "unknown",
	}
	for cat, want := range cases {
		if got := cat.String(); got != want {
			t.Errorf("%d.String() = %q, want %q", cat, got, want)
		}
	}
}
