package maxtime

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Vikasa2M/vikasa-collector/sdk/adapter"
	"github.com/Vikasa2M/vikasa-collector/sdk/model"
)

type fakeMIBReader struct {
	values map[string]map[string]string
	errs   map[string]error
}

func (f fakeMIBReader) Get(_ context.Context, name string) (map[string]string, error) {
	if err := f.errs[name]; err != nil {
		return nil, err
	}
	return f.values[name], nil
}

func TestASCReadRecordedFixture(t *testing.T) {
	values := loadFixture(t)
	snap, err := NewASC("maxtime-asc-1", fakeMIBReader{values: values}).Read(context.Background())
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if snap.DeviceID != "maxtime-asc-1" || snap.SampledAt.IsZero() {
		t.Fatalf("bad snapshot header: %+v", snap)
	}
	if len(snap.Errors) != 0 {
		t.Fatalf("recorded healthy read has errors: %+v", snap.Errors)
	}

	signal, ok := snap.Facet(model.KindSignalStatus)
	if !ok {
		t.Fatal("missing signal-status facet")
	}
	wantSignal := model.SignalStatus{Mode: model.ModeNormal}
	if got := signal.(model.SignalStatus); !reflect.DeepEqual(got, wantSignal) {
		t.Fatalf("signal status = %+v, want %+v", got, wantSignal)
	}

	faults, ok := snap.Facet(model.KindFaultSet)
	if !ok {
		t.Fatal("missing fault-set facet")
	}
	if got := faults.(model.FaultSet).Faults; len(got) != 0 {
		t.Fatalf("healthy fixture faults = %+v", got)
	}

	detectors, ok := snap.Facet(model.KindDetectorSamples)
	if !ok {
		t.Fatal("missing detector-samples facet")
	}
	wantDetectors := model.DetectorSamples{Samples: []model.DetectorSample{
		{Channel: 1}, {Channel: 2},
	}}
	if got := detectors.(model.DetectorSamples); !reflect.DeepEqual(got, wantDetectors) {
		t.Fatalf("detectors = %+v, want %+v", got, wantDetectors)
	}
}

func TestASCReadExtendedFacetsFromFixture(t *testing.T) {
	values := loadFixture(t)
	snap, err := NewASC("maxtime-asc-1", fakeMIBReader{values: values}).Read(context.Background())
	if err != nil {
		t.Fatalf("Read: %v", err)
	}

	coord, ok := snap.Facet(model.KindCoordinationStatus)
	if !ok {
		t.Fatal("missing coordination facet")
	}
	wantCoord := model.CoordinationStatus{
		OperationalMode: 1, SelectedPattern: 254, ActualOffset: 0,
		CycleLength: 120, CoordinationMode: 2,
	}
	if got := coord.(model.CoordinationStatus); !reflect.DeepEqual(got, wantCoord) {
		t.Fatalf("coordination = %+v, want %+v", got, wantCoord)
	}

	phases, ok := snap.Facet(model.KindPhaseStatuses)
	if !ok {
		t.Fatal("missing phase facet")
	}
	if got := phases.(model.PhaseStatuses).Phases; len(got) != 4 {
		t.Fatalf("phase count = %d, want 4", len(got))
	}

	indications, ok := snap.Facet(model.KindSignalIndications)
	if !ok {
		t.Fatal("missing indication facet")
	}
	wantIndications := model.SignalIndications{Channels: []model.ChannelIndication{
		{Channel: 1, Color: model.SignalColorRed},
		{Channel: 2, Color: model.SignalColorRed},
		{Channel: 3, Color: model.SignalColorRed},
		{Channel: 4, Color: model.SignalColorRed},
	}}
	if got := indications.(model.SignalIndications); !reflect.DeepEqual(got, wantIndications) {
		t.Fatalf("indications = %+v, want %+v", got, wantIndications)
	}

	detStatus, ok := snap.Facet(model.KindDetectorChannelStatuses)
	if !ok {
		t.Fatal("missing detector-channel-status facet")
	}
	if got := detStatus.(model.DetectorChannelStatuses).Channels; len(got) != 0 {
		t.Fatalf("detector channel status = %+v, want empty when no calls/active", got)
	}
}

func TestASCAlarmMapping(t *testing.T) {
	values := healthyValues()
	values[mibShortAlarms] = map[string]string{"1": "160"} // critical + detector fault
	values[mibAlarms1] = map[string]string{"1": "16"}      // MMU flash
	values[mibAlarms2] = map[string]string{"1": "3"}       // power + low battery
	values[mibAlarmStatus] = map[string]string{"4": "1"}

	snap, err := NewASC("maxtime-asc-1", fakeMIBReader{values: values}).Read(context.Background())
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	faults, ok := snap.Facet(model.KindFaultSet)
	if !ok {
		t.Fatal("missing fault facet")
	}
	gotIDs := make([]string, 0, len(faults.(model.FaultSet).Faults))
	for _, fault := range faults.(model.FaultSet).Faults {
		gotIDs = append(gotIDs, fault.ID)
	}
	wantIDs := []string{
		"custom-alarm-4",
		"short-alarm-critical",
		"short-alarm-detector-fault",
		"unit-alarm-1-mmu-flash",
		"unit-alarm-2-low-battery",
		"unit-alarm-2-power-restart",
	}
	if !reflect.DeepEqual(gotIDs, wantIDs) {
		t.Fatalf("fault IDs = %v, want %v", gotIDs, wantIDs)
	}
}

func TestASCDetectorAlarmMapping(t *testing.T) {
	values := healthyValues()
	values[mibDetectorAlarms] = map[string]string{"2": "131"} // no activity + max presence + other
	values[mibDetectorGroupAlarms] = map[string]string{"1": "1"}

	snap, err := NewASC("maxtime-asc-1", fakeMIBReader{values: values}).Read(context.Background())
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	faults, ok := snap.Facet(model.KindFaultSet)
	if !ok {
		t.Fatal("missing fault facet")
	}
	gotIDs := make([]string, 0, len(faults.(model.FaultSet).Faults))
	for _, fault := range faults.(model.FaultSet).Faults {
		gotIDs = append(gotIDs, fault.ID)
	}
	wantIDs := []string{
		"detector-alarm-2-max-presence",
		"detector-alarm-2-no-activity",
		"detector-alarm-2-other",
		"detector-group-alarm-1",
	}
	if !reflect.DeepEqual(gotIDs, wantIDs) {
		t.Fatalf("fault IDs = %v, want %v", gotIDs, wantIDs)
	}
}

func TestASCMapsControllerAndDetectorValues(t *testing.T) {
	values := healthyValues()
	values[mibControllerOpMode] = map[string]string{"1": "7"} // Flash
	values[mibPatternStatus] = map[string]string{"1": "3"}    // Pattern 3
	values[mibPreemptStatus] = map[string]string{"1": "2"}    // Preempt 2
	values[mibDetectorVolume] = map[string]string{"1": "1234", "2": "99"}
	values[mibDetectorOccupancy] = map[string]string{"1": "600", "2": "1000"}

	snap, err := NewASC("maxtime-asc-1", fakeMIBReader{values: values}).Read(context.Background())
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	signal, ok := snap.Facet(model.KindSignalStatus)
	if !ok {
		t.Fatal("missing signal facet")
	}
	wantSignal := model.SignalStatus{
		Mode: model.ModeFlash, ActivePlanID: 3,
		PreemptionActive: true, PreemptionSource: "preempt-2",
	}
	if got := signal.(model.SignalStatus); !reflect.DeepEqual(got, wantSignal) {
		t.Fatalf("signal status = %+v, want %+v", got, wantSignal)
	}
	detectors, ok := snap.Facet(model.KindDetectorSamples)
	if !ok {
		t.Fatal("missing detector facet")
	}
	wantDetectors := model.DetectorSamples{Samples: []model.DetectorSample{
		{Channel: 1, VolumeCount: 1234, OccupancyTenths: 600},
		{Channel: 2, VolumeCount: 99, OccupancyTenths: 1000},
	}}
	if !reflect.DeepEqual(detectors, wantDetectors) {
		t.Fatalf("detectors = %+v, want %+v", detectors, wantDetectors)
	}
}

func TestASCMapsMaxtimeNumericEnumsWithoutRenumbering(t *testing.T) {
	tests := []struct {
		name  string
		value int64
		want  bool
	}{
		{name: "off", value: 2, want: false},
		{name: "fault monitor flash", value: 5, want: true},
		{name: "mmu flash", value: 6, want: true},
		{name: "startup flash", value: 7, want: true},
		{name: "preempt flash", value: 8, want: true},
		{name: "local manual", value: 4, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := flashActive(tt.value); got != tt.want {
				t.Fatalf("flashActive(%d) = %v, want %v", tt.value, got, tt.want)
			}
		})
	}

	if got := patternID(254); got != 0 {
		t.Fatalf("patternID(254) = %d, want 0 (MAXTIME means Free)", got)
	}
	if got := clampOccupancyTenths(600); got != 600 {
		t.Fatalf("clampOccupancyTenths(600) = %d, want 600 (60.0%%)", got)
	}
}

func TestASCUnansweredFlashStatusFailsSignalFacet(t *testing.T) {
	values := healthyValues()
	snap, err := NewASC("maxtime-asc-1", fakeMIBReader{
		values: values,
		errs:   map[string]error{mibFlashStatus: errors.New("flash endpoint unavailable")},
	}).Read(context.Background())
	if err != nil {
		t.Fatalf("flash endpoint failure must not be a hard read error: %v", err)
	}
	if !snap.FacetFailed(model.KindSignalStatus) {
		t.Fatal("unanswered FlashSta must produce a FacetError, not a fabricated signal facet")
	}
	if _, ok := snap.Facet(model.KindSignalStatus); ok {
		t.Fatal("signal facet must be omitted when FlashSta is unreadable")
	}
}

func TestASCMalformedControllerOpModeFailsSignalFacet(t *testing.T) {
	values := healthyValues()
	values[mibControllerOpMode] = map[string]string{"1": "not-a-number"}
	snap, err := NewASC("maxtime-asc-1", fakeMIBReader{values: values}).Read(context.Background())
	if err != nil {
		t.Fatalf("malformed controllerOpMode must not be a hard read error: %v", err)
	}
	if !snap.FacetFailed(model.KindSignalStatus) {
		t.Fatal("malformed controllerOpMode must produce a FacetError")
	}
	if _, ok := snap.Facet(model.KindSignalStatus); ok {
		t.Fatal("signal facet must be omitted when controllerOpMode is malformed")
	}
}

func TestASCFacetsFailIndependently(t *testing.T) {
	values := healthyValues()
	snap, err := NewASC("maxtime-asc-1", fakeMIBReader{
		values: values,
		errs: map[string]error{
			mibShortAlarms:       errors.New("alarm endpoint unavailable"),
			mibDetectorOccupancy: errors.New("occupancy endpoint unavailable"),
		},
	}).Read(context.Background())
	if err != nil {
		t.Fatalf("partial endpoint failures must not be hard errors: %v", err)
	}
	if _, ok := snap.Facet(model.KindSignalStatus); !ok {
		t.Fatal("signal facet must survive independent failures")
	}
	if !snap.FacetFailed(model.KindFaultSet) {
		t.Fatal("fault facet should have a FacetError")
	}
	if !snap.FacetFailed(model.KindDetectorSamples) {
		t.Fatal("detector facet should have a FacetError")
	}
}

func TestASCPrimaryEndpointFailureIsHardError(t *testing.T) {
	_, err := NewASC("maxtime-asc-1", fakeMIBReader{
		errs: map[string]error{mibControllerOpMode: errors.New("connection refused")},
	}).Read(context.Background())
	if err == nil {
		t.Fatal("controller operation endpoint failure must be a hard read error")
	}
}

func TestASCUsesHTTPMIBEndpoints(t *testing.T) {
	fixture := loadFixture(t)
	requests := make(map[string]int)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := path.Base(r.URL.Path)
		values, ok := fixture[name]
		if !ok || !strings.HasPrefix(r.URL.Path, "/maxtime/api/mibs/") {
			http.NotFound(w, r)
			return
		}
		requests[name]++
		w.Header().Set("Content-Type", "application/octet-stream")
		body, _ := json.Marshal([]struct {
			Name string            `json:"name"`
			Data map[string]string `json:"data"`
		}{{Name: name, Data: values}})
		_, _ = w.Write(body)
	}))
	defer server.Close()

	client, err := newHTTPMIBReader(server.URL+"/maxtime", time.Second)
	if err != nil {
		t.Fatalf("newHTTPMIBReader: %v", err)
	}
	snap, err := NewASC("maxtime-asc-1", client).Read(context.Background())
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(snap.Errors) != 0 {
		t.Fatalf("HTTP fixture read errors: %+v", snap.Errors)
	}
	for _, name := range []string{
		mibControllerOpMode, mibFlashStatus, mibPatternStatus, mibPreemptStatus,
		mibCoordOperational, mibCoordSelected, mibActualOffset, mibCoordCycleLength, mibCoordModeStatus,
		mibSignalState, mibRedIndications, mibYellowIndications, mibGreenIndications,
		mibShortAlarms, mibAlarms1, mibAlarms2, mibAlarmStatus,
		mibDetectorAlarms, mibDetectorGroupAlarms,
		mibVehicleCalls, mibDetectorGroupActive,
		mibDetectorVolume, mibDetectorOccupancy,
	} {
		if requests[name] != 1 {
			t.Fatalf("GET %s count = %d, want 1", name, requests[name])
		}
	}
}

func TestHTTPMIBReaderAuthenticatesWithMaxtimeTransportContract(t *testing.T) {
	var loginCalls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/maxprofile/accounts/loginWithPassword":
			loginCalls++
			if got := r.Header.Get("Content-Type"); got != "application/x-www-form-urlencoded" {
				t.Errorf("login content type = %q", got)
			}
			body, _ := io.ReadAll(r.Body)
			decoded, err := url.QueryUnescape(string(body))
			if err != nil {
				t.Fatalf("decode login body: %v", err)
			}
			var request struct {
				User struct {
					Username string `json:"username"`
				} `json:"user"`
				Password string `json:"password"`
			}
			if err := json.Unmarshal([]byte(decoded), &request); err != nil {
				t.Fatalf("decode login JSON: %v", err)
			}
			if request.User.Username != "admin" || request.Password != "secret" {
				t.Fatalf("login payload = %+v", request)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":{"tokens":{"accessToken":"test-token"}}}`))
		case r.URL.Path == "/maxtime/api/mibs/controllerOpMode":
			if got := r.Header.Get("accounts-access-token"); got != "test-token" {
				t.Fatalf("access token header = %q", got)
			}
			_, _ = w.Write([]byte(`[{"name":"controllerOpMode","data":{"1":"5"}}]`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client, err := newHTTPMIBReader(server.URL+"/maxtime", time.Second, "admin", "secret")
	if err != nil {
		t.Fatalf("newHTTPMIBReader: %v", err)
	}
	values, err := client.Get(context.Background(), mibControllerOpMode)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if loginCalls != 1 {
		t.Fatalf("login calls = %d, want 1", loginCalls)
	}
	if values["1"] != "5" {
		t.Fatalf("values = %+v", values)
	}
}

func TestASCDescriptor(t *testing.T) {
	got := NewASC("maxtime-asc-1", fakeMIBReader{}).Descriptor()
	want := adapter.Descriptor{Vendor: "maxtime", DeviceKind: "asc", Caps: adapter.CapState}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Descriptor = %+v, want %+v", got, want)
	}
}

func TestParseHTTPBlockRejectsMalformedConfig(t *testing.T) {
	tests := []struct {
		name string
		conn map[string]any
	}{
		{name: "missing block", conn: map[string]any{}},
		{name: "missing URL", conn: map[string]any{"http": map[string]any{}}},
		{name: "bad timeout", conn: map[string]any{"http": map[string]any{
			"base_url": "http://controller/maxtime", "timeout": "soon",
		}}},
		{name: "username without password", conn: map[string]any{"http": map[string]any{
			"base_url": "http://controller/maxtime", "username": "admin",
		}}},
		{name: "bad channels", conn: map[string]any{"http": map[string]any{
			"base_url": "http://controller/maxtime", "detector_channels": []any{0},
		}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := parseHTTPBlock(tt.conn); err == nil {
				t.Fatal("parseHTTPBlock accepted malformed config")
			}
		})
	}
}

func TestRegisterToRecognizesOnlyDistinctMaxtimeKey(t *testing.T) {
	registry := adapter.NewRegistry()
	RegisterTo(registry)
	if !registry.Known("maxtime", "asc") {
		t.Fatal("maxtime-asc was not registered")
	}
	if registry.Known("ntcip", "asc") {
		t.Fatal("RegisterTo must not replace ntcip-asc")
	}
}

func loadFixture(t *testing.T) map[string]map[string]string {
	t.Helper()
	raw, err := os.ReadFile("testdata/live/healthy.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(raw, &envelope); err != nil {
		t.Fatalf("decode fixture envelope: %v", err)
	}
	values := make(map[string]map[string]string, len(envelope))
	for name, rawRecord := range envelope {
		var records []struct {
			Data map[string]string `json:"data"`
		}
		if err := json.Unmarshal(rawRecord, &records); err != nil {
			t.Fatalf("decode %s fixture: %v", name, err)
		}
		if len(records) != 1 {
			t.Fatalf("%s fixture has %d records, want 1", name, len(records))
		}
		values[name] = records[0].Data
	}
	return values
}

func healthyValues() map[string]map[string]string {
	return map[string]map[string]string{
		mibControllerOpMode:    {"1": "5"},
		mibFlashStatus:         {"1": "2"},
		mibPatternStatus:       {"1": "254"},
		mibPreemptStatus:       {"1": "0"},
		mibCoordOperational:    {"1": "1"},
		mibCoordSelected:       {"1": "254"},
		mibActualOffset:        {"1": "0"},
		mibCoordCycleLength:    {"1": "120"},
		mibCoordModeStatus:     {"1": "2"},
		mibSignalState:         {"1": "2", "2": "2"},
		mibRedIndications:      {"1": "3"},
		mibYellowIndications:   {"1": "0"},
		mibGreenIndications:    {"1": "0"},
		mibShortAlarms:         {"1": "0"},
		mibAlarms1:             {"1": "0"},
		mibAlarms2:             {"1": "0"},
		mibAlarmStatus:         {"1": "0"},
		mibDetectorAlarms:      {"1": "0", "2": "0"},
		mibDetectorGroupAlarms: {"1": "0"},
		mibVehicleCalls:        {"1": "0"},
		mibDetectorGroupActive: {"1": "0"},
		mibDetectorVolume:      {"1": "0", "2": "0"},
		mibDetectorOccupancy:   {"1": "0", "2": "0"},
	}
}
