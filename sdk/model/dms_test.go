package model

import "testing"

func TestDMSStatusIsAFacet(t *testing.T) {
	var f Facet = DMSStatus{
		ControlMode: ControlCentral, DisplayState: DisplayNormal,
		ActiveMemoryType: MemoryChangeable, ActiveSlot: 4,
		MessageStatus: StatusValid,
	}
	if f.FacetKind() != KindDMSStatus {
		t.Fatalf("FacetKind() = %q, want %q", f.FacetKind(), KindDMSStatus)
	}
	if got := f.(DMSStatus).ActiveSlot; got != 4 {
		t.Fatalf("ActiveSlot = %d", got)
	}
}

// Every enum's zero value must mean "we do not know", never a real state: a
// sign that fails to answer an object must not read as a definite value.
func TestDMSEnumZeroValuesMeanUnknown(t *testing.T) {
	if ControlUnknown != 0 || DisplayUnknown != 0 || MemoryUnknown != 0 || StatusUnknown != 0 {
		t.Error("control/display/memory/status zero values must be the Unknown variant")
	}
	// MultiSyntaxError is the sharp one. The catalog's ErrorType puts SYNTAX at
	// 0 with NO unspecified value — mirroring that numbering would make an
	// unanswered object report a genuine syntax error. Ours must be None.
	if SyntaxErrorNone != 0 {
		t.Error("MultiSyntaxError zero must be None, or an unanswered object fabricates a syntax error")
	}
	if SyntaxErrorSyntax == 0 {
		t.Error("SyntaxErrorSyntax must NOT be zero — that is the catalog's numbering, not ours")
	}
	var zero DMSStatus
	if zero.ControlMode.String() != "unknown" || zero.DisplayState.String() != "unknown" {
		t.Error("a zero DMSStatus must describe itself as unknown")
	}
	if zero.SyntaxError.String() != "none" {
		t.Errorf("zero SyntaxError.String() = %q, want none", zero.SyntaxError.String())
	}
}

func TestDMSEnumStrings(t *testing.T) {
	for got, want := range map[string]string{
		ControlLocal.String(): "local", ControlCentral.String(): "central",
		ControlCentralOverride.String(): "central-override", ControlSimulation.String(): "simulation",
		ControlExternal.String(): "external", ControlOther.String(): "other",
		DisplayOff.String(): "off", DisplayBlank.String(): "blank",
		DisplayTest.String(): "test", DisplayNormal.String(): "normal",
		MemoryPermanent.String(): "permanent", MemoryChangeable.String(): "changeable",
		MemoryVolatile.String(): "volatile", MemorySchedule.String(): "schedule", MemoryBlank.String(): "blank",
		StatusNotUsed.String(): "not-used", StatusModifying.String(): "modifying",
		StatusValidating.String(): "validating", StatusValid.String(): "valid", StatusError.String(): "error",
		SyntaxErrorSyntax.String(): "syntax", SyntaxErrorUnsupportedTag.String(): "unsupported-tag",
		SyntaxErrorFontNotFound.String(): "font-not-found", SyntaxErrorGraphicNotFound.String(): "graphic-not-found",
		SyntaxErrorTooLong.String(): "too-long", SyntaxErrorHardware.String(): "hardware",
		SyntaxErrorOther.String(): "other",
	} {
		if got != want {
			t.Errorf("String() = %q, want %q", got, want)
		}
	}
	if DMSControlMode(99).String() != "unknown" || MultiSyntaxError(99).String() != "none" {
		t.Error("out-of-range values must fall back to their zero meaning")
	}
}

func TestDMSFaultCategories(t *testing.T) {
	for cat, want := range map[FaultCategory]string{
		CategoryPixel: "pixel", CategoryController: "controller", CategoryEnvironment: "environment",
	} {
		if got := cat.String(); got != want {
			t.Errorf("%d.String() = %q, want %q", cat, got, want)
		}
	}
}
