package adapter

import (
	"errors"
	"fmt"
)

// ErrNotRegistered is returned (wrapped) by Build when no adapter is
// registered for the requested vendor and device kind.
var ErrNotRegistered = errors.New("adapter: none registered")

// Registry maps "<vendor>-<device_kind>" keys to adapter factories.
// Config validation uses Known as the trust boundary: a device whose
// vendor/kind has no registered adapter fails boot.
//
// The zero value is ready to use. A Registry is not safe for concurrent
// Register calls; register everything before the first Build.
type Registry struct {
	factories map[string]Factory
}

// NewRegistry returns an empty Registry. It is equivalent to new(Registry).
func NewRegistry() *Registry { return new(Registry) }

// Register adds f under d.Key(). It panics on a duplicate key: two adapters
// for one vendor and device kind is a programming error.
func (r *Registry) Register(d Descriptor, f Factory) {
	k := d.Key()
	if _, exists := r.factories[k]; exists {
		panic("adapter: duplicate registration for " + k)
	}
	if r.factories == nil {
		r.factories = make(map[string]Factory)
	}
	r.factories[k] = f
}

// Known reports whether an adapter is registered for vendor+deviceKind.
func (r *Registry) Known(vendor, deviceKind string) bool {
	_, ok := r.factories[Descriptor{Vendor: vendor, DeviceKind: deviceKind}.Key()]
	return ok
}

// Build constructs the adapter for one configured device. An unregistered
// vendor and device kind returns an error wrapping ErrNotRegistered; a
// factory failure is returned wrapped, with the registry key added.
func (r *Registry) Build(vendor, deviceKind, deviceID string, conn map[string]any) (Adapter, error) {
	k := Descriptor{Vendor: vendor, DeviceKind: deviceKind}.Key()
	f, ok := r.factories[k]
	if !ok {
		return nil, fmt.Errorf("%w for %q", ErrNotRegistered, k)
	}
	a, err := f(deviceID, conn)
	if err != nil {
		return nil, fmt.Errorf("adapter %s: %w", k, err)
	}
	return a, nil
}
