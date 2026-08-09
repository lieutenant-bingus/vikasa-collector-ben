package openits

import (
	"encoding/hex"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/encoding/prototext"
	"google.golang.org/protobuf/proto"

	commonv1 "github.com/Vikasa2M/openits-models/pkg/proto/openits/common/v1"
	dmsv1 "github.com/Vikasa2M/openits-models/pkg/proto/openits/dms/v1"
	scv1 "github.com/Vikasa2M/openits-models/pkg/proto/openits/signal_control/v1"
	tsv1 "github.com/Vikasa2M/openits-models/pkg/proto/openits/traffic_sensor/v1"

	"github.com/Vikasa2M/vikasa-collector/sdk/model"
)

// goldenAt is the fixed instant every golden is encoded at. Goldens must not
// depend on wall-clock time.
var goldenAt = time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)

func gbase(deviceID, deviceKind string) model.Base {
	return model.Base{DeviceID: deviceID, DeviceKind: deviceKind, OccurredAt: goldenAt}
}

// goldenCases is one fixture per mapped ce-type. Each is encoded by a FRESH
// emitter, so sequence is always 1 and the bytes are reproducible.
var goldenCases = []struct {
	name       string
	ev         model.Event
	ceType     string
	dataSchema string
	dataHex    string
	identHex   string
}{
	{
		name:       "signal-control mode-changed",
		ev:         model.ModeChanged{Base: gbase("asc-1", "asc"), From: model.ModeFlash, To: model.ModeNormal},
		ceType:     "openits.signal-control.mode-changed.v1",
		dataSchema: "https://schemas.open-its.org/openits-common-mode-events/2026-07-21/",
		dataHex:    "0a056173632d3112276f70656e6974732d7369676e616c2d636f6e74726f6c2d74797065733a6d6f64652d666c6173681a266f70656e6974732d7369676e616c2d636f6e74726f6c2d74797065733a6d6f64652d667265652a0608c0e182d3063210636162696e65742d706f6c6c65722d3140019a062f6f70656e6974732d7369676e616c2d636f6e74726f6c2d74797065733a73632d6d6f64652d6576656e742d6b696e64",
		identHex:   "0a056173632d3112276f70656e6974732d7369676e616c2d636f6e74726f6c2d74797065733a6d6f64652d666c6173681a266f70656e6974732d7369676e616c2d636f6e74726f6c2d74797065733a6d6f64652d667265652a0608c0e182d3069a062f6f70656e6974732d7369676e616c2d636f6e74726f6c2d74797065733a73632d6d6f64652d6576656e742d6b696e64",
	},
	{
		name: "signal-control fault-raised",
		ev: model.FaultRaised{Base: gbase("asc-1", "asc"), FaultID: "mmu-fault",
			Severity: model.SeverityCritical, Category: model.CategoryConflict, Description: "conflict"},
		ceType:     "openits.signal-control.fault-raised.v1",
		dataSchema: "https://schemas.open-its.org/openits-common-fault-events/2026-07-21/",
		dataHex:    "0a056173632d3112096d6d752d6661756c7418042208636f6e666c6963742a0608c0e182d3063210636162696e65742d706f6c6c65722d3140019a062e6f70656e6974732d7369676e616c2d636f6e74726f6c2d74797065733a73632d6661756c742d636f6e666c696374",
		identHex:   "0a056173632d3112096d6d752d6661756c7418042208636f6e666c6963742a0608c0e182d3069a062e6f70656e6974732d7369676e616c2d636f6e74726f6c2d74797065733a73632d6661756c742d636f6e666c696374",
	},
	{
		name:       "signal-control fault-cleared",
		ev:         model.FaultCleared{Base: gbase("asc-1", "asc"), FaultID: "mmu-fault"},
		ceType:     "openits.signal-control.fault-cleared.v1",
		dataSchema: "https://schemas.open-its.org/openits-common-fault-events/2026-07-21/",
		dataHex:    "0a056173632d3112096d6d752d6661756c741a0608c0e182d3062210636162696e65742d706f6c6c65722d3130019a06306f70656e6974732d7369676e616c2d636f6e74726f6c2d74797065733a73632d6661756c742d6576656e742d6b696e64",
		identHex:   "0a056173632d3112096d6d752d6661756c741a0608c0e182d3069a06306f70656e6974732d7369676e616c2d636f6e74726f6c2d74797065733a73632d6661756c742d6576656e742d6b696e64",
	},
	{
		name:       "signal-control plan-applied",
		ev:         model.PlanChanged{Base: gbase("asc-1", "asc"), FromPlanID: 2, ToPlanID: 5},
		ceType:     "openits.signal-control.plan-applied.v1",
		dataSchema: "https://schemas.open-its.org/openits-signal-control-events/2026-07-21/",
		dataHex:    "0a056173632d3110053a0608c0e182d3064210636162696e65742d706f6c6c65722d3150019a06376f70656e6974732d7369676e616c2d636f6e74726f6c2d74797065733a73632d636f6f7264696e6174696f6e2d6576656e742d6b696e64",
		identHex:   "0a056173632d3110053a0608c0e182d3069a06376f70656e6974732d7369676e616c2d636f6e74726f6c2d74797065733a73632d636f6f7264696e6174696f6e2d6576656e742d6b696e64",
	},
	{
		name: "signal-control operational-status-report",
		ev: model.OperationalStatusReport{Base: gbase("asc-1", "asc"), Mode: model.ModeFlash,
			InConflictFlash: true, ActivePlanID: 3},
		ceType:     "openits.signal-control.operational-status-report.v1",
		dataSchema: "https://schemas.open-its.org/openits-signal-control-events/2026-07-21/",
		dataHex:    "0a056173632d3112276f70656e6974732d7369676e616c2d636f6e74726f6c2d74797065733a6d6f64652d666c617368320608c0e182d3063a10636162696e65742d706f6c6c65722d31480150019a062f6f70656e6974732d7369676e616c2d636f6e74726f6c2d74797065733a73632d6d6f64652d6576656e742d6b696e64",
		identHex:   "0a056173632d3112276f70656e6974732d7369676e616c2d636f6e74726f6c2d74797065733a6d6f64652d666c617368320608c0e182d30650019a062f6f70656e6974732d7369676e616c2d636f6e74726f6c2d74797065733a73632d6d6f64652d6576656e742d6b696e64",
	},
	{
		name:       "signal-control preemption-activated",
		ev:         model.PreemptionActivated{Base: gbase("asc-1", "asc"), Source: "rail-1"},
		ceType:     "openits.signal-control.preemption-activated.v1",
		dataSchema: "https://schemas.open-its.org/openits-signal-control-events/2026-07-21/",
		dataHex:    "0a056173632d311a067261696c2d31220608c0e182d3062a10636162696e65742d706f6c6c65722d3138019a06356f70656e6974732d7369676e616c2d636f6e74726f6c2d74797065733a73632d707265656d7074696f6e2d6576656e742d6b696e64",
		identHex:   "0a056173632d311a067261696c2d31220608c0e182d3069a06356f70656e6974732d7369676e616c2d636f6e74726f6c2d74797065733a73632d707265656d7074696f6e2d6576656e742d6b696e64",
	},
	{
		name:       "signal-control preemption-cleared",
		ev:         model.PreemptionCleared{Base: gbase("asc-1", "asc")},
		ceType:     "openits.signal-control.preemption-cleared.v1",
		dataSchema: "https://schemas.open-its.org/openits-signal-control-events/2026-07-21/",
		dataHex:    "0a056173632d31220608c0e182d3062a10636162696e65742d706f6c6c65722d3138019a06356f70656e6974732d7369676e616c2d636f6e74726f6c2d74797065733a73632d707265656d7074696f6e2d6576656e742d6b696e64",
		identHex:   "0a056173632d31220608c0e182d3069a06356f70656e6974732d7369676e616c2d636f6e74726f6c2d74797065733a73632d707265656d7074696f6e2d6576656e742d6b696e64",
	},
	{
		name: "signal-control detector-report",
		ev: model.DetectorReport{Base: gbase("asc-1", "asc"),
			IntervalStart: goldenAt.Add(-30 * time.Second), IntervalDuration: 30500 * time.Millisecond,
			Readings: []model.DetectorReading{{Channel: 1, VolumeDelta: 42, OccupancyTenths: 125}}},
		ceType:     "openits.signal-control.detector-report.v1",
		dataSchema: "https://schemas.open-its.org/openits-signal-control-events/2026-07-21/",
		dataHex:    "0a056173632d31120608a2e182d306181f220a0801182a220431322e352a0608c0e182d3063210636162696e65742d706f6c6c65722d3140019a06336f70656e6974732d7369676e616c2d636f6e74726f6c2d74797065733a73632d6465746563746f722d6576656e742d6b696e64",
		identHex:   "0a056173632d31120608a2e182d306181f220a0801182a220431322e352a0608c0e182d3069a06336f70656e6974732d7369676e616c2d636f6e74726f6c2d74797065733a73632d6465746563746f722d6576656e742d6b696e64",
	},
	{
		name:       "dms mode-changed (control axis)",
		ev:         model.DMSControlModeChanged{Base: gbase("dms-1", "dms"), From: model.ControlCentral, To: model.ControlLocal},
		ceType:     "openits.dms.mode-changed.v1",
		dataSchema: "https://schemas.open-its.org/openits-common-mode-events/2026-07-21/",
		dataHex:    "0a05646d732d3112256f70656e6974732d646d732d74797065733a646d732d636f6e74726f6c2d63656e7472616c1a236f70656e6974732d646d732d74797065733a646d732d636f6e74726f6c2d6c6f63616c2a0608c0e182d3063210636162696e65742d706f6c6c65722d3140019a06256f70656e6974732d646d732d74797065733a646d732d6d6f64652d6576656e742d6b696e64",
		identHex:   "0a05646d732d3112256f70656e6974732d646d732d74797065733a646d732d636f6e74726f6c2d63656e7472616c1a236f70656e6974732d646d732d74797065733a646d732d636f6e74726f6c2d6c6f63616c2a0608c0e182d3069a06256f70656e6974732d646d732d74797065733a646d732d6d6f64652d6576656e742d6b696e64",
	},
	{
		name:       "dms mode-changed (display axis)",
		ev:         model.DMSDisplayStateChanged{Base: gbase("dms-1", "dms"), From: model.DisplayNormal, To: model.DisplayBlank},
		ceType:     "openits.dms.mode-changed.v1",
		dataSchema: "https://schemas.open-its.org/openits-common-mode-events/2026-07-21/",
		dataHex:    "0a05646d732d31121d6f70656e6974732d646d732d74797065733a6d6f64652d6e6f726d616c1a1c6f70656e6974732d646d732d74797065733a6d6f64652d626c616e6b2a0608c0e182d3063210636162696e65742d706f6c6c65722d3140019a06256f70656e6974732d646d732d74797065733a646d732d6d6f64652d6576656e742d6b696e64",
		identHex:   "0a05646d732d31121d6f70656e6974732d646d732d74797065733a6d6f64652d6e6f726d616c1a1c6f70656e6974732d646d732d74797065733a6d6f64652d626c616e6b2a0608c0e182d3069a06256f70656e6974732d646d732d74797065733a646d732d6d6f64652d6576656e742d6b696e64",
	},
	{
		name: "dms fault-raised",
		ev: model.FaultRaised{Base: gbase("dms-1", "dms"), FaultID: "pixel-row-3",
			Severity: model.SeverityMinor, Category: model.CategoryPixel},
		ceType:     "openits.dms.fault-raised.v1",
		dataSchema: "https://schemas.open-its.org/openits-common-fault-events/2026-07-21/",
		dataHex:    "0a05646d732d31120b706978656c2d726f772d3318022a0608c0e182d3063210636162696e65742d706f6c6c65722d3140019a06216f70656e6974732d646d732d74797065733a646d732d6661756c742d706978656c",
		identHex:   "0a05646d732d31120b706978656c2d726f772d3318022a0608c0e182d3069a06216f70656e6974732d646d732d74797065733a646d732d6661756c742d706978656c",
	},
	{
		name:       "dms fault-cleared",
		ev:         model.FaultCleared{Base: gbase("dms-1", "dms"), FaultID: "pixel-row-3"},
		ceType:     "openits.dms.fault-cleared.v1",
		dataSchema: "https://schemas.open-its.org/openits-common-fault-events/2026-07-21/",
		dataHex:    "0a05646d732d31120b706978656c2d726f772d331a0608c0e182d3062210636162696e65742d706f6c6c65722d3130019a06266f70656e6974732d646d732d74797065733a646d732d6661756c742d6576656e742d6b696e64",
		identHex:   "0a05646d732d31120b706978656c2d726f772d331a0608c0e182d3069a06266f70656e6974732d646d732d74797065733a646d732d6661756c742d6576656e742d6b696e64",
	},
	{
		name: "cctv fault-raised",
		ev: model.FaultRaised{Base: gbase("cam-03", "cctv"), FaultID: "video-loss",
			Severity: model.SeverityMajor, Category: model.CategoryCommunication},
		ceType:     "openits.cctv.fault-raised.v1",
		dataSchema: "https://schemas.open-its.org/openits-common-fault-events/2026-07-21/",
		dataHex:    "0a0663616d2d3033120a766964656f2d6c6f737318032a0608c0e182d3063210636162696e65742d706f6c6c65722d3140019a06236f70656e6974732d636374762d74797065733a636374762d6661756c742d636f6d6d73",
		identHex:   "0a0663616d2d3033120a766964656f2d6c6f737318032a0608c0e182d3069a06236f70656e6974732d636374762d74797065733a636374762d6661756c742d636f6d6d73",
	},
	{
		name:       "cctv fault-cleared",
		ev:         model.FaultCleared{Base: gbase("cam-03", "cctv"), FaultID: "video-loss"},
		ceType:     "openits.cctv.fault-cleared.v1",
		dataSchema: "https://schemas.open-its.org/openits-common-fault-events/2026-07-21/",
		dataHex:    "0a0663616d2d3033120a766964656f2d6c6f73731a0608c0e182d3062210636162696e65742d706f6c6c65722d3130019a06286f70656e6974732d636374762d74797065733a636374762d6661756c742d6576656e742d6b696e64",
		identHex:   "0a0663616d2d3033120a766964656f2d6c6f73731a0608c0e182d3069a06286f70656e6974732d636374762d74797065733a636374762d6661756c742d6576656e742d6b696e64",
	},
	{
		name: "traffic-sensor fault-raised",
		ev: model.FaultRaised{Base: gbase("ts-01", "traffic-sensor"), FaultID: "rf-degraded",
			Severity: model.SeverityMinor, Category: model.CategoryPower},
		ceType:     "openits.traffic-sensor.fault-raised.v1",
		dataSchema: "https://schemas.open-its.org/openits-common-fault-events/2026-07-21/",
		dataHex:    "0a0574732d3031120b72662d646567726164656418022a0608c0e182d3063210636162696e65742d706f6c6c65722d3140019a06376f70656e6974732d747261666669632d73656e736f722d74797065733a747261666669632d73656e736f722d6661756c742d706f776572",
		identHex:   "0a0574732d3031120b72662d646567726164656418022a0608c0e182d3069a06376f70656e6974732d747261666669632d73656e736f722d74797065733a747261666669632d73656e736f722d6661756c742d706f776572",
	},
	{
		name:       "traffic-sensor fault-cleared",
		ev:         model.FaultCleared{Base: gbase("ts-01", "traffic-sensor"), FaultID: "rf-degraded"},
		ceType:     "openits.traffic-sensor.fault-cleared.v1",
		dataSchema: "https://schemas.open-its.org/openits-common-fault-events/2026-07-21/",
		dataHex:    "0a0574732d3031120b72662d64656772616465641a0608c0e182d3062210636162696e65742d706f6c6c65722d3130019a063c6f70656e6974732d747261666669632d73656e736f722d74797065733a747261666669632d73656e736f722d6661756c742d6576656e742d6b696e64",
		identHex:   "0a0574732d3031120b72662d64656772616465641a0608c0e182d3069a063c6f70656e6974732d747261666669632d73656e736f722d74797065733a747261666669632d73656e736f722d6661756c742d6576656e742d6b696e64",
	},
	{
		name: "perception fault-raised",
		// Blockage is the archetypal lidar fault and the domain has no category
		// for it, so this golden pins the FALLBACK path rather than dressing up
		// a mapping that does not exist. Mapping it to CategoryEnvironment would
		// have produced perception-fault-temperature — a confident, wrong claim
		// about a sensor that is dirty, not hot.
		ev: model.FaultRaised{Base: gbase("lidar-01", "perception"), FaultID: "blockage",
			Severity: model.SeverityMajor, Category: model.CategoryUnknown},
		ceType:     "openits.perception.fault-raised.v1",
		dataSchema: "https://schemas.open-its.org/openits-common-fault-events/2026-07-21/",
		dataHex:    "0a086c696461722d30311208626c6f636b61676518032a0608c0e182d3063210636162696e65742d706f6c6c65722d3140019a06346f70656e6974732d70657263657074696f6e2d74797065733a70657263657074696f6e2d6661756c742d6576656e742d6b696e64",
		identHex:   "0a086c696461722d30311208626c6f636b61676518032a0608c0e182d3069a06346f70656e6974732d70657263657074696f6e2d74797065733a70657263657074696f6e2d6661756c742d6576656e742d6b696e64",
	},
	{
		name:       "perception fault-cleared",
		ev:         model.FaultCleared{Base: gbase("lidar-01", "perception"), FaultID: "blockage"},
		ceType:     "openits.perception.fault-cleared.v1",
		dataSchema: "https://schemas.open-its.org/openits-common-fault-events/2026-07-21/",
		dataHex:    "0a086c696461722d30311208626c6f636b6167651a0608c0e182d3062210636162696e65742d706f6c6c65722d3130019a06346f70656e6974732d70657263657074696f6e2d74797065733a70657263657074696f6e2d6661756c742d6576656e742d6b696e64",
		identHex:   "0a086c696461722d30311208626c6f636b6167651a0608c0e182d3069a06346f70656e6974732d70657263657074696f6e2d74797065733a70657263657074696f6e2d6661756c742d6576656e742d6b696e64",
	},
	{
		name: "traffic-sensor interval report",
		ev: model.TrafficIntervalReport{
			Base:             gbase("ts-01", "traffic-sensor"),
			IntervalStart:    goldenAt.Add(-time.Minute),
			IntervalDuration: 60 * time.Second,
			Lanes: []model.LaneMeasurement{{
				LaneID: 1, Volume: 42, OccupancyTenths: 125,
				SpeedAvgHundredthsKPH: 8850, SpeedReported: true,
				ClassVolumes: []model.LaneClassVolume{{ClassID: 2, Volume: 30}},
				Quality:      model.QualityValid,
			}, {
				// Second lane exercises the unreported-speed path, which must
				// stay absent rather than render as stopped traffic.
				LaneID: 2, Volume: 7, OccupancyTenths: 20, Quality: model.QualityUnknown,
			}},
		},
		ceType:     "openits.traffic-sensor.traffic-interval-report.v1",
		dataSchema: "https://schemas.open-its.org/openits-traffic-sensor-events/2026-07-21/",
		dataHex:    "0a21080122040802101e483c52060884e182d3065a0431322e356a0538382e3530702a0a150802483c52060884e182d3065a03322e30700778011210636162696e65742d706f6c6c65722d311a0608c0e182d3062801320574732d30319a06376f70656e6974732d747261666669632d73656e736f722d74797065733a74732d747261666669632d696e74657276616c2d7265706f7274",
		identHex:   "0a21080122040802101e483c52060884e182d3065a0431322e356a0538382e3530702a0a150802483c52060884e182d3065a03322e30700778011a0608c0e182d306320574732d30319a06376f70656e6974732d747261666669632d73656e736f722d74797065733a74732d747261666669632d696e74657276616c2d7265706f7274",
	},
	{
		name: "dms message-activation-failed",
		ev: model.DMSMessageActivationFailed{Base: gbase("dms-1", "dms"),
			MemoryType: model.MemoryChangeable, Slot: 7,
			Error: model.SyntaxErrorFontNotFound, ErrorPosition: 12},
		ceType:     "openits.dms.message-activation-failed.v1",
		dataSchema: "https://schemas.open-its.org/openits-dms-events/2026-07-23/",
		dataHex:    "080210072210636162696e65742d706f6c6c65722d312a0608c0e182d30638014205646d732d314802500c9a062f6f70656e6974732d646d732d74797065733a646d732d6d6573736167652d61637469766174696f6e2d6661696c6564",
		identHex:   "080210072a0608c0e182d3064205646d732d314802500c9a062f6f70656e6974732d646d732d74797065733a646d732d6d6573736167652d61637469766174696f6e2d6661696c6564",
	},
}

// TestGoldens pins the exact bytes every mapped event encodes to.
//
// Byte-exact rather than field-by-field on purpose: these bytes ARE the wire
// contract, and a field-level assertion would pass through a proto field
// renumbering that silently changes what consumers receive. A diff here means
// either the mapping changed or the models module moved under us, and both
// deserve a human decision (ADR 0008; the wire-emitter skill's pin-bump rule).
//
// The hex was generated by TestPrintGoldens, but every case was verified by
// decoding it and reading the result against its fixture before being pasted.
// Regenerating without that step turns a golden into a record of whatever the
// code happened to do, which is worse than no golden at all — it looks like
// coverage.
//
// Each case uses a FRESH emitter, so sequence is always 1 and the bytes are
// reproducible.
func TestGoldens(t *testing.T) {
	for _, c := range goldenCases {
		t.Run(c.name, func(t *testing.T) {
			enc, ok, err := New("cabinet-poller-1").Encode(c.ev)
			if err != nil || !ok {
				t.Fatalf("Encode: ok=%v err=%v", ok, err)
			}
			if enc.CEType != c.ceType {
				t.Errorf("ce-type = %q, want %q", enc.CEType, c.ceType)
			}
			if enc.ContentType != "application/protobuf" {
				t.Errorf("content-type = %q", enc.ContentType)
			}
			if enc.DataSchema != c.dataSchema {
				t.Errorf("ce-dataschema = %q, want %q", enc.DataSchema, c.dataSchema)
			}
			if got := hex.EncodeToString(enc.Data); got != c.dataHex {
				t.Errorf("payload bytes changed:\n got %s\nwant %s", got, c.dataHex)
			}
			// The identity projection is pinned separately. It feeds ce-id, so
			// a change here silently changes the id of every event of this
			// type — invisible in the payload goldens, which would still pass.
			if got := hex.EncodeToString(enc.Identity); got != c.identHex {
				t.Errorf("identity bytes changed:\n got %s\nwant %s", got, c.identHex)
			}
			// And the bytes must still be a valid message of the expected type.
			if _, err := unmarshalFor(c.ceType, enc.Data); err != nil {
				t.Errorf("payload does not decode: %v", err)
			}
		})
	}
}

func TestGoldensCoverEveryCEType(t *testing.T) {
	// A mapped ce-type with no golden is an unpinned wire contract. Catching
	// that here is what stops the golden set silently lagging the mapping.
	covered := make(map[string]bool)
	for _, c := range goldenCases {
		covered[c.ceType] = true
	}
	for _, ceType := range New("x").CETypes() {
		if !covered[ceType] {
			t.Errorf("ce-type %q has no golden fixture", ceType)
		}
	}
}

// TestPrintGoldens regenerates the hex above. Run it with
// -run TestPrintGoldens -v, and READ the decoded form it prints against each
// fixture before pasting. It is a tool, not a test.
func TestPrintGoldens(t *testing.T) {
	t.Skip("generator; run explicitly with -run TestPrintGoldens")
	for _, c := range goldenCases {
		enc, ok, err := New("cabinet-poller-1").Encode(c.ev)
		if err != nil || !ok {
			t.Fatalf("%s: ok=%v err=%v", c.name, ok, err)
		}
		msg, err := unmarshalFor(c.ceType, enc.Data)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		t.Logf("\n=== %s\n  ceType:     %s\n  dataSchema: %s\n  dataHex:    %s\n  identHex:   %s\n  decoded: %s",
			c.name, enc.CEType, enc.DataSchema,
			hex.EncodeToString(enc.Data), hex.EncodeToString(enc.Identity),
			prototext.Format(msg))
	}
}

func unmarshalFor(ceType string, b []byte) (proto.Message, error) {
	m := emptyMessageFor(ceType)
	if err := proto.Unmarshal(b, m); err != nil {
		return nil, err
	}
	return m, nil
}

func emptyMessageFor(ceType string) proto.Message {
	// The shared families match by suffix rather than by exhaustive ce-type.
	// fault-raised/cleared and mode-changed are declared once upstream and
	// reused across every service, so an exhaustive list here would need a new
	// case per service for a message shape that never changes — and forgetting
	// one panics the generator rather than failing a mapping.
	switch {
	case strings.HasSuffix(ceType, ".mode-changed.v1"):
		return &commonv1.ModeChanged{}
	case strings.HasSuffix(ceType, ".fault-raised.v1"):
		return &commonv1.FaultRaised{}
	case strings.HasSuffix(ceType, ".fault-cleared.v1"):
		return &commonv1.FaultCleared{}
	}
	switch ceType {
	case "openits.traffic-sensor.traffic-interval-report.v1":
		return &tsv1.TrafficIntervalReport{}
	case "openits.signal-control.plan-applied.v1":
		return &scv1.PlanApplied{}
	case "openits.signal-control.operational-status-report.v1":
		return &scv1.OperationalStatusReport{}
	case "openits.signal-control.preemption-activated.v1":
		return &scv1.PreemptionActivated{}
	case "openits.signal-control.preemption-cleared.v1":
		return &scv1.PreemptionCleared{}
	case "openits.signal-control.detector-report.v1":
		return &scv1.DetectorReport{}
	case "openits.dms.message-activation-failed.v1":
		return &dmsv1.MessageActivationFailed{}
	default:
		panic("no message type for ce-type " + ceType)
	}
}
