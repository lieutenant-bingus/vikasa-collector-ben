package app

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"google.golang.org/protobuf/proto"

	commonv1 "github.com/Vikasa2M/openits-models/pkg/proto/openits/common/v1"
	scv1 "github.com/Vikasa2M/openits-models/pkg/proto/openits/signal_control/v1"

	"github.com/Vikasa2M/vikasa-collector/internal/config"
	"github.com/Vikasa2M/vikasa-collector/internal/vendors/maxtime"
	"github.com/Vikasa2M/vikasa-collector/sdk/adapter"
)

func TestMaxtimeASCReachesJetStreamWithAuthenticatedMapping(t *testing.T) {
	values := loadMaxtimeFixture(t)
	var (
		loginCall   atomic.Int32
		opModeReads atomic.Int32
		volReads    atomic.Int32
	)
	controller := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/maxprofile/accounts/loginWithPassword":
			loginCall.Add(1)
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Errorf("read login body: %v", err)
			}
			decoded, err := url.QueryUnescape(string(body))
			if err != nil {
				t.Errorf("decode login body: %v", err)
			}
			var request struct {
				User struct {
					Username string `json:"username"`
				} `json:"user"`
				Password string `json:"password"`
			}
			if err := json.Unmarshal([]byte(decoded), &request); err != nil {
				t.Errorf("decode login JSON: %v", err)
			}
			if request.User.Username != "admin" || request.Password != "secret" {
				t.Errorf("login payload = %+v", request)
			}
			_, _ = w.Write([]byte(`{"data":{"tokens":{"accessToken":"test-token"}}}`))
		case r.URL.Path == "/maxtime/api/mibs/controllerOpMode":
			poll := opModeReads.Add(1)
			data := values["controllerOpMode"]
			if poll >= 2 {
				data = map[string]string{"1": "7"} // flash
			}
			serveMaxtimeMIB(w, "controllerOpMode", data, r)
		case r.URL.Path == "/maxtime/api/mibs/PatrnSta":
			data := values["PatrnSta"]
			if opModeReads.Load() >= 2 {
				data = map[string]string{"1": "3"}
			}
			serveMaxtimeMIB(w, "PatrnSta", data, r)
		case r.URL.Path == "/maxtime/api/mibs/preemptStatus":
			data := values["preemptStatus"]
			if opModeReads.Load() >= 2 {
				data = map[string]string{"1": "1"}
			}
			serveMaxtimeMIB(w, "preemptStatus", data, r)
		case r.URL.Path == "/maxtime/api/mibs/SAlarms":
			data := values["SAlarms"]
			if opModeReads.Load() >= 2 {
				data = map[string]string{"1": "128"} // critical bit
			}
			serveMaxtimeMIB(w, "SAlarms", data, r)
		case r.URL.Path == "/maxtime/api/mibs/Volume":
			data := values["Volume"]
			if volReads.Add(1) >= 2 {
				data = map[string]string{"1": "17"}
			}
			serveMaxtimeMIB(w, "Volume", data, r)
		default:
			name := path.Base(r.URL.Path)
			if !strings.HasPrefix(r.URL.Path, "/maxtime/api/mibs/") {
				http.NotFound(w, r)
				return
			}
			serveMaxtimeMIB(w, name, values[name], r)
		}
	}))
	defer controller.Close()

	ns, err := server.NewServer(&server.Options{Port: -1, JetStream: true, StoreDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	ns.Start()
	t.Cleanup(ns.Shutdown)
	if !ns.ReadyForConnections(5 * time.Second) {
		t.Fatal("nats not ready")
	}

	reg := adapter.NewRegistry()
	maxtime.RegisterTo(reg)
	cfg := &config.Config{
		CollectorID:  "maxtime-e2e-collector",
		Region:       "us-ga",
		Agency:       "metro",
		AgencyUnit:   "d01",
		Site:         "cab-1",
		ModelVersion: "openits/v1",
		Devices: []config.Device{{
			ID:           "maxtime-asc-1",
			Vendor:       "maxtime",
			DeviceKind:   "asc",
			PollInterval: 40 * time.Millisecond,
			Connection: map[string]any{"http": map[string]any{
				"base_url":          controller.URL + "/maxtime",
				"username":          "admin",
				"password":          "secret",
				"detector_channels": []any{1},
			}},
		}},
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := Run(ctx, cfg, reg, ns.ClientURL(), "test"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if loginCall.Load() != 1 {
		t.Fatalf("login calls = %d, want one cached login", loginCall.Load())
	}

	nc, err := nats.Connect(ns.ClientURL())
	if err != nil {
		t.Fatal(err)
	}
	defer nc.Close()
	js, err := jetstream.New(nc)
	if err != nil {
		t.Fatal(err)
	}
	stream, err := js.Stream(context.Background(), "OPENITS-US-GA-METRO-D01")
	if err != nil {
		t.Fatalf("signal-control stream: %v", err)
	}
	info, err := stream.Info(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if info.State.Msgs < 6 {
		t.Fatalf("JetStream stored %d messages, want mapped ASC events", info.State.Msgs)
	}
	consumer, err := stream.CreateOrUpdateConsumer(context.Background(),
		jetstream.ConsumerConfig{Durable: "maxtime-e2e"})
	if err != nil {
		t.Fatal(err)
	}

	var (
		sawOperational bool
		sawPlan        bool
		sawDetector    bool
		sawMode        bool
		sawPreempt     bool
		sawFault       bool
	)
	for i := uint64(0); i < info.State.Msgs; i++ {
		msg, err := consumer.Next(jetstream.FetchMaxWait(500 * time.Millisecond))
		if err != nil {
			t.Fatalf("read JetStream message %d: %v", i, err)
		}
		_ = msg.Ack()
		switch msg.Headers().Get("ce-type") {
		case "openits.signal-control.operational-status-report.v1":
			var report scv1.OperationalStatusReport
			if err := proto.Unmarshal(msg.Data(), &report); err != nil {
				t.Fatalf("decode operational report: %v", err)
			}
			if report.GetMode() == "openits-signal-control-types:mode-free" && !report.GetFlashActive() {
				sawOperational = true
			}
		case "openits.signal-control.mode-changed.v1":
			sawMode = true
		case "openits.signal-control.plan-applied.v1":
			var plan scv1.PlanApplied
			if err := proto.Unmarshal(msg.Data(), &plan); err != nil {
				t.Fatalf("decode plan report: %v", err)
			}
			if plan.GetPlanId() == 3 {
				sawPlan = true
			}
		case "openits.signal-control.preemption-activated.v1":
			var preempt scv1.PreemptionActivated
			if err := proto.Unmarshal(msg.Data(), &preempt); err != nil {
				t.Fatalf("decode preemption: %v", err)
			}
			if preempt.GetSourceId() == "preempt-1" {
				sawPreempt = true
			}
		case "openits.signal-control.fault-raised.v1":
			var fault commonv1.FaultRaised
			if err := proto.Unmarshal(msg.Data(), &fault); err != nil {
				t.Fatalf("decode fault: %v", err)
			}
			if fault.GetFaultId() == "short-alarm-critical" {
				sawFault = true
			}
		case "openits.signal-control.detector-report.v1":
			var report scv1.DetectorReport
			if err := proto.Unmarshal(msg.Data(), &report); err != nil {
				t.Fatalf("decode detector report: %v", err)
			}
			if len(report.GetDetector()) != 1 {
				t.Fatalf("wire detector count = %d, want 1", len(report.GetDetector()))
			}
			detector := report.GetDetector()[0]
			if detector.GetDetectorId() == 1 && detector.GetVolume() == 17 && detector.GetOccupancy() == "0.0" {
				sawDetector = true
			}
		}
	}
	if !sawOperational || !sawPlan || !sawDetector || !sawMode || !sawPreempt || !sawFault {
		t.Fatalf("JetStream coverage: operational=%v mode=%v plan=%v preempt=%v fault=%v detector=%v op_reads=%d vol_reads=%d",
			sawOperational, sawMode, sawPlan, sawPreempt, sawFault, sawDetector, opModeReads.Load(), volReads.Load())
	}
}

func loadMaxtimeFixture(t *testing.T) map[string]map[string]string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "vendors", "maxtime", "testdata", "live", "healthy.json"))
	if err != nil {
		t.Fatalf("read Maxtime fixture: %v", err)
	}
	var records map[string]json.RawMessage
	if err := json.Unmarshal(raw, &records); err != nil {
		t.Fatalf("decode Maxtime fixture: %v", err)
	}
	out := make(map[string]map[string]string, len(records))
	for name, rawRecords := range records {
		var rows []struct {
			Data map[string]string `json:"data"`
		}
		if err := json.Unmarshal(rawRecords, &rows); err != nil {
			t.Fatalf("decode %s fixture: %v", name, err)
		}
		if len(rows) != 1 {
			t.Fatalf("%s fixture rows = %d, want 1", name, len(rows))
		}
		out[name] = rows[0].Data
	}
	return out
}

func serveMaxtimeMIB(w http.ResponseWriter, name string, data map[string]string, r *http.Request) {
	if r.Header.Get("accounts-access-token") != "test-token" {
		http.Error(w, "missing auth", http.StatusUnauthorized)
		return
	}
	if data == nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	body, _ := json.Marshal([]struct {
		Name string            `json:"name"`
		Data map[string]string `json:"data"`
	}{{Name: name, Data: data}})
	_, _ = w.Write(body)
}
