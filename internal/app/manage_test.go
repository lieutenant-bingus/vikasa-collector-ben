package app

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Vikasa2M/vikasa-collector/internal/config"
	"github.com/Vikasa2M/vikasa-collector/sdk/adapter"
	"github.com/Vikasa2M/vikasa-collector/sdk/model"
)

// switchableASC answers or times out on command.
type switchableASC struct{ failing atomic.Bool }

func (s *switchableASC) Descriptor() adapter.Descriptor {
	return adapter.Descriptor{Vendor: "fixture", DeviceKind: "asc", Caps: adapter.CapState}
}
func (s *switchableASC) Close() error { return nil }
func (s *switchableASC) Read(context.Context) (*model.Snapshot, error) {
	if s.failing.Load() {
		return nil, errors.New("read timeout")
	}
	return &model.Snapshot{DeviceID: "asc-1", SampledAt: time.Now().UTC(),
		Facets: []model.Facet{model.SignalStatus{Mode: model.ModeNormal}}}, nil
}

func freeAddr(t *testing.T) string {
	t.Helper()
	l, err := new(net.ListenConfig).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	_ = l.Close()
	return addr
}

// runWithSurface runs the collector with the status surface on and NO broker
// (nothing listens on natsPort), and returns its base URL.
func runWithSurface(t *testing.T, dev adapter.Adapter) string {
	t.Helper()
	reg := adapter.NewRegistry()
	reg.Register(dev.Descriptor(), func(string, map[string]any) (adapter.Adapter, error) { return dev, nil })
	cfg := &config.Config{
		Region: "us-ga", Agency: "metro", AgencyUnit: "d01", Site: "cab-1",
		CollectorID: "metro-cab-1-collector", ModelVersion: "openits/v1",
		Devices: []config.Device{{ID: "asc-1", Vendor: "fixture", DeviceKind: "asc", PollInterval: 10 * time.Millisecond}},
	}
	addr, noBroker := freeAddr(t), "nats://"+freeAddr(t)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, cfg, reg, Options{NATSURL: noBroker, Version: "test", StatusAddr: addr})
	}()
	t.Cleanup(func() { cancel(); <-done })
	return "http://" + addr
}

func status(t *testing.T, base, path string) (int, map[string]any) {
	t.Helper()
	req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, base+path, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, nil
	}
	defer func() { _ = resp.Body.Close() }()
	var body map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&body)
	return resp.StatusCode, body
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// Ready needs a successful poll, not a broker: the broker is down throughout
// this test. Then readiness latches — every device failing afterwards does
// not make the collector unready, or a supervisor would restart-loop it for
// a comms outage it did not cause.
func TestReadinessLatchesAndIgnoresTheBroker(t *testing.T) {
	dev := &switchableASC{}
	base := runWithSurface(t, dev)
	eventually(t, "/readyz 200 with no broker", func() bool { c, _ := status(t, base, "/readyz"); return c == http.StatusOK })

	dev.failing.Store(true)
	eventually(t, "several failed polls", func() bool {
		_, body := status(t, base, "/status")
		devs, _ := body["devices"].([]any)
		if len(devs) != 1 {
			return false
		}
		n, _ := devs[0].(map[string]any)["consecutive_failures"].(float64)
		return n >= 5
	})
	if c, _ := status(t, base, "/readyz"); c != http.StatusOK {
		t.Fatalf("/readyz = %d after the device went down; readiness must latch", c)
	}
	if _, body := status(t, base, "/status"); body["broker"].(map[string]any)["connected"] != false {
		t.Fatalf("/status broker = %v, want connected=false", body["broker"])
	}
}

// Config parsed, process up, but no device answering: not ready. This is
// the failure a rollback must catch — a config pointing at the wrong
// addresses starts cleanly and polls nothing.
func TestNotReadyWhileNoDeviceAnswers(t *testing.T) {
	dev := &switchableASC{}
	dev.failing.Store(true)
	base := runWithSurface(t, dev)
	eventually(t, "the surface to come up", func() bool { c, _ := status(t, base, "/livez"); return c == http.StatusOK })
	eventually(t, "a few failed polls", func() bool {
		_, body := status(t, base, "/status")
		devs, _ := body["devices"].([]any)
		if len(devs) != 1 {
			return false
		}
		n, _ := devs[0].(map[string]any)["consecutive_failures"].(float64)
		return n >= 3
	})
	if c, _ := status(t, base, "/readyz"); c != http.StatusServiceUnavailable {
		t.Fatalf("/readyz = %d with no device answering, want 503", c)
	}
}
