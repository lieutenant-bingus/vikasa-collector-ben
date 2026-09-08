// Package maxtime implements the HTTP-facing ASC adapter for MAXTIME.
package maxtime

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Vikasa2M/vikasa-collector/sdk/adapter"
	"github.com/Vikasa2M/vikasa-collector/sdk/model"
)

const (
	mibControllerOpMode    = "controllerOpMode"
	mibFlashStatus         = "FlashSta"
	mibPatternStatus       = "PatrnSta"
	mibPreemptStatus       = "preemptStatus"
	mibShortAlarms         = "SAlarms"
	mibAlarms1             = "Alarms1"
	mibAlarms2             = "Alarms2"
	mibAlarmStatus         = "alarmSta"
	mibDetectorVolume      = "Volume"
	mibDetectorOccupancy   = "percentOccupancy"
	mibDetectorAlarms      = "DtAlrms"
	mibDetectorGroupAlarms = "DtGpAlrm"
	mibSignalState         = "signalState"
	mibRedIndications      = "Reds"
	mibYellowIndications   = "Yellows"
	mibGreenIndications    = "Greens"
	mibCoordOperational    = "CoordOpp"
	mibCoordSelected       = "CoordSel"
	mibActualOffset        = "actualOffset"
	mibCoordCycleLength    = "coordCycleLengthStatus"
	mibCoordModeStatus     = "coordCoordinationModeStatus"
	mibVehicleCalls        = "VehCalls"
	mibDetectorGroupActive = "DtGrpAct"
)

var ascDescriptor = adapter.Descriptor{
	Vendor: "maxtime", DeviceKind: "asc", Caps: adapter.CapState,
}

// mibReader is deliberately smaller than http.Client so the adapter can test
// response handling without making the model layer know about HTTP.
type mibReader interface {
	Get(context.Context, string) (map[string]string, error)
}

type asc struct {
	deviceID         string
	client           mibReader
	now              func() time.Time
	detectorChannels []uint32
}

// NewASC wraps a MIB reader as a maxtime-asc StateReader. It is exported so
// fixture tests can inject a deterministic reader.
func NewASC(deviceID string, client interface {
	Get(context.Context, string) (map[string]string, error)
}) adapter.StateReader {
	return &asc{deviceID: deviceID, client: client, now: time.Now}
}

func (a *asc) Descriptor() adapter.Descriptor { return ascDescriptor }
func (a *asc) Close() error                   { return nil }

func (a *asc) Read(ctx context.Context) (*model.Snapshot, error) {
	op, err := a.client.Get(ctx, mibControllerOpMode)
	if err != nil {
		// This request is the controller's primary liveness check. A failure
		// here means the device did not answer the poll at all.
		return nil, fmt.Errorf("maxtime-asc %s: %w", a.deviceID, err)
	}

	snap := &model.Snapshot{
		DeviceID:  a.deviceID,
		SampledAt: a.now().UTC(),
	}
	a.readSignalStatus(ctx, snap, op)
	a.readCoordinationStatus(ctx, snap)
	a.readPhaseStatuses(ctx, snap)
	a.readSignalIndications(ctx, snap)
	a.readFaultSet(ctx, snap)
	a.readDetectors(ctx, snap)
	a.readDetectorChannelStatuses(ctx, snap)
	return snap, nil
}

func (a *asc) readSignalStatus(ctx context.Context, snap *model.Snapshot, op map[string]string) {
	opValue, err := oneValue(op, mibControllerOpMode)
	if err != nil {
		snap.Errors = append(snap.Errors, model.FacetError{
			Kind: model.KindSignalStatus, Err: "controller operation mode: " + err.Error(),
		})
		return
	}

	pattern, err := a.client.Get(ctx, mibPatternStatus)
	if err != nil {
		snap.Errors = append(snap.Errors, model.FacetError{
			Kind: model.KindSignalStatus, Err: "pattern status: " + err.Error(),
		})
		return
	}
	preempt, err := a.client.Get(ctx, mibPreemptStatus)
	if err != nil {
		snap.Errors = append(snap.Errors, model.FacetError{
			Kind: model.KindSignalStatus, Err: "preempt status: " + err.Error(),
		})
		return
	}

	mode, err := parseInt(opValue, mibControllerOpMode)
	if err != nil {
		snap.Errors = append(snap.Errors, model.FacetError{
			Kind: model.KindSignalStatus, Err: err.Error(),
		})
		return
	}
	plan, err := firstInt(pattern, mibPatternStatus)
	if err != nil {
		snap.Errors = append(snap.Errors, model.FacetError{
			Kind: model.KindSignalStatus, Err: "pattern status: " + err.Error(),
		})
		return
	}
	preemptValue, err := firstInt(preempt, mibPreemptStatus)
	if err != nil {
		snap.Errors = append(snap.Errors, model.FacetError{
			Kind: model.KindSignalStatus, Err: "preempt status: " + err.Error(),
		})
		return
	}
	flash, err := a.client.Get(ctx, mibFlashStatus)
	if err != nil {
		snap.Errors = append(snap.Errors, model.FacetError{
			Kind: model.KindSignalStatus, Err: "flash status: " + err.Error(),
		})
		return
	}
	flashValue, err := firstInt(flash, mibFlashStatus)
	if err != nil {
		snap.Errors = append(snap.Errors, model.FacetError{
			Kind: model.KindSignalStatus, Err: "flash status: " + err.Error(),
		})
		return
	}

	status := model.SignalStatus{
		Mode:            modeFromControllerOp(mode),
		ActivePlanID:    patternID(plan),
		InConflictFlash: flashActive(flashValue) || plan == 253 || plan == 255 || mode == 9,
	}
	if preemptValue > 0 {
		status.PreemptionActive = true
		status.PreemptionSource = fmt.Sprintf("preempt-%d", preemptValue)
	}
	snap.Facets = append(snap.Facets, status)
}

func (a *asc) readFaultSet(ctx context.Context, snap *model.Snapshot) {
	bitfields := []struct {
		name   string
		prefix string
		bits   []faultBit
	}{
		{
			name: mibShortAlarms, prefix: "short-alarm",
			bits: []faultBit{
				{0, "preempt", model.SeverityMajor, model.CategoryController},
				{1, "t-and-f-flash", model.SeverityMajor, model.CategoryController},
				{2, "local-zero", model.SeverityMajor, model.CategoryController},
				{3, "local-override", model.SeverityMajor, model.CategoryController},
				{4, "coord-alarm", model.SeverityMajor, model.CategoryController},
				{5, "detector-fault", model.SeverityMajor, model.CategoryDetector},
				{6, "non-critical", model.SeverityWarning, model.CategoryController},
				{7, "critical", model.SeverityCritical, model.CategoryController},
			},
		},
		{
			name: mibAlarms1, prefix: "unit-alarm-1",
			bits: []faultBit{
				{0, "cycle-fault", model.SeverityMajor, model.CategoryController},
				{1, "coord-fault", model.SeverityMajor, model.CategoryController},
				{2, "coord-fail", model.SeverityMajor, model.CategoryController},
				{3, "cycle-fail", model.SeverityMajor, model.CategoryController},
				{4, "mmu-flash", model.SeverityCritical, model.CategoryConflict},
				{5, "local-flash", model.SeverityMajor, model.CategoryController},
				{6, "local-free", model.SeverityMinor, model.CategoryController},
			},
		},
		{
			name: mibAlarms2, prefix: "unit-alarm-2",
			bits: []faultBit{
				{0, "power-restart", model.SeverityMajor, model.CategoryPower},
				{1, "low-battery", model.SeverityMajor, model.CategoryPower},
				{2, "response-fault", model.SeverityMajor, model.CategoryCommunication},
				{3, "external-start", model.SeverityInfo, model.CategoryController},
				{4, "stop-time", model.SeverityMinor, model.CategoryController},
				{5, "transition", model.SeverityMinor, model.CategoryController},
			},
		},
	}

	var faults []model.Fault
	for _, field := range bitfields {
		values, err := a.client.Get(ctx, field.name)
		if err != nil {
			snap.Errors = append(snap.Errors, model.FacetError{
				Kind: model.KindFaultSet, Err: field.name + ": " + err.Error(),
			})
			return
		}
		value, err := firstInt(values, field.name)
		if err != nil {
			snap.Errors = append(snap.Errors, model.FacetError{
				Kind: model.KindFaultSet, Err: field.name + ": " + err.Error(),
			})
			return
		}
		for _, bit := range field.bits {
			if value&(1<<bit.position) == 0 {
				continue
			}
			faults = append(faults, model.Fault{
				ID:          field.prefix + "-" + bit.id,
				Severity:    bit.severity,
				Category:    bit.category,
				Description: bit.id,
			})
		}
	}

	// Custom alarm groups have no stable descriptions in the status response,
	// but their group indexes are stable and still useful to consumers.
	custom, err := a.client.Get(ctx, mibAlarmStatus)
	if err != nil {
		snap.Errors = append(snap.Errors, model.FacetError{
			Kind: model.KindFaultSet, Err: mibAlarmStatus + ": " + err.Error(),
		})
		return
	}
	for index, raw := range custom {
		value, parseErr := parseInt(raw, mibAlarmStatus+"."+index)
		if parseErr != nil {
			snap.Errors = append(snap.Errors, model.FacetError{
				Kind: model.KindFaultSet, Err: parseErr.Error(),
			})
			return
		}
		if value != 0 {
			faults = append(faults, model.Fault{
				ID:          "custom-alarm-" + index,
				Severity:    model.SeverityWarning,
				Category:    model.CategoryController,
				Description: "custom alarm " + index,
			})
		}
	}

	detectorAlarms, err := a.client.Get(ctx, mibDetectorAlarms)
	if err != nil {
		snap.Errors = append(snap.Errors, model.FacetError{
			Kind: model.KindFaultSet, Err: mibDetectorAlarms + ": " + err.Error(),
		})
		return
	}
	for index, raw := range detectorAlarms {
		value, parseErr := parseInt(raw, mibDetectorAlarms+"."+index)
		if parseErr != nil {
			snap.Errors = append(snap.Errors, model.FacetError{
				Kind: model.KindFaultSet, Err: parseErr.Error(),
			})
			return
		}
		for _, bit := range detectorAlarmBits {
			if value&(1<<bit.position) == 0 {
				continue
			}
			faults = append(faults, model.Fault{
				ID:          "detector-alarm-" + index + "-" + bit.id,
				Severity:    model.SeverityWarning,
				Category:    model.CategoryDetector,
				Description: bit.id,
			})
		}
	}

	detectorGroupAlarms, err := a.client.Get(ctx, mibDetectorGroupAlarms)
	if err != nil {
		snap.Errors = append(snap.Errors, model.FacetError{
			Kind: model.KindFaultSet, Err: mibDetectorGroupAlarms + ": " + err.Error(),
		})
		return
	}
	for index, raw := range detectorGroupAlarms {
		value, parseErr := parseInt(raw, mibDetectorGroupAlarms+"."+index)
		if parseErr != nil {
			snap.Errors = append(snap.Errors, model.FacetError{
				Kind: model.KindFaultSet, Err: parseErr.Error(),
			})
			return
		}
		if value != 0 {
			faults = append(faults, model.Fault{
				ID:          "detector-group-alarm-" + index,
				Severity:    model.SeverityWarning,
				Category:    model.CategoryDetector,
				Description: "detector group alarm " + index,
			})
		}
	}

	sort.Slice(faults, func(i, j int) bool { return faults[i].ID < faults[j].ID })
	snap.Facets = append(snap.Facets, model.FaultSet{Faults: faults})
}

func (a *asc) readDetectors(ctx context.Context, snap *model.Snapshot) {
	volumes, err := a.client.Get(ctx, mibDetectorVolume)
	if err != nil {
		snap.Errors = append(snap.Errors, model.FacetError{
			Kind: model.KindDetectorSamples, Err: mibDetectorVolume + ": " + err.Error(),
		})
		return
	}
	occupancies, err := a.client.Get(ctx, mibDetectorOccupancy)
	if err != nil {
		snap.Errors = append(snap.Errors, model.FacetError{
			Kind: model.KindDetectorSamples, Err: mibDetectorOccupancy + ": " + err.Error(),
		})
		return
	}

	channels := a.detectorChannels
	if len(channels) == 0 {
		channels = commonChannels(volumes, occupancies)
	}
	samples := make([]model.DetectorSample, 0, len(channels))
	for _, channel := range channels {
		key := strconv.FormatUint(uint64(channel), 10)
		rawVolume, volumeOK := volumes[key]
		rawOccupancy, occupancyOK := occupancies[key]
		if !volumeOK || !occupancyOK {
			// A partially populated table is not evidence of a zero reading.
			continue
		}
		volume, err := parseUint32(rawVolume, mibDetectorVolume+"."+key)
		if err != nil {
			snap.Errors = append(snap.Errors, model.FacetError{
				Kind: model.KindDetectorSamples, Err: err.Error(),
			})
			return
		}
		occupancy, err := parseInt(rawOccupancy, mibDetectorOccupancy+"."+key)
		if err != nil {
			snap.Errors = append(snap.Errors, model.FacetError{
				Kind: model.KindDetectorSamples, Err: err.Error(),
			})
			return
		}
		samples = append(samples, model.DetectorSample{
			Channel: channel, VolumeCount: volume,
			OccupancyTenths: clampOccupancyTenths(occupancy),
		})
	}
	sort.Slice(samples, func(i, j int) bool { return samples[i].Channel < samples[j].Channel })
	snap.Facets = append(snap.Facets, model.DetectorSamples{Samples: samples})
}

func (a *asc) readCoordinationStatus(ctx context.Context, snap *model.Snapshot) {
	read := func(name string) (map[string]string, error) {
		values, err := a.client.Get(ctx, name)
		if err != nil {
			return nil, err
		}
		return values, nil
	}
	opp, err := read(mibCoordOperational)
	if err != nil {
		snap.Errors = append(snap.Errors, model.FacetError{
			Kind: model.KindCoordinationStatus, Err: mibCoordOperational + ": " + err.Error(),
		})
		return
	}
	sel, err := read(mibCoordSelected)
	if err != nil {
		snap.Errors = append(snap.Errors, model.FacetError{
			Kind: model.KindCoordinationStatus, Err: mibCoordSelected + ": " + err.Error(),
		})
		return
	}
	offset, err := read(mibActualOffset)
	if err != nil {
		snap.Errors = append(snap.Errors, model.FacetError{
			Kind: model.KindCoordinationStatus, Err: mibActualOffset + ": " + err.Error(),
		})
		return
	}
	cycle, err := read(mibCoordCycleLength)
	if err != nil {
		snap.Errors = append(snap.Errors, model.FacetError{
			Kind: model.KindCoordinationStatus, Err: mibCoordCycleLength + ": " + err.Error(),
		})
		return
	}
	mode, err := read(mibCoordModeStatus)
	if err != nil {
		snap.Errors = append(snap.Errors, model.FacetError{
			Kind: model.KindCoordinationStatus, Err: mibCoordModeStatus + ": " + err.Error(),
		})
		return
	}

	oppValue, err := firstInt(opp, mibCoordOperational)
	if err != nil {
		snap.Errors = append(snap.Errors, model.FacetError{Kind: model.KindCoordinationStatus, Err: err.Error()})
		return
	}
	selValue, err := firstInt(sel, mibCoordSelected)
	if err != nil {
		snap.Errors = append(snap.Errors, model.FacetError{Kind: model.KindCoordinationStatus, Err: err.Error()})
		return
	}
	offsetValue, err := firstInt(offset, mibActualOffset)
	if err != nil {
		snap.Errors = append(snap.Errors, model.FacetError{Kind: model.KindCoordinationStatus, Err: err.Error()})
		return
	}
	cycleValue, err := firstInt(cycle, mibCoordCycleLength)
	if err != nil {
		snap.Errors = append(snap.Errors, model.FacetError{Kind: model.KindCoordinationStatus, Err: err.Error()})
		return
	}
	modeValue, err := firstInt(mode, mibCoordModeStatus)
	if err != nil {
		snap.Errors = append(snap.Errors, model.FacetError{Kind: model.KindCoordinationStatus, Err: err.Error()})
		return
	}

	snap.Facets = append(snap.Facets, model.CoordinationStatus{
		OperationalMode:  oppValue,
		SelectedPattern:  selValue,
		ActualOffset:     offsetValue,
		CycleLength:      cycleValue,
		CoordinationMode: modeValue,
	})
}

func (a *asc) readPhaseStatuses(ctx context.Context, snap *model.Snapshot) {
	values, err := a.client.Get(ctx, mibSignalState)
	if err != nil {
		snap.Errors = append(snap.Errors, model.FacetError{
			Kind: model.KindPhaseStatuses, Err: mibSignalState + ": " + err.Error(),
		})
		return
	}
	phases := make([]model.PhaseStatus, 0, len(values))
	for key, raw := range values {
		channel, err := strconv.ParseUint(key, 10, 32)
		if err != nil || channel == 0 {
			continue
		}
		stateValue, err := parseInt(raw, mibSignalState+"."+key)
		if err != nil {
			snap.Errors = append(snap.Errors, model.FacetError{
				Kind: model.KindPhaseStatuses, Err: err.Error(),
			})
			return
		}
		phases = append(phases, model.PhaseStatus{
			PhaseNumber: uint32(channel),
			State:       signalStateFromMaxtime(stateValue),
		})
	}
	sort.Slice(phases, func(i, j int) bool { return phases[i].PhaseNumber < phases[j].PhaseNumber })
	snap.Facets = append(snap.Facets, model.PhaseStatuses{Phases: phases})
}

func (a *asc) readSignalIndications(ctx context.Context, snap *model.Snapshot) {
	reds, err := a.client.Get(ctx, mibRedIndications)
	if err != nil {
		snap.Errors = append(snap.Errors, model.FacetError{
			Kind: model.KindSignalIndications, Err: mibRedIndications + ": " + err.Error(),
		})
		return
	}
	yellows, err := a.client.Get(ctx, mibYellowIndications)
	if err != nil {
		snap.Errors = append(snap.Errors, model.FacetError{
			Kind: model.KindSignalIndications, Err: mibYellowIndications + ": " + err.Error(),
		})
		return
	}
	greens, err := a.client.Get(ctx, mibGreenIndications)
	if err != nil {
		snap.Errors = append(snap.Errors, model.FacetError{
			Kind: model.KindSignalIndications, Err: mibGreenIndications + ": " + err.Error(),
		})
		return
	}

	channels := indicationChannelNumbers(reds, yellows, greens)
	indications := make([]model.ChannelIndication, 0, len(channels))
	for _, channel := range channels {
		indications = append(indications, model.ChannelIndication{
			Channel: channel,
			Color:   indicationColor(channel, reds, yellows, greens),
		})
	}
	snap.Facets = append(snap.Facets, model.SignalIndications{Channels: indications})
}

func (a *asc) readDetectorChannelStatuses(ctx context.Context, snap *model.Snapshot) {
	calls, err := a.client.Get(ctx, mibVehicleCalls)
	if err != nil {
		snap.Errors = append(snap.Errors, model.FacetError{
			Kind: model.KindDetectorChannelStatuses, Err: mibVehicleCalls + ": " + err.Error(),
		})
		return
	}
	active, err := a.client.Get(ctx, mibDetectorGroupActive)
	if err != nil {
		snap.Errors = append(snap.Errors, model.FacetError{
			Kind: model.KindDetectorChannelStatuses, Err: mibDetectorGroupActive + ": " + err.Error(),
		})
		return
	}

	channels := a.detectorChannels
	if len(channels) == 0 {
		channels = unionBitmapChannels(calls, active)
	}
	callActive, err := channelsFromBitmapTable(calls, mibVehicleCalls)
	if err != nil {
		snap.Errors = append(snap.Errors, model.FacetError{
			Kind: model.KindDetectorChannelStatuses, Err: err.Error(),
		})
		return
	}
	presenceActive, err := channelsFromBitmapTable(active, mibDetectorGroupActive)
	if err != nil {
		snap.Errors = append(snap.Errors, model.FacetError{
			Kind: model.KindDetectorChannelStatuses, Err: err.Error(),
		})
		return
	}
	statuses := make([]model.DetectorChannelStatus, 0, len(channels))
	for _, channel := range channels {
		statuses = append(statuses, model.DetectorChannelStatus{
			Channel:        channel,
			CallActive:     callActive[channel],
			PresenceActive: presenceActive[channel],
		})
	}
	sort.Slice(statuses, func(i, j int) bool { return statuses[i].Channel < statuses[j].Channel })
	snap.Facets = append(snap.Facets, model.DetectorChannelStatuses{Channels: statuses})
}

func signalStateFromMaxtime(value int64) model.SignalState {
	// MAXTIME exposes NTCIP-style signalState enumerations. Unlisted values
	// stay unknown rather than coerced into a neighbor.
	switch value {
	case 1:
		return model.SignalStateDark
	case 2:
		return model.SignalStateStop
	case 3:
		return model.SignalStatePreMovement
	case 4:
		return model.SignalStatePermissiveMovement
	case 5:
		return model.SignalStateProtectedMovement
	case 6:
		return model.SignalStateClearance
	default:
		return model.SignalStateUnknown
	}
}

func indicationChannelNumbers(reds, yellows, greens map[string]string) []uint32 {
	seen := make(map[uint32]bool)
	for _, table := range []map[string]string{reds, yellows, greens} {
		channels, err := bitmapChannelList(table, "indication")
		if err != nil {
			continue
		}
		for _, channel := range channels {
			seen[channel] = true
		}
	}
	channels := make([]uint32, 0, len(seen))
	for channel := range seen {
		channels = append(channels, channel)
	}
	sort.Slice(channels, func(i, j int) bool { return channels[i] < channels[j] })
	return channels
}

func indicationColor(channel uint32, reds, yellows, greens map[string]string) model.SignalColor {
	greenActive, _ := channelsFromBitmapTable(greens, mibGreenIndications)
	if greenActive[channel] {
		return model.SignalColorGreen
	}
	yellowActive, _ := channelsFromBitmapTable(yellows, mibYellowIndications)
	if yellowActive[channel] {
		return model.SignalColorYellow
	}
	redActive, _ := channelsFromBitmapTable(reds, mibRedIndications)
	if redActive[channel] {
		return model.SignalColorRed
	}
	return model.SignalColorOff
}

func unionBitmapChannels(left, right map[string]string) []uint32 {
	seen := make(map[uint32]bool)
	for _, table := range []map[string]string{left, right} {
		channels, err := bitmapChannelList(table, "bitmap")
		if err != nil {
			continue
		}
		for _, channel := range channels {
			seen[channel] = true
		}
	}
	channels := make([]uint32, 0, len(seen))
	for channel := range seen {
		channels = append(channels, channel)
	}
	sort.Slice(channels, func(i, j int) bool { return channels[i] < channels[j] })
	return channels
}

func channelsFromBitmapTable(table map[string]string, name string) (map[uint32]bool, error) {
	channels, err := bitmapChannelList(table, name)
	if err != nil {
		return nil, err
	}
	out := make(map[uint32]bool, len(channels))
	for _, channel := range channels {
		out[channel] = true
	}
	return out, nil
}

func bitmapChannelList(table map[string]string, name string) ([]uint32, error) {
	channels := make([]uint32, 0)
	for groupKey, raw := range table {
		group, err := strconv.ParseUint(groupKey, 10, 32)
		if err != nil || group == 0 {
			continue
		}
		value, err := parseInt(raw, name+"."+groupKey)
		if err != nil {
			return nil, err
		}
		for bit := 0; bit < 32; bit++ {
			if value&(1<<bit) == 0 {
				continue
			}
			channels = append(channels, (uint32(group)-1)*32+uint32(bit)+1)
		}
	}
	sort.Slice(channels, func(i, j int) bool { return channels[i] < channels[j] })
	return channels, nil
}

type faultBit struct {
	position uint
	id       string
	severity model.FaultSeverity
	category model.FaultCategory
}

var detectorAlarmBits = []struct {
	position uint
	id       string
}{
	{0, "no-activity"},
	{1, "max-presence"},
	{2, "erratic-count"},
	{3, "communications"},
	{4, "configuration"},
	{7, "other"},
}

func oneValue(values map[string]string, name string) (string, error) {
	if len(values) == 0 {
		return "", fmt.Errorf("%s returned no values", name)
	}
	if value, ok := values["1"]; ok {
		return value, nil
	}
	for _, value := range values {
		return value, nil
	}
	return "", fmt.Errorf("%s returned no values", name)
}

func firstInt(values map[string]string, name string) (int64, error) {
	value, err := oneValue(values, name)
	if err != nil {
		return 0, err
	}
	return parseInt(value, name)
}

func parseInt(raw, name string) (int64, error) {
	value, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%s value %q: %w", name, raw, err)
	}
	return value, nil
}

func parseUint32(raw, name string) (uint32, error) {
	value, err := strconv.ParseUint(strings.TrimSpace(raw), 10, 32)
	if err != nil {
		return 0, fmt.Errorf("%s value %q: %w", name, raw, err)
	}
	return uint32(value), nil
}

func commonChannels(left, right map[string]string) []uint32 {
	channels := make([]uint32, 0)
	for key := range left {
		if _, ok := right[key]; !ok {
			continue
		}
		channel, err := strconv.ParseUint(key, 10, 32)
		if err == nil && channel > 0 {
			channels = append(channels, uint32(channel))
		}
	}
	sort.Slice(channels, func(i, j int) bool { return channels[i] < channels[j] })
	return channels
}

func patternID(value int64) uint32 {
	if value < 1 || value > 128 {
		return 0
	}
	return uint32(value)
}

func modeFromControllerOp(value int64) model.ControllerMode {
	switch value {
	case 1, 6, 7, 8, 9:
		return model.ModeFlash
	case 2:
		return model.ModeStandby
	case 3, 4, 5, 10, 12, 13:
		return model.ModeNormal
	case 11:
		return model.ModeOff
	default:
		return model.ModeUnknown
	}
}

func flashActive(value int64) bool {
	// MAXTIME unitFlashStatus: 2 is Off; 5–8 are fault-monitor, MMU,
	// startup, and preempt flash respectively. "Other" (1), automatic (3),
	// and local manual (4) do not assert a known active flash cause.
	return value >= 5 && value <= 8
}

func clampOccupancyTenths(value int64) uint16 {
	switch {
	case value < 0:
		return 0
	case value > 1000:
		return 1000
	default:
		return uint16(value)
	}
}

type httpMIBReader struct {
	baseURL     *url.URL
	profileURL  *url.URL
	client      *http.Client
	username    string
	password    string
	authMu      sync.Mutex
	accessToken string
}

func newHTTPMIBReader(baseURL string, timeout time.Duration, credentials ...string) (*httpMIBReader, error) {
	if len(credentials) != 0 && len(credentials) != 2 {
		return nil, fmt.Errorf("username and password must be provided together")
	}
	parsed, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil {
		return nil, fmt.Errorf("invalid base_url: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, fmt.Errorf("base_url must use http or https")
	}
	if parsed.Host == "" {
		return nil, fmt.Errorf("base_url host is required")
	}
	if timeout <= 0 {
		return nil, fmt.Errorf("timeout must be positive")
	}
	profileURL := *parsed
	profileURL.Path = "/maxprofile/accounts/loginWithPassword"
	profileURL.RawQuery = ""
	profileURL.Fragment = ""
	username, password := "", ""
	if len(credentials) == 2 {
		username, password = credentials[0], credentials[1]
	}
	return &httpMIBReader{
		baseURL: parsed, profileURL: &profileURL,
		client:   &http.Client{Timeout: timeout},
		username: username, password: password,
	}, nil
}

func (c *httpMIBReader) Get(ctx context.Context, name string) (map[string]string, error) {
	endpoint := *c.baseURL
	endpoint.Path = strings.TrimRight(endpoint.Path, "/") + "/api/mibs/" + url.PathEscape(name)
	token, err := c.token(ctx)
	if err != nil {
		return nil, err
	}
	values, status, err := c.get(ctx, endpoint, token)
	if err == nil || status != http.StatusUnauthorized || c.username == "" {
		return values, err
	}
	c.clearToken(token)
	token, err = c.token(ctx)
	if err != nil {
		return nil, err
	}
	values, _, err = c.get(ctx, endpoint, token)
	return values, err
}

func (c *httpMIBReader) get(ctx context.Context, endpoint url.URL, token string) (map[string]string, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, 0, err
	}
	if token != "" {
		req.Header.Set("accounts-access-token", token)
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, resp.StatusCode, fmt.Errorf("GET %s: HTTP %s", endpoint.Path, resp.Status)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("GET %s: read response: %w", endpoint.Path, err)
	}
	var records []struct {
		Name string            `json:"name"`
		Data map[string]string `json:"data"`
	}
	if err := json.Unmarshal(body, &records); err != nil {
		return nil, resp.StatusCode, fmt.Errorf("GET %s: decode response: %w", endpoint.Path, err)
	}
	if len(records) == 0 {
		return map[string]string{}, resp.StatusCode, nil
	}
	if records[0].Data == nil {
		return map[string]string{}, resp.StatusCode, nil
	}
	return records[0].Data, resp.StatusCode, nil
}

func (c *httpMIBReader) token(ctx context.Context) (string, error) {
	if c.username == "" {
		return "", nil
	}
	c.authMu.Lock()
	defer c.authMu.Unlock()
	if c.accessToken != "" {
		return c.accessToken, nil
	}

	var request struct {
		User struct {
			Username string `json:"username"`
		} `json:"user"`
		Password string `json:"password"`
	}
	request.User.Username = c.username
	request.Password = c.password
	payload, err := json.Marshal(request)
	if err != nil {
		return "", fmt.Errorf("login request: encode: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.profileURL.String(),
		strings.NewReader(url.QueryEscape(string(payload))))
	if err != nil {
		return "", fmt.Errorf("login request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := c.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("login request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return "", fmt.Errorf("login request: HTTP %s", resp.Status)
	}
	var result struct {
		Data struct {
			Tokens struct {
				AccessToken string `json:"accessToken"`
			} `json:"tokens"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("login response: decode: %w", err)
	}
	if result.Data.Tokens.AccessToken == "" {
		return "", fmt.Errorf("login response: access token missing")
	}
	c.accessToken = result.Data.Tokens.AccessToken
	return c.accessToken, nil
}

func (c *httpMIBReader) clearToken(token string) {
	c.authMu.Lock()
	defer c.authMu.Unlock()
	if c.accessToken == token {
		c.accessToken = ""
	}
}
