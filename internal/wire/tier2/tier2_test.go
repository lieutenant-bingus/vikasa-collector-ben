package tier2_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	"google.golang.org/protobuf/proto"

	cctvv1 "github.com/Vikasa2M/openits-models/pkg/proto/openits/cctv/v1"
	commonv1 "github.com/Vikasa2M/openits-models/pkg/proto/openits/common/v1"
	dmsv1 "github.com/Vikasa2M/openits-models/pkg/proto/openits/dms/v1"
	pcpv1 "github.com/Vikasa2M/openits-models/pkg/proto/openits/perception/v1"
	scv1 "github.com/Vikasa2M/openits-models/pkg/proto/openits/signal_control/v1"
	tsv1 "github.com/Vikasa2M/openits-models/pkg/proto/openits/traffic_sensor/v1"
	zocv1 "github.com/Vikasa2M/openits-models/pkg/proto/openits/zone_occupancy/v1"

	conformance "github.com/Vikasa2M/openits-models/tools/conformance/tests"

	"github.com/Vikasa2M/vikasa-collector/internal/app"
	"github.com/Vikasa2M/vikasa-collector/internal/config"
	"github.com/Vikasa2M/vikasa-collector/sdk/adapter"
	"github.com/Vikasa2M/vikasa-collector/sdk/model"
)

// scripted plays one snapshot per poll and loops, so every transition
// recurs: polls taken while the engine is held for its stored state
// (ADR 0020) are never diffed, and a single pass could be spent before
// diffing starts.
type scripted struct {
	desc  adapter.Descriptor
	id    string
	steps [][]model.Facet

	mu sync.Mutex
	n  int
}

func (s *scripted) Descriptor() adapter.Descriptor { return s.desc }
func (s *scripted) Close() error                   { return nil }
func (s *scripted) Read(context.Context) (*model.Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	step := s.steps[s.n%len(s.steps)]
	s.n++
	return &model.Snapshot{DeviceID: s.id, SampledAt: time.Now().UTC(), Facets: step}, nil
}

func ascScript() [][]model.Facet {
	return [][]model.Facet{
		{
			model.SignalStatus{Mode: model.ModeCoordinated, CabinetOutput: model.OutputNormal, ActivePlanID: 1},
			model.FaultSet{},
			model.DetectorSamples{Samples: []model.DetectorSample{{Channel: 1, VolumeCount: 10, OccupancyTenths: 50}}},
		},
		{
			model.SignalStatus{Mode: model.ModeFree, CabinetOutput: model.OutputFlash, FlashCause: model.FlashMonitor,
				ActivePlanID: 2, PreemptionActive: true, PreemptionSource: "rail-1"},
			model.FaultSet{Faults: []model.Fault{{ID: "mmu-fault", Severity: model.SeverityCritical, Category: model.CategoryConflict, Description: "conflict"}}},
			model.DetectorSamples{Samples: []model.DetectorSample{{Channel: 1, VolumeCount: 15, OccupancyTenths: 120}}},
		},
		{
			model.SignalStatus{Mode: model.ModeCoordinated, CabinetOutput: model.OutputNormal, ActivePlanID: 2},
			model.FaultSet{},
			model.DetectorSamples{Samples: []model.DetectorSample{{Channel: 1, VolumeCount: 21, OccupancyTenths: 80}}},
		},
	}
}

func dmsScript() [][]model.Facet {
	return [][]model.Facet{
		{
			model.DMSStatus{
				ControlMode: model.ControlCentral, DisplayState: model.DisplayNormal,
				ActiveMemoryType: model.MemoryChangeable, ActiveSlot: 1, MessageStatus: model.StatusValid,
				MessageText: "ROAD WORK", MessageCRC: 0x1a2b, ActivationTrigger: model.TriggerCommand,
			},
			model.FaultSet{},
		},
		{
			model.DMSStatus{
				ControlMode: model.ControlLocal, DisplayState: model.DisplayBlank,
				ActiveMemoryType: model.MemoryChangeable, ActiveSlot: 2, MessageStatus: model.StatusError,
				SyntaxError: model.SyntaxErrorFontNotFound, SyntaxErrorPos: 4,
				MessageText: "[fo9]DETOUR", MessageCRC: 0x3c4d, ActivationTrigger: model.TriggerCommand,
			},
			model.FaultSet{Faults: []model.Fault{{ID: "pixel-fault", Severity: model.SeverityMinor, Category: model.CategoryPixel, Description: "pixels"}}},
		},
		{
			model.DMSStatus{
				ControlMode: model.ControlLocal, DisplayState: model.DisplayBlank,
				ActiveMemoryType: model.MemoryChangeable, ActiveSlot: 2, MessageStatus: model.StatusError,
				SyntaxError: model.SyntaxErrorFontNotFound, SyntaxErrorPos: 4,
				MessageText: "[fo9]DETOUR", MessageCRC: 0x3c4d, ActivationTrigger: model.TriggerCommand,
			},
			model.FaultSet{},
		},
	}
}

// intervalAt gives each scripted step its own measurement interval, so every
// step is a new interval report.
func intervalAt(step int) time.Time {
	return time.Date(2026, 10, 22, 12, step, 0, 0, time.UTC)
}

func cctvScript() [][]model.Facet {
	return [][]model.Facet{
		{
			model.CCTVStatus{ControlMode: model.CCTVControlCentral, Tours: []model.CCTVTour{{TourID: 4, State: model.TourRunning}}},
			model.FaultSet{},
		},
		{
			model.CCTVStatus{ControlMode: model.CCTVControlLocal, Tours: []model.CCTVTour{{TourID: 4, State: model.TourPaused}}},
			model.FaultSet{Faults: []model.Fault{{ID: "link-down", Severity: model.SeverityMajor, Category: model.CategoryCommunication, Description: "uplink lost"}}},
		},
		{
			model.CCTVStatus{ControlMode: model.CCTVControlCentral, Tours: []model.CCTVTour{{TourID: 4, State: model.TourStopped}}},
			model.FaultSet{},
		},
	}
}

func trafficScript() [][]model.Facet {
	lane := func(vol uint32) model.LaneMeasurement {
		return model.LaneMeasurement{
			LaneID: 1, Volume: vol, OccupancyTenths: 125, SpeedAvgHundredthsKPH: 8850, SpeedReported: true,
			ClassVolumes: []model.LaneClassVolume{{ClassID: 2, Volume: vol}}, Quality: model.QualityValid,
		}
	}
	report := func(step int, vol uint32) model.TrafficIntervals {
		return model.TrafficIntervals{IntervalStart: intervalAt(step), IntervalDuration: time.Minute, Lanes: []model.LaneMeasurement{lane(vol)}}
	}
	return [][]model.Facet{
		{report(0, 40), model.FaultSet{}},
		{report(1, 42), model.FaultSet{Faults: []model.Fault{{ID: "psu", Severity: model.SeverityMajor, Category: model.CategoryPower, Description: "supply low"}}}},
		{report(2, 38), model.FaultSet{}},
	}
}

func perceptionScript() [][]model.Facet {
	incident := func(confidence uint8) model.ZoneIncident {
		return model.ZoneIncident{
			IncidentID: "i-1", ZoneID: "zone-a", Type: model.IncidentWrongWayVehicle, Severity: model.IncidentMajor,
			ObjectClass: model.ObjectTruck, ConfidencePercent: confidence, SpeedHundredthsKPH: 4250, SpeedReported: true,
			TrackID: 7, TrackEpoch: 2,
		}
	}
	zones := func(step int) model.ZoneIntervals {
		return model.ZoneIntervals{IntervalStart: intervalAt(step), IntervalDuration: time.Minute, Zones: []model.ZoneMeasurement{{
			ZoneID: "zone-a", CrossedVolume: 40, ObservedCount: 42, OccupancyTenths: 310,
			SpeedAvgHundredthsKPH: 5125, SpeedReported: true,
			CrossedClassCounts:  []model.ZoneClassCount{{Class: model.ObjectPassengerVehicle, Count: 36}, {Class: model.ObjectTruck, Count: 4}},
			ObservedClassCounts: []model.ZoneClassCount{{Class: model.ObjectPassengerVehicle, Count: 38}, {Class: model.ObjectTruck, Count: 4}},
		}}}
	}
	return [][]model.Facet{
		{model.ZoneIncidents{}, zones(0), model.FaultSet{}},
		{model.ZoneIncidents{Incidents: []model.ZoneIncident{incident(80)}}, zones(1),
			model.FaultSet{Faults: []model.Fault{{ID: "overheat", Severity: model.SeverityMajor, Category: model.CategoryEnvironment, Description: "enclosure hot"}}}},
		{model.ZoneIncidents{Incidents: []model.ZoneIncident{incident(95)}}, zones(2), model.FaultSet{}},
	}
}

// want is the catalog event set the scripts must produce before the checks
// run, so no check can pass for want of the event it judges.
var want = []string{
	"openits.signal-control.mode-changed.v1",
	"openits.signal-control.cabinet-output-changed.v1",
	"openits.signal-control.plan-applied.v1",
	"openits.signal-control.operational-status-report.v1",
	"openits.signal-control.preemption-activated.v1",
	"openits.signal-control.preemption-cleared.v1",
	"openits.signal-control.fault-raised.v1",
	"openits.signal-control.fault-cleared.v1",
	"openits.signal-control.detector-report.v1",
	"openits.dms.mode-changed.v1",
	"openits.dms.message-changed.v1",
	"openits.dms.message-activation-failed.v1",
	"openits.dms.fault-raised.v1",
	"openits.dms.fault-cleared.v1",
	"openits.cctv.mode-changed.v1",
	"openits.cctv.tour-state-changed.v1",
	"openits.cctv.fault-raised.v1",
	"openits.cctv.fault-cleared.v1",
	"openits.traffic-sensor.traffic-interval-report.v1",
	"openits.traffic-sensor.fault-raised.v1",
	"openits.traffic-sensor.fault-cleared.v1",
	"openits.perception.zone-incident-detected.v1",
	"openits.perception.zone-incident-updated.v1",
	"openits.perception.zone-incident-cleared.v1",
	"openits.perception.zone-interval-report.v1",
	"openits.perception.fault-raised.v1",
	"openits.perception.fault-cleared.v1",
	"openits.zone-occupancy.zone-occupancy-interval-report.v1",
}

// checks names, per harness kind, the harness checks that judge the event
// stream. Every other check the harness defines for a kind is ruled out:
// stateChecks judge the device's YANG state tree, which a collector never
// publishes, and unpublished need an event the collector does not emit. TestEveryHarnessCheckIsRunOrRuledOut holds the two lists to
// the pinned harness, so a check a release adds cannot go unrun unnoticed.
// Checks are looked up by name in conformance.All: a check the harness
// renames or drops fails this test instead of silently not running.
var envelopeChecks = []string{
	"TestSubject_SevenTokenShape",
	"TestSubject_OpenitsPrefix",
	"TestCEType_OpenitsFormat",
	"TestCESource_URN",
	"TestCEID_Present",
	"TestEvent_SequenceContiguousWithinEpoch",
	"TestFault_KindMatchesService",
	"TestMode_ValueMatchesKind",
}

var checks = map[string][]string{
	"asc": append(slices.Clone(envelopeChecks),
		"TestPreemption_EventFired",
		"TestPreemption_EventTypeShape",
		"TestEvents_CabinetOutputChanged",
		"TestEvents_StatusReportFlashCause",
	),
	"dms": append(slices.Clone(envelopeChecks),
		"TestDMSOperational_Heartbeat",
		"TestDMSEvent_FaultRaisedShape",
		"TestDMSEvent_ModeChangedShape",
		"TestDMSEvent_ActivationFailedShape",
	),
	"cctv": append(slices.Clone(envelopeChecks),
		"TestCctvEvent_FaultRaisedShape",
	),
	"traffic-sensor": append(slices.Clone(envelopeChecks),
		"TestTrafficSensorEvent_IntervalReportShape",
		"TestTrafficSensorEvent_FaultRaisedShape",
	),
	"perception": append(slices.Clone(envelopeChecks),
		"TestPerceptionEvent_IncidentDetectedShape",
		"TestPerceptionEvent_IntervalCrossedReconciles",
		"TestPerceptionEvent_FaultRaisedShape",
		// The zone-occupancy capability's event checks run in the
		// perception suite, which is where the harness composes them.
		"TestZoneOccupancyEvent_IntervalReportShape",
		"TestZoneOccupancyEvent_ObservedClassReconciles",
		"TestZoneOccupancyEvent_PeakWithinObserved",
		"TestZoneOccupancyEvent_ChangedShape",
		"TestZoneOccupancyEvent_ChangedKindAgreesWithPayload",
		"TestZoneOccupancyEvent_ChangedPresentClassSumsToCount",
	),
}

// payloadFor decodes each catalog ce-type the scripts produce, so the
// harness's payload checks see real messages.
var payloadFor = map[string]func() proto.Message{
	"openits.signal-control.mode-changed.v1":              func() proto.Message { return &commonv1.ModeChanged{} },
	"openits.signal-control.cabinet-output-changed.v1":    func() proto.Message { return &scv1.CabinetOutputChanged{} },
	"openits.signal-control.plan-applied.v1":              func() proto.Message { return &scv1.PlanApplied{} },
	"openits.signal-control.operational-status-report.v1": func() proto.Message { return &scv1.OperationalStatusReport{} },
	"openits.signal-control.preemption-activated.v1":      func() proto.Message { return &scv1.PreemptionActivated{} },
	"openits.signal-control.preemption-cleared.v1":        func() proto.Message { return &scv1.PreemptionCleared{} },
	"openits.signal-control.fault-raised.v1":              func() proto.Message { return &commonv1.FaultRaised{} },
	"openits.signal-control.fault-cleared.v1":             func() proto.Message { return &commonv1.FaultCleared{} },
	"openits.signal-control.detector-report.v1":           func() proto.Message { return &scv1.DetectorReport{} },
	"openits.signal-control.comm-health-event.v1":         func() proto.Message { return &commonv1.CommHealthEvent{} },
	"openits.dms.mode-changed.v1":                         func() proto.Message { return &commonv1.ModeChanged{} },
	"openits.dms.message-changed.v1":                      func() proto.Message { return &dmsv1.MessageChanged{} },
	"openits.dms.message-activation-failed.v1":            func() proto.Message { return &dmsv1.MessageActivationFailed{} },
	"openits.dms.fault-raised.v1":                         func() proto.Message { return &commonv1.FaultRaised{} },
	"openits.dms.fault-cleared.v1":                        func() proto.Message { return &commonv1.FaultCleared{} },
	"openits.dms.sign-status-report.v1":                   func() proto.Message { return &dmsv1.SignStatusReport{} },
	"openits.cctv.mode-changed.v1":                        func() proto.Message { return &commonv1.ModeChanged{} },
	"openits.cctv.tour-state-changed.v1":                  func() proto.Message { return &cctvv1.TourStateChanged{} },
	"openits.cctv.fault-raised.v1":                        func() proto.Message { return &commonv1.FaultRaised{} },
	"openits.cctv.fault-cleared.v1":                       func() proto.Message { return &commonv1.FaultCleared{} },
	"openits.traffic-sensor.traffic-interval-report.v1":   func() proto.Message { return &tsv1.TrafficIntervalReport{} },
	"openits.traffic-sensor.fault-raised.v1":              func() proto.Message { return &commonv1.FaultRaised{} },
	"openits.traffic-sensor.fault-cleared.v1":             func() proto.Message { return &commonv1.FaultCleared{} },
	"openits.perception.zone-incident-detected.v1":        func() proto.Message { return &pcpv1.ZoneIncidentDetected{} },
	"openits.perception.zone-incident-updated.v1":         func() proto.Message { return &pcpv1.ZoneIncidentUpdated{} },
	"openits.perception.zone-incident-cleared.v1":         func() proto.Message { return &pcpv1.ZoneIncidentCleared{} },
	"openits.perception.zone-interval-report.v1":          func() proto.Message { return &pcpv1.ZoneIntervalReport{} },
	"openits.perception.fault-raised.v1":                  func() proto.Message { return &commonv1.FaultRaised{} },
	"openits.perception.fault-cleared.v1":                 func() proto.Message { return &commonv1.FaultCleared{} },
	"openits.zone-occupancy.zone-occupancy-interval-report.v1": func() proto.Message {
		return &zocv1.ZoneOccupancyIntervalReport{}
	},
}

// serviceKind is the harness kind whose suite judges a catalog service.
// zone-occupancy is a capability the harness composes into perception.
var serviceKind = map[string]string{
	"signal-control": "asc", "dms": "dms", "cctv": "cctv",
	"traffic-sensor": "traffic-sensor", "perception": "perception", "zone-occupancy": "perception",
}

func TestCollectorStreamPassesTheTier2Harness(t *testing.T) {
	ns, err := server.NewServer(&server.Options{Host: "127.0.0.1", Port: -1, JetStream: true, StoreDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	ns.Start()
	t.Cleanup(ns.Shutdown)
	if !ns.ReadyForConnections(5 * time.Second) {
		t.Fatal("nats not ready")
	}

	reg := adapter.NewRegistry()
	cfg := &config.Config{
		Region: "us-ga", Agency: "metro", AgencyUnit: "d01", Site: "cab-1",
		CollectorID: "metro-cab-1-collector", ModelVersion: "openits/v1",
	}
	for _, d := range []struct {
		kind, id string
		script   func() [][]model.Facet
	}{
		{"asc", "asc-1", ascScript},
		{"dms", "dms-1", dmsScript},
		{"cctv", "cam-1", cctvScript},
		{"traffic-sensor", "ts-1", trafficScript},
		{"perception", "pcp-1", perceptionScript},
	} {
		desc := adapter.Descriptor{Vendor: "fixture", DeviceKind: d.kind, Caps: adapter.CapState}
		reg.Register(desc, func(id string, _ map[string]any) (adapter.Adapter, error) {
			return &scripted{desc: desc, id: id, steps: d.script()}, nil
		})
		cfg.Devices = append(cfg.Devices, config.Device{ID: d.id, Vendor: "fixture", DeviceKind: d.kind, PollInterval: 30 * time.Millisecond})
	}

	// Subscribe to the catalog subject space only: that is what the harness
	// observes, and collector-owned health lives on its own root (ADR 0011).
	nc, err := nats.Connect(ns.ClientURL())
	if err != nil {
		t.Fatal(err)
	}
	defer nc.Close()
	var (
		mu     sync.Mutex
		events = map[string][]conformance.EventEnvelope{} // by harness kind
		seen   = map[string]bool{}
		stray  []string
		// undecodable lists catalog events the test could not decode.
		undecodable []string
	)
	sub, err := nc.Subscribe("openits.>", func(m *nats.Msg) {
		ceTime, _ := time.Parse(time.RFC3339Nano, m.Header.Get("ce-time"))
		env := conformance.EventEnvelope{
			Subject: m.Subject, CEType: m.Header.Get("ce-type"),
			CESource: m.Header.Get("ce-source"), CEID: m.Header.Get("ce-id"), CETime: ceTime,
		}
		// Payload checks skip an event whose Data is not the type they
		// expect, so a missing decoder would make them pass vacuously: an
		// undecodable catalog event is a failure here, not a skip.
		mu.Lock()
		defer mu.Unlock()
		if newMsg, ok := payloadFor[env.CEType]; ok {
			msg := newMsg()
			if err := proto.Unmarshal(m.Data, msg); err != nil {
				undecodable = append(undecodable, env.CEType+": "+err.Error())
			}
			env.Data = msg
		} else {
			undecodable = append(undecodable, env.CEType+": no decoder in payloadFor")
		}
		seen[env.CEType] = true
		parts := strings.Split(m.Subject, ".")
		kind, ok := "", len(parts) == 7
		if ok {
			kind, ok = serviceKind[parts[4]]
		}
		if !ok {
			stray = append(stray, m.Subject)
			return
		}
		events[kind] = append(events[kind], env)
	})
	if err != nil {
		t.Fatal(err)
	}

	start := time.Now()
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- app.Run(ctx, cfg, reg, app.Options{NATSURL: ns.ClientURL(), Version: "test"}) }()

	missing := func() []string {
		mu.Lock()
		defer mu.Unlock()
		var out []string
		for _, ce := range want {
			if !seen[ce] {
				out = append(out, ce)
			}
		}
		return out
	}
	deadline := time.Now().Add(10 * time.Second)
	for len(missing()) > 0 {
		if time.Now().After(deadline) {
			cancel()
			t.Fatalf("the scripts never produced %v; the harness checks would judge an incomplete stream", missing())
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	if err := <-done; err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("Run: %v", err)
	}
	_ = sub.Unsubscribe()

	mu.Lock()
	defer mu.Unlock()
	if len(undecodable) > 0 {
		t.Errorf("catalog payloads the harness checks could not see: %q", undecodable)
	}
	if len(stray) > 0 {
		t.Errorf("catalog-space subjects outside the 7-token shape, or for a service no harness kind judges: %q", stray)
	}
	window := time.Since(start)
	for kind, names := range checks {
		suite := map[string]func(*conformance.T, *conformance.Observation){}
		for _, tc := range conformance.All(kind) {
			suite[tc.Name] = tc.Fn
		}
		obs := &conformance.Observation{Events: events[kind], Window: window}
		for _, name := range names {
			fn, ok := suite[name]
			if !ok {
				t.Errorf("%s: the harness no longer has %s; review what replaced it", kind, name)
				continue
			}
			if r := conformance.Run(name, fn, obs); !r.Pass {
				t.Errorf("%s %s: %s", kind, name, r.Message)
			}
		}
	}
}
