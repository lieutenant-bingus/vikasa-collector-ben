package maxtime

import (
	"context"
	"time"

	"github.com/Vikasa2M/vikasa-collector/sdk/adapter"
	"github.com/Vikasa2M/vikasa-collector/sdk/model"
)

// device is the registered maxtime-asc adapter. It always polls MIB state and,
// when asclog is enabled, also implements EventReader for the HR log.
type device struct {
	*asc
	log           *asclog
	eventInterval time.Duration
}

func (d *device) Descriptor() adapter.Descriptor {
	return adapter.Descriptor{
		Vendor: "maxtime", DeviceKind: "asc",
		Caps: adapter.CapState | adapter.CapEvents,
	}
}

func (d *device) Close() error {
	if d.asc != nil {
		_ = d.asc.Close()
	}
	if d.log != nil {
		_ = d.log.Close()
	}
	return nil
}

func (d *device) Read(ctx context.Context) (*model.Snapshot, error) {
	return d.asc.Read(ctx)
}

func (d *device) Fetch(ctx context.Context) ([]model.Event, error) {
	return d.log.Fetch(ctx)
}

// EventPollInterval is the cadence for the EventRunner. Zero means the app
// should fall back to the device's configured poll_interval.
func (d *device) EventPollInterval() time.Duration { return d.eventInterval }
