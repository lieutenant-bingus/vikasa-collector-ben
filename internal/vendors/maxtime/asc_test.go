package maxtime

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/Vikasa2M/vikasa-collector/sdk/model"
	"github.com/Vikasa2M/vikasa-collector/sdk/transport/httpapi"
	"github.com/Vikasa2M/vikasa-collector/sdk/transport/httpapi/httpapitest"
)

var fixedNow = func() time.Time { return time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC) }

var batchKey = "GET /maxtime/api/mibs/" + strings.Join(pollMIBs, ",") + "/"

func newASC(c httpapi.Client, mut ...func(*mibAPI)) *asc {
	api := &mibAPI{client: c, prefix: "/maxtime", batch: true}
	for _, m := range mut {
		m(api)
	}
	return &asc{deviceID: "asc-1", api: api, now: fixedNow}
}

// answer builds the API's JSON answer from name → rows.
func answer(t *testing.T, objs map[string]rows) string {
	t.Helper()
	type rec struct {
		Name string `json:"name"`
		Data rows   `json:"data"`
	}
	var out []rec
	for _, n := range pollMIBs {
		if r, ok := objs[n]; ok {
			out = append(out, rec{n, r})
		}
	}
	b, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// healthy is a free-running controller with no alarms and two detectors.
func healthy() map[string]rows {
	return map[string]rows{
		mibFlashStatus: {"1": "2"}, mibPatternStatus: {"1": "254"}, mibPreemptStatus: {"1": "0"},
		mibShortAlarms: {"1": "0"}, mibAlarms1: {"1": "0"}, mibAlarms2: {"1": "0"},
		mibDetectorAlarms: {"1": "0", "2": "0"},
		mibVolume:         {"1": "0", "2": "0"}, mibOccupancy: {"1": "0", "2": "0"},
	}
}

func read(t *testing.T, objs map[string]rows) *model.Snapshot {
	t.Helper()
	s := &httpapitest.Static{}
	s.Set(batchKey, 200, answer(t, objs))
	snap, err := newASC(s).Read(t.Context())
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	return snap
}

func TestReadGoldenFromLabFixture(t *testing.T) {
	snap, err := newASC(httpapitest.LoadFixture(t, "testdata/asc-healthy-free.json")).Read(t.Context())
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(snap.Errors) != 0 {
		t.Fatalf("facet errors on a healthy controller: %+v", snap.Errors)
	}
	want := []model.Facet{
		model.SignalStatus{Mode: model.ModeFree, CabinetOutput: model.OutputNormal},
		model.FaultSet{},
		model.DetectorSamples{Samples: []model.DetectorSample{{Channel: 1}, {Channel: 2}}},
	}
	if diff := cmp.Diff(want, snap.Facets); diff != "" {
		t.Fatalf("facets (-want +got):\n%s", diff)
	}
	if snap.DeviceID != "asc-1" || !snap.SampledAt.Equal(fixedNow()) {
		t.Errorf("header = %s %s", snap.DeviceID, snap.SampledAt)
	}
}

func TestSignalStatusMapping(t *testing.T) {
	for _, tc := range []struct {
		name                    string
		flash, pattern, preempt string
		want                    model.SignalStatus
	}{
		{"coordinated plan 3", "2", "3", "0", model.SignalStatus{Mode: model.ModeCoordinated, CabinetOutput: model.OutputNormal, ActivePlanID: 3}},
		{"free", "2", "254", "0", model.SignalStatus{Mode: model.ModeFree, CabinetOutput: model.OutputNormal}},
		{"mmu flash", "6", "255", "0", model.SignalStatus{CabinetOutput: model.OutputFlash, FlashCause: model.FlashMonitor}},
		{"manual flash keeps coordination", "4", "5", "0", model.SignalStatus{Mode: model.ModeCoordinated, CabinetOutput: model.OutputFlash, FlashCause: model.FlashManual, ActivePlanID: 5}},
		{"startup flash", "7", "0", "0", model.SignalStatus{CabinetOutput: model.OutputFlash, FlashCause: model.FlashStartup}},
		{"fault monitor", "5", "255", "0", model.SignalStatus{CabinetOutput: model.OutputFlash, FlashCause: model.FlashControllerFault}},
		{"preempt 2", "2", "254", "2", model.SignalStatus{Mode: model.ModeFree, CabinetOutput: model.OutputNormal, PreemptionActive: true, PreemptionSource: "preempt-2"}},
		{"out-of-range flash is unknown, not normal", "9", "254", "0", model.SignalStatus{Mode: model.ModeFree, CabinetOutput: model.OutputUnknown}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o := healthy()
			o[mibFlashStatus], o[mibPatternStatus], o[mibPreemptStatus] = rows{"1": tc.flash}, rows{"1": tc.pattern}, rows{"1": tc.preempt}
			f, ok := read(t, o).Facet(model.KindSignalStatus)
			if !ok {
				t.Fatal("no signal-status facet")
			}
			if diff := cmp.Diff(tc.want, f); diff != "" {
				t.Errorf("(-want +got):\n%s", diff)
			}
		})
	}
}

func TestFaultBitsAndStatesThatAreNotFaults(t *testing.T) {
	o := healthy()
	// SAlarms: T&F flash (bit1) + preempt (bit0, a state) + local override (bit3, a state).
	o[mibShortAlarms] = rows{"1": "11"}
	// Alarms1: coord fail (bit2) + MMU flash (bit4) + local free (bit6, a state).
	o[mibAlarms1] = rows{"1": "84"}
	// Alarms2: low battery (bit1) + external start (bit3, a state).
	o[mibAlarms2] = rows{"1": "10"}
	// Detector 3 and detector 10 failed.
	o[mibDetectorAlarms] = rows{"1": "4", "2": "2"}
	f, _ := read(t, o).Facet(model.KindFaultSet)
	var ids []string
	for _, x := range f.(model.FaultSet).Faults {
		ids = append(ids, x.ID)
	}
	want := []string{"tf-flash", "coord-fail", "mmu-fault", "low-battery", "detector-3-fault", "detector-10-fault"}
	if diff := cmp.Diff(want, ids); diff != "" {
		t.Errorf("fault ids (-want +got):\n%s", diff)
	}
}

func TestDetectorSummaryBitOnlyWithoutPerDetectorTable(t *testing.T) {
	o := healthy()
	o[mibShortAlarms] = rows{"1": "32"} // bit5 detector fault
	f, _ := read(t, o).Facet(model.KindFaultSet)
	if n := len(f.(model.FaultSet).Faults); n != 0 {
		t.Errorf("with DtAlrms answered and clear, the summary bit must not raise: got %d faults", n)
	}
	delete(o, mibDetectorAlarms)
	f, _ = read(t, o).Facet(model.KindFaultSet)
	if fs := f.(model.FaultSet).Faults; len(fs) != 1 || fs[0].ID != "detector-fault" {
		t.Errorf("without DtAlrms the summary bit raises once: %+v", fs)
	}
}

func TestFacetsFailIndependently(t *testing.T) {
	o := healthy()
	delete(o, mibAlarms2)
	delete(o, mibOccupancy)
	snap := read(t, o)
	if !snap.FacetFailed(model.KindFaultSet) || !snap.FacetFailed(model.KindDetectorSamples) {
		t.Errorf("want fault-set and detector facet errors, got %+v", snap.Errors)
	}
	if _, ok := snap.Facet(model.KindSignalStatus); !ok || snap.FacetFailed(model.KindSignalStatus) {
		t.Errorf("signal status must survive the other facets failing")
	}
	o = healthy()
	o[mibPatternStatus] = rows{"1": "abc"}
	if snap := read(t, o); !snap.FacetFailed(model.KindSignalStatus) {
		t.Errorf("unparseable PatrnSta must fail signal status: %+v", snap.Errors)
	}
}

func TestDetectorRows(t *testing.T) {
	o := healthy()
	o[mibVolume] = rows{"1": "12", "2": "7", "3": "5", "4": "1"}
	o[mibOccupancy] = rows{"1": "25", "2": "101", "4": "0"} // 2 out of range, 3 half-read
	f, _ := read(t, o).Facet(model.KindDetectorSamples)
	want := model.DetectorSamples{Samples: []model.DetectorSample{
		{Channel: 1, VolumeCount: 12, OccupancyTenths: 250},
		{Channel: 4, VolumeCount: 1},
	}}
	if diff := cmp.Diff(want, f); diff != "" {
		t.Errorf("(-want +got):\n%s", diff)
	}
	o[mibVolume], o[mibOccupancy] = rows{}, rows{}
	if f, _ := read(t, o).Facet(model.KindDetectorSamples); len(f.(model.DetectorSamples).Samples) != 0 {
		t.Errorf("no detectors is an empty facet")
	}
}

func TestUnreachableAndRefusedAreHardErrors(t *testing.T) {
	if _, err := newASC(&httpapitest.Static{Err: errors.New("timeout")}).Read(t.Context()); err == nil {
		t.Error("unreachable: want error")
	}
	s := &httpapitest.Static{}
	s.Set(batchKey, http.StatusUnauthorized, "")
	if _, err := newASC(s).Read(t.Context()); !errors.Is(err, errAuth) {
		t.Errorf("401: err = %v, want errAuth", err)
	}
	s.Set(batchKey, http.StatusInternalServerError, "")
	if _, err := newASC(s).Read(t.Context()); err == nil {
		t.Error("500 on the only request: want error")
	}
}

func TestBasicAuthHeader(t *testing.T) {
	s := &httpapitest.Static{}
	s.Set(batchKey, 200, answer(t, healthy()))
	a := newASC(s, func(m *mibAPI) { m.auth, m.username, m.password = authBasic, "admin", "pw" })
	if _, err := a.Read(t.Context()); err != nil {
		t.Fatal(err)
	}
	if h := s.Requests()[0].Header.Get("Authorization"); h != "Basic YWRtaW46cHc=" {
		t.Errorf("Authorization = %q", h)
	}
}

func TestMaxProfileLoginAndReloginOn401(t *testing.T) {
	s := &httpapitest.Static{}
	s.Set("POST "+maxProfileLoginPath, 200, `{"data":{"tokens":{"accessToken":"tok-1"}}}`)
	s.Set(batchKey, 200, answer(t, healthy()))
	a := newASC(s, func(m *mibAPI) { m.auth, m.username, m.password = authMaxProfile, "admin", "pw" })
	if _, err := a.Read(t.Context()); err != nil {
		t.Fatal(err)
	}
	reqs := s.Requests()
	if len(reqs) != 2 || reqs[0].Method != http.MethodPost || reqs[1].Header.Get(tokenHeader) != "tok-1" {
		t.Fatalf("requests = %+v", reqs)
	}
	if _, err := a.Read(t.Context()); err != nil || len(s.Requests()) != 3 {
		t.Fatalf("second poll must reuse the session: %d requests, %v", len(s.Requests()), err)
	}
	// Session expiry: the next GET is refused once, then succeeds after a
	// fresh login.
	s.FailCall = nil
	s2 := &expiring{Static: s}
	a.api.client = s2
	if _, err := a.Read(t.Context()); err != nil {
		t.Fatalf("re-login: %v", err)
	}
	if s2.logins != 1 {
		t.Errorf("logins after expiry = %d, want 1", s2.logins)
	}
}

// expiring answers the first MIB GET with 401, as an expired session does.
type expiring struct {
	*httpapitest.Static
	refused bool
	logins  int
}

func (e *expiring) Do(ctx context.Context, req httpapi.Request) (httpapi.Response, error) {
	if req.Method == http.MethodPost {
		e.logins++
	} else if !e.refused {
		e.refused = true
		return httpapi.Response{Status: http.StatusUnauthorized}, nil
	}
	return e.Static.Do(ctx, req)
}

func TestNonBatchModeReadsPerObject(t *testing.T) {
	s := &httpapitest.Static{}
	for n, r := range healthy() {
		if n == mibAlarms2 {
			s.Set("GET /maxtime/api/mibs/"+n+"/", http.StatusInternalServerError, "")
			continue
		}
		b, _ := json.Marshal([]map[string]any{{"name": n, "data": r}})
		s.Set("GET /maxtime/api/mibs/"+n+"/", 200, string(b))
	}
	snap, err := newASC(s, func(m *mibAPI) { m.batch = false }).Read(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Requests()) != len(pollMIBs) {
		t.Errorf("requests = %d, want %d", len(s.Requests()), len(pollMIBs))
	}
	if !snap.FacetFailed(model.KindFaultSet) || snap.FacetFailed(model.KindSignalStatus) {
		t.Errorf("one object failing fails only its facet: %+v", snap.Errors)
	}
}

func TestDecodeAcceptsNumbers(t *testing.T) {
	got, err := decodeMIBs([]byte(`[{"name":"FlashSta","data":{"1":2}},{"name":"","data":{"1":"x"}}]`))
	if err != nil || got["FlashSta"]["1"] != "2" || len(got) != 1 {
		t.Fatalf("got %v, %v", got, err)
	}
	if _, err := decodeMIBs([]byte(`<html>`)); err == nil {
		t.Error("non-JSON body: want error")
	}
}

// The lab controller as found: coordinated on plan 30, normal output, no
// alarms, no traffic. Volume reports 128 channels but percentOccupancy only
// 64; a channel without both is not sampled.
func TestReadLabFixture2151(t *testing.T) {
	snap, err := newASC(httpapitest.LoadFixture(t, "testdata/asc-lab-2.15.1.json")).Read(t.Context())
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(snap.Errors) != 0 {
		t.Fatalf("facet errors: %+v", snap.Errors)
	}
	if len(snap.Facets) != 3 {
		t.Fatalf("facets = %+v", snap.Facets)
	}
	if diff := cmp.Diff(model.SignalStatus{Mode: model.ModeCoordinated, CabinetOutput: model.OutputNormal, ActivePlanID: 30}, snap.Facets[0]); diff != "" {
		t.Errorf("status (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(model.FaultSet{}, snap.Facets[1]); diff != "" {
		t.Errorf("faults (-want +got):\n%s", diff)
	}
	d := snap.Facets[2].(model.DetectorSamples)
	if len(d.Samples) != 64 || d.Samples[0].Channel != 1 || d.Samples[63].Channel != 64 {
		t.Errorf("detector samples: %d, first %+v", len(d.Samples), d.Samples[:1])
	}
}

// Preempt 3 held (PreCtrSt) on the lab controller. SAlarms bit 0 (preempt)
// is set and Alarms1's coord-active bit drops for the hold; neither is a
// fault. PatrnSta stays 30, so the mode stays coordinated.
func TestReadLabPreemptFixture(t *testing.T) {
	snap, err := newASC(httpapitest.LoadFixture(t, "testdata/asc-lab-preempt.json")).Read(t.Context())
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(snap.Errors) != 0 || len(snap.Facets) != 3 {
		t.Fatalf("facets %+v, errors %+v", snap.Facets, snap.Errors)
	}
	want := model.SignalStatus{Mode: model.ModeCoordinated, CabinetOutput: model.OutputNormal, ActivePlanID: 30,
		PreemptionActive: true, PreemptionSource: "preempt-3"}
	if diff := cmp.Diff(want, snap.Facets[0]); diff != "" {
		t.Errorf("status (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(model.FaultSet{}, snap.Facets[1]); diff != "" {
		t.Errorf("a preempt is not a fault (-want +got):\n%s", diff)
	}
}

// Phase calls latched on the lab controller; detector counts stayed 0.
func TestReadLabTrafficFixture(t *testing.T) {
	snap, err := newASC(httpapitest.LoadFixture(t, "testdata/asc-lab-traffic.json")).Read(t.Context())
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if diff := cmp.Diff(model.SignalStatus{Mode: model.ModeCoordinated, CabinetOutput: model.OutputNormal, ActivePlanID: 30}, snap.Facets[0]); diff != "" {
		t.Errorf("status (-want +got):\n%s", diff)
	}
	if d := snap.Facets[2].(model.DetectorSamples); len(d.Samples) != 64 {
		t.Errorf("detector samples = %d, want 64", len(d.Samples))
	}
}

// SNMP-driven actuations on the lab controller, recorded while detector 3
// was held: the API's Volume is the last completed 900 s period's total
// (det 1 = 180, det 4 = 900, above NTCIP's 254 cap), and percentOccupancy is
// 0 on every channel although SNMP detectorOccupancy read 40, 200 and 40.
// ADR 0026: this pins what the API says, not what the adapter should
// publish.
func TestReadLabDetectorFixture(t *testing.T) {
	snap, err := newASC(httpapitest.LoadFixture(t, "testdata/asc-lab-detectors.json")).Read(t.Context())
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	d := snap.Facets[2].(model.DetectorSamples)
	got := map[uint32]model.DetectorSample{}
	for _, s := range d.Samples {
		got[s.Channel] = s
	}
	for ch, vol := range map[uint32]uint32{1: 180, 3: 0, 4: 900, 5: 0} {
		if s := got[ch]; s.VolumeCount != vol || s.OccupancyTenths != 0 {
			t.Errorf("channel %d = %+v, want volume %d, occupancy 0", ch, s, vol)
		}
	}
}
