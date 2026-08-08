package cloudevents

import "testing"

func TestSourceFor_BuildsTheProfileURN(t *testing.T) {
	tenant := Tenant{Region: "us-tx", Agency: "txdot", AgencyUnit: "d07", Site: "cabinet-042"}

	for name, tc := range map[string]struct {
		deviceKind, deviceID, want string
	}{
		// entity-kind is NOT the collector's DeviceKind. Upstream names a
		// signal controller "controller" and a sign "sign".
		"asc becomes controller": {"asc", "i35-exit-214",
			"urn:openits:controller:us-tx:txdot:d07:i35-exit-214"},
		"dms becomes sign": {"dms", "i35-mm214-nb",
			"urn:openits:sign:us-tx:txdot:d07:i35-mm214-nb"},
		// Collector-level events (collector-started) have no device. The
		// collector itself is the entity.
		"no device becomes collector": {"", "",
			"urn:openits:collector:us-tx:txdot:d07:cabinet-042"},
	} {
		t.Run(name, func(t *testing.T) {
			if got := SourceFor(tenant, tc.deviceKind, tc.deviceID); got != tc.want {
				t.Errorf("SourceFor = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestSourceFor_UnknownDeviceKindIsNotGuessed(t *testing.T) {
	// A device kind with no entity-kind mapping must not fall back to the raw
	// kind: that would emit a URN asserting an entity type upstream does not
	// define, and every consumer parsing ce-source would inherit the invention.
	tenant := Tenant{Region: "us-tx", Agency: "txdot", AgencyUnit: "d07", Site: "cab-1"}
	if got := SourceFor(tenant, "rsu", "rsu-1"); got != "" {
		t.Errorf("SourceFor with an unmapped device kind = %q, want empty", got)
	}
}

func TestTenantValidate_RejectsTokensThatCorruptTheURN(t *testing.T) {
	ok := Tenant{Region: "us-tx", Agency: "txdot", AgencyUnit: "d07", Site: "cab-1"}
	if err := ok.Validate(); err != nil {
		t.Fatalf("valid tenant rejected: %v", err)
	}
	for name, bad := range map[string]Tenant{
		"empty region":    {Region: "", Agency: "txdot", AgencyUnit: "d07", Site: "cab-1"},
		"empty unit":      {Region: "us-tx", Agency: "txdot", AgencyUnit: "", Site: "cab-1"},
		"colon in agency": {Region: "us-tx", Agency: "tx:dot", AgencyUnit: "d07", Site: "cab-1"},
		"dot in region":   {Region: "us.tx", Agency: "txdot", AgencyUnit: "d07", Site: "cab-1"},
		"upper in unit":   {Region: "us-tx", Agency: "txdot", AgencyUnit: "D07", Site: "cab-1"},
	} {
		if err := bad.Validate(); err == nil {
			t.Errorf("%s: accepted %+v", name, bad)
		}
	}
}
