# P4a — Command Device-Type Cross-Check (C2 safety fix) Implementation Plan


**Goal:** Close the C2 safety hole — a command routed by `cmd.DeviceType` currently executes against the device resolved by `cmd.DeviceId` with no check that the two agree, so e.g. an ASC command can write NTCIP-1202 OIDs to an RSU. Enforce that the resolved device's real kind matches the handler's declared `DeviceType()` before executing, failing closed.

**Architecture:** The dispatcher (`internal/command/handler.go`) resolves `dev` (by `cmd.DeviceId`) and `svc` (by `cmd.DeviceType`) independently, then calls `svc.Execute(ctx, cmd, dev)`. Insert a cross-check between routing and execute: map `dev.Config.DeviceKind` (populated for every device since P1d) to a `pb.DeviceType` and require it to equal `svc.DeviceType()` (the interface method that exists for exactly this and is currently never called). Unknown/empty kinds match nothing → command rejected. This is P4a of the vendor-adapter design; the full command-into-`Commander`-adapter migration (P4b) is separate.

**Tech Stack:** Go 1.26, `github.com/nats-io/nats.go`, `google.golang.org/protobuf/proto`, stdlib `testing`.

## Global Constraints

- Module path `github.com/Vikasa2M/openits-collector`. Go 1.26. `go test ./...` resolves `openits-models` via `replace => ../openits-models`.
- **Fail closed:** a device whose `Config.DeviceKind` doesn't map to a known `pb.DeviceType` (empty/unknown) must be REJECTED, not executed. Better to refuse a command than to write to an unverified device.
- This tightens behavior: a command whose `DeviceType` disagrees with the target device's actual kind now fails where it previously executed. That is the intended safety change. All shipped/valid commands (correct DeviceType for the DeviceId) are unaffected.
- The check must run BEFORE `svc.Execute` — no device write may happen on a mismatch.
- This is the collector's first `internal/command` test. Commit after the task; `gofmt -s -w .` before committing.

---

### Task 1: Enforce the device-type cross-check + tests

**Files:**
- Modify: `internal/command/handler.go` (add `deviceKindToType` + `deviceIsType`; insert the check in `handleMessage` after `svc` is resolved)
- Modify: `internal/metrics/metrics.go` (add `CommandsDeviceTypeMismatchTotal`)
- Test: `internal/command/handler_test.go` (create)

**Interfaces:**
- Produces: `metrics.CommandsDeviceTypeMismatchTotal prometheus.Counter`. Internal: `deviceIsType(dev *device.Device, want pb.DeviceType) bool`.

- [ ] **Step 1: Add the metric.** In `internal/metrics/metrics.go`, find `CommandsSafetyRejectedTotal` and add an adjacent counter with the SAME `Namespace`/`Subsystem` (copy its opts, changing only Name/Help):

```go
	// CommandsDeviceTypeMismatchTotal counts commands rejected because the
	// resolved device's actual kind did not match the command's device type.
	CommandsDeviceTypeMismatchTotal = promauto.NewCounter(prometheus.CounterOpts{
		Namespace: "openits",
		Subsystem: "command", // MATCH the subsystem CommandsSafetyRejectedTotal uses
		Name:      "device_type_mismatch_rejected_total",
		Help:      "Total commands rejected due to device-type mismatch",
	})
```
> Verify the exact `Subsystem` value on `CommandsSafetyRejectedTotal` and use the same one so the metric groups with its siblings.

- [ ] **Step 2: Add the mapping + helper in `internal/command/handler.go`.** Near `deviceTypeToService` (line 29), add:

```go
// deviceKindToType maps the inventory device_kind token (on Device.Config)
// to its protobuf DeviceType. Unknown/empty kinds are absent, so the
// device-type cross-check fails closed for them.
var deviceKindToType = map[string]pb.DeviceType{
	"asc": pb.DeviceType_DEVICE_TYPE_ASC,
	"rsu": pb.DeviceType_DEVICE_TYPE_RSU,
}

// deviceIsType reports whether dev's actual device kind matches want. Fails
// closed: a device with an unknown/empty kind, or want UNSPECIFIED, matches
// nothing.
func deviceIsType(dev *device.Device, want pb.DeviceType) bool {
	if want == pb.DeviceType_DEVICE_TYPE_UNSPECIFIED {
		return false
	}
	return deviceKindToType[dev.Config.DeviceKind] == want
}
```

- [ ] **Step 3: Insert the check in `handleMessage`.** In `internal/command/handler.go`, immediately AFTER the `svc, ok := h.registry.Get(serviceName)` block (the one that returns UNSUPPORTED when `!ok`, ending ~line 246) and BEFORE the `// Execute` / `svc.Execute(...)` call (~line 248-249), insert:

```go
	// Device-type cross-check (SAFETY). The command is routed by
	// cmd.DeviceType but the device was resolved independently by
	// cmd.DeviceId. Refuse to execute if the resolved device is not actually
	// the type this handler commands — otherwise a command addressed (by
	// mistake or forgery) to a device of another kind would write the wrong
	// service's OIDs to it (e.g. ASC phase/flash writes to an RSU).
	if want := svc.DeviceType(); !deviceIsType(dev, want) {
		metrics.CommandsDeviceTypeMismatchTotal.Inc()
		h.logger.Warn("Rejected command: device type mismatch",
			"command_id", cmd.CommandId, "device_id", cmd.DeviceId,
			"command_device_type", cmd.DeviceType, "handler_device_type", want,
			"device_kind", dev.Config.DeviceKind)
		h.finishCommand(ctx, cmd, receivedAt, pb.CommandResult_COMMAND_RESULT_FAILED,
			fmt.Sprintf("device type mismatch: device %s is kind %q, not %s",
				cmd.DeviceId, dev.Config.DeviceKind, want), "")
		return
	}
```

- [ ] **Step 4: Write `internal/command/handler_test.go`.** Prove the safety property directly: a mismatched command must NOT reach `Execute`; a matching one must. Uses small in-package fakes.

```go
package command

import (
	"context"
	"iter"
	"testing"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"google.golang.org/protobuf/proto"

	"github.com/Vikasa2M/openits-collector/internal/config"
	"github.com/Vikasa2M/openits-collector/internal/device"
	inats "github.com/Vikasa2M/openits-collector/internal/nats"
	pb "github.com/Vikasa2M/openits-models/pkg/proto/openits/v1"
)

// --- fakes ---

type stubNATS struct{}

func (stubNATS) Connect(context.Context) error                                        { return nil }
func (stubNATS) Publish(context.Context, string, []byte, ...inats.PublishOption) error { return nil }
func (stubNATS) PublishMsg(context.Context, *nats.Msg, ...inats.PublishOption) error   { return nil }
func (stubNATS) Subscribe(context.Context, string, nats.MsgHandler) (*nats.Subscription, error) {
	return nil, nil
}
func (stubNATS) JetStream() jetstream.JetStream { return nil }
func (stubNATS) IsConnected() bool              { return true }
func (stubNATS) Close() error                   { return nil }

// oneDeviceManager returns a single device with the given kind for any Get.
type oneDeviceManager struct{ dev *device.Device }

func (m oneDeviceManager) Add(config.DeviceConfig) error            { return nil }
func (m oneDeviceManager) Remove(string) error                     { return nil }
func (m oneDeviceManager) Get(string) (*device.Device, error)      { return m.dev, nil }
func (m oneDeviceManager) Devices() iter.Seq2[string, *device.Device] {
	return func(func(string, *device.Device) bool) {}
}
func (m oneDeviceManager) Count() int                                            { return 1 }
func (m oneDeviceManager) HealthSummary() (total, healthy, degraded, offline int) { return 1, 1, 0, 0 }
func (m oneDeviceManager) Close() error                                          { return nil }

// recordingHandler is a ServiceHandler that records whether Execute ran.
type recordingHandler struct {
	service    string
	deviceType pb.DeviceType
	executed   bool
}

func (h *recordingHandler) Service() string          { return h.service }
func (h *recordingHandler) DeviceType() pb.DeviceType { return h.deviceType }
func (h *recordingHandler) Execute(context.Context, *pb.DeviceCommand, *device.Device) (pb.CommandResult, string, error) {
	h.executed = true
	return pb.CommandResult_COMMAND_RESULT_SUCCESS, "", nil
}

func newHandlerFor(kind string, sh *recordingHandler) *Handler {
	reg := NewRegistry()
	reg.Register(sh)
	dev := &device.Device{Config: config.DeviceConfig{ID: "dev-001", DeviceKind: kind}}
	return NewHandler("poller-1", stubNATS{}, oneDeviceManager{dev: dev}, WithRegistry(reg))
}

func cmdMsg(t *testing.T, deviceType pb.DeviceType) *nats.Msg {
	t.Helper()
	data, err := proto.Marshal(&pb.DeviceCommand{
		CommandId:  "cmd-1",
		DeviceId:   "dev-001",
		DeviceType: deviceType,
	})
	if err != nil {
		t.Fatalf("marshal command: %v", err)
	}
	return &nats.Msg{Data: data}
}

// SAFETY: an ASC command targeting an RSU device must NOT reach Execute.
func TestHandleMessage_RejectsDeviceTypeMismatch(t *testing.T) {
	sh := &recordingHandler{service: "signal-control", deviceType: pb.DeviceType_DEVICE_TYPE_ASC}
	h := newHandlerFor("rsu", sh) // device is an RSU
	h.handleMessage(context.Background(), cmdMsg(t, pb.DeviceType_DEVICE_TYPE_ASC))
	if sh.executed {
		t.Fatal("SAFETY VIOLATION: Execute ran on a device-type mismatch (ASC command, RSU device)")
	}
}

// A matching command (ASC command, ASC device) executes normally.
func TestHandleMessage_ExecutesOnMatch(t *testing.T) {
	sh := &recordingHandler{service: "signal-control", deviceType: pb.DeviceType_DEVICE_TYPE_ASC}
	h := newHandlerFor("asc", sh)
	h.handleMessage(context.Background(), cmdMsg(t, pb.DeviceType_DEVICE_TYPE_ASC))
	if !sh.executed {
		t.Fatal("Execute did not run for a matching ASC command on an ASC device")
	}
}

// Fail-closed: a device with an empty/unknown kind is never commanded.
func TestHandleMessage_FailsClosedOnUnknownKind(t *testing.T) {
	sh := &recordingHandler{service: "signal-control", deviceType: pb.DeviceType_DEVICE_TYPE_ASC}
	h := newHandlerFor("", sh) // unknown device kind
	h.handleMessage(context.Background(), cmdMsg(t, pb.DeviceType_DEVICE_TYPE_ASC))
	if sh.executed {
		t.Fatal("SAFETY VIOLATION: Execute ran for a device with unknown kind (should fail closed)")
	}
}
```

> Verify before running: `WithRegistry` is an exported `command.Option` (used in `cmd/poller/main.go`); `NewHandler(pollerID, client, devices, opts...)` signature; and that a `pb.DeviceCommand` with only `CommandId`/`DeviceId`/`DeviceType` set passes `safetyvalidate.Command` (no oneof payload → no violations) so it reaches the cross-check. If an empty command is rejected earlier by safety validation, set a benign valid ASC payload on the command so it reaches routing, and note the adjustment. If the mismatch test still reaches `Execute`, that is a real failure of the fix — report it, do not weaken the test.

- [ ] **Step 5: Run tests + full suite.**

Run: `gofmt -s -w . && go test ./internal/command/... -v && go test ./...`
Expected: the three handler tests PASS (mismatch + unknown-kind reject without executing; match executes); full suite green.

- [ ] **Step 6: Commit.**

```bash
git add internal/command/handler.go internal/command/handler_test.go internal/metrics/metrics.go
git commit -m "fix(command): enforce device-type cross-check before execute (C2 safety)

Commands are routed by cmd.DeviceType but the device is resolved by
cmd.DeviceId; nothing verified the two agreed, so an ASC command could
write ASC OIDs to an RSU. Reject when the resolved device's kind does not
match the handler's DeviceType() (fail closed on unknown kinds). Makes the
previously-dead ServiceHandler.DeviceType() the live guard."
```

---

## Self-Review

**Spec coverage:** Closes C2 from the audit — the device-type cross-check runs between routing and execute, uses the now-populated `Device.Config.DeviceKind`, calls the previously-dead `svc.DeviceType()`, and fails closed. Tests prove the core safety invariant directly (a mismatch never reaches `Execute`) plus the fail-closed and happy-path cases. Deferred: full `Commander`-adapter migration (P4b); per-action bounds re-validation (S3 in the audit).

**Placeholder scan:** None. The one test-setup uncertainty (whether an empty command passes `safetyvalidate` to reach the check) has an explicit verify-and-adjust instruction with a concrete fallback (add a benign ASC payload).

**Type consistency:** `deviceIsType(dev *device.Device, want pb.DeviceType) bool`; `deviceKindToType map[string]pb.DeviceType`. The check reads `svc.DeviceType()` (`ServiceHandler` interface, `registry.go:40`) and `dev.Config.DeviceKind` (`config.DeviceConfig`, added P1d-1). `metrics.CommandsDeviceTypeMismatchTotal` is a `prometheus.Counter` matching the existing command-rejection counters. Test fakes implement `inats.NATSClient` (7 methods), `device.DeviceManager` (7 methods), and `command.ServiceHandler` (3 methods) with the exact signatures.
