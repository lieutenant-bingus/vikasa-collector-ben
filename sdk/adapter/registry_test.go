package adapter

import (
	"context"
	"testing"

	"github.com/Vikasa2M/vikasa-collector/sdk/model"
)

type fakeAdapter struct{ d Descriptor }

func (f *fakeAdapter) Descriptor() Descriptor { return f.d }
func (f *fakeAdapter) Close() error           { return nil }
func (f *fakeAdapter) Read(context.Context) (*model.Snapshot, error) {
	return &model.Snapshot{DeviceID: "x"}, nil
}

func TestRegistryBuildAndKnown(t *testing.T) {
	r := NewRegistry()
	d := Descriptor{Vendor: "ntcip", DeviceKind: "asc", Caps: CapState}
	r.Register(d, func(deviceID string, conn map[string]any) (Adapter, error) {
		return &fakeAdapter{d: d}, nil
	})

	if !r.Known("ntcip", "asc") {
		t.Fatal("ntcip-asc should be known")
	}
	if r.Known("acme", "asc") {
		t.Fatal("acme-asc should be unknown")
	}

	a, err := r.Build("ntcip", "asc", "dev-1", nil)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if _, ok := a.(StateReader); !ok {
		t.Fatal("built adapter should be a StateReader")
	}
	if !a.Descriptor().Caps.Has(CapState) || a.Descriptor().Caps.Has(CapCommand) {
		t.Fatal("capability bits wrong")
	}

	if _, err := r.Build("acme", "asc", "dev-2", nil); err == nil {
		t.Fatal("expected error for unknown adapter")
	}
}

func TestRegistryDuplicatePanics(t *testing.T) {
	r := NewRegistry()
	d := Descriptor{Vendor: "ntcip", DeviceKind: "asc", Caps: CapState}
	f := func(string, map[string]any) (Adapter, error) { return nil, nil }
	r.Register(d, f)
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic on duplicate registration")
		}
	}()
	r.Register(d, f)
}
