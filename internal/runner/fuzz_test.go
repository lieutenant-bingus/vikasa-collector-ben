package runner

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Vikasa2M/vikasa-collector/internal/synth"
	"github.com/Vikasa2M/vikasa-collector/sdk/adapter"
	"github.com/Vikasa2M/vikasa-collector/sdk/model"
)

// fuzzAdapter misbehaves as data directs: every way an adapter's Read can
// hand the core something malformed.
type fuzzAdapter struct{ data []byte }

func (f *fuzzAdapter) Descriptor() adapter.Descriptor {
	return adapter.Descriptor{Vendor: "fuzz", DeviceKind: "asc", Caps: adapter.CapState}
}
func (f *fuzzAdapter) Close() error { return nil }

func (f *fuzzAdapter) Read(context.Context) (*model.Snapshot, error) {
	next := func() byte {
		if len(f.data) == 0 {
			return 0
		}
		b := f.data[0]
		f.data = f.data[1:]
		return b
	}
	switch next() % 5 {
	case 0:
		panic(string(f.data))
	case 1:
		return nil, nil
	case 2:
		return nil, errors.New(string(f.data))
	case 3:
		var nilErr *adapterFault
		panic(nilErr)
	}
	snap := &model.Snapshot{DeviceID: "asc-1", SampledAt: time.Unix(int64(next()), 0)}
	for range next() % 8 {
		switch next() % 8 {
		case 0:
			snap.Facets = append(snap.Facets, nil)
		case 1:
			snap.Facets = append(snap.Facets, (*model.SignalStatus)(nil))
		case 2:
			snap.Facets = append(snap.Facets, model.SignalStatus{CabinetOutput: model.CabinetOutput(next()), Mode: model.ControllerMode(next())})
		case 3:
			snap.Facets = append(snap.Facets, model.FaultSet{Faults: []model.Fault{{ID: string(rune(next())), Category: model.FaultCategory(next())}}})
		case 4:
			snap.Facets = append(snap.Facets, model.DetectorSamples{})
		case 5:
			snap.Errors = append(snap.Errors, model.FacetError{Kind: model.Kind(string(rune(next())))})
		case 6:
			snap.Errors = append(snap.Errors, model.FacetError{Kind: model.KindSignalStatus})
		case 7:
			snap.Facets = append(snap.Facets, model.FaultSet{Faults: nil})
		}
	}
	return snap, nil
}

// SECURITY.md puts "a malformed or malicious device response that crashes
// the collector" in scope. Whatever an adapter returns or does — panics,
// nil snapshots, nil or wrong-typed facets, facets it also reports failed —
// one poll must never take the process down. Every engine differ is
// registered, so a facet reaches real diffing code.
func FuzzPollSurvivesAnyAdapter(f *testing.F) {
	for _, seed := range [][]byte{
		{0, 'x'}, {1}, {2, 'e'}, {3},
		{4, 0, 2, 2, 1, 1, 4}, // two well-formed facets
		{4, 0, 1, 0},          // a nil facet
		{4, 0, 1, 1},          // a typed nil pointer
		{4, 0, 2, 2, 1, 1, 6}, // a facet also reported failed
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		engine := synth.NewEngine(
			synth.NewSignalDiffer(), synth.NewFaultDiffer(), synth.NewDetectorDiffer(),
			synth.NewDMSDiffer(), synth.NewDMSEnvironmentDiffer(), synth.NewTrafficIntervalDiffer(),
			synth.NewZoneIncidentDiffer(), synth.NewZoneIntervalDiffer(), synth.NewCCTVDiffer(),
		)
		dev := &fuzzAdapter{data: data}
		r := New(dev, Config{Jitter: noJitter, DeviceID: "asc-1", Interval: time.Second, Engine: engine, Out: func([]model.Event) {}})
		// Twice: the second poll diffs against whatever the first stored.
		dev2 := &fuzzAdapter{data: append([]byte(nil), data...)}
		r.pollOnce(t.Context())
		r.dev = dev2
		r.pollOnce(t.Context())
	})
}
