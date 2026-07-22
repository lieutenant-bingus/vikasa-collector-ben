package adapter

import "fmt"

// Registry maps "<vendor>-<device_kind>" keys to adapter factories.
// Config validation uses Known as the trust boundary: a device whose
// vendor/kind has no registered adapter fails boot.
type Registry struct {
	factories map[string]Factory
}

func NewRegistry() *Registry {
	return &Registry{factories: make(map[string]Factory)}
}

// Register panics on duplicate keys — duplicates are programmer error.
func (r *Registry) Register(d Descriptor, f Factory) {
	k := d.Key()
	if _, exists := r.factories[k]; exists {
		panic("adapter: duplicate registration for " + k)
	}
	r.factories[k] = f
}

// Known reports whether an adapter is registered for vendor+deviceKind.
func (r *Registry) Known(vendor, deviceKind string) bool {
	_, ok := r.factories[Descriptor{Vendor: vendor, DeviceKind: deviceKind}.Key()]
	return ok
}

// Build constructs the adapter for one configured device.
func (r *Registry) Build(vendor, deviceKind, deviceID string, conn map[string]any) (Adapter, error) {
	k := Descriptor{Vendor: vendor, DeviceKind: deviceKind}.Key()
	f, ok := r.factories[k]
	if !ok {
		return nil, fmt.Errorf("no adapter registered for %q", k)
	}
	return f(deviceID, conn)
}
