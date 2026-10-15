package openits

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
	"unicode"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	commonv1 "github.com/Vikasa2M/openits-models/pkg/proto/openits/common/v1"
	rlv1 "github.com/Vikasa2M/openits-models/pkg/proto/openits/reversible_lane/v1"

	"github.com/Vikasa2M/vikasa-collector/internal/synth"
	"github.com/Vikasa2M/vikasa-collector/internal/vendors/cameleon"
	"github.com/Vikasa2M/vikasa-collector/sdk/model"
)

// TestScriptedBitFlipCoverage flips every defined gate-word bit through the
// ACS decoder and checks the CloudEvent the emitter produces. The block is
// synthetic. There is no PLC address in this test.
func TestScriptedBitFlipCoverage(t *testing.T) {
	base := uint16(0x0025) // auto, lock-open, closed
	cases := []struct {
		name string
		word uint16
	}{
		{"baseline", base},
		{"toggle-mode-bit0", base ^ (1 << 0)},
		{"toggle-mode-bit1", base ^ (1 << 1)},
		{"toggle-lock-open-bit2", base ^ (1 << 2)},
		{"toggle-lock-close-bit3", base ^ (1 << 3)},
		{"toggle-position-bit4", base ^ (1 << 4)},
		{"toggle-position-bit5", base ^ (1 << 5)},
		{"toggle-position-bit6", base ^ (1 << 6)},
		{"toggle-bit10-failed-close", base ^ (1 << 10)},
		{"toggle-bit11-failed-open", base ^ (1 << 11)},
		{"toggle-bit12-estop", base ^ (1 << 12)},
		{"toggle-bit13", base ^ (1 << 13)},
		{"toggle-bit14", base ^ (1 << 14)},
		{"mode-manual", (base &^ 0x3) | 2},
		{"position-closing", (base &^ 0x70) | (1 << 4)},
		{"position-opened", (base &^ 0x70) | (4 << 4)},
	}
	gates := []cameleon.GateWord{
		{ID: "WG-111", Kind: model.GateKindWarning, DSW: 101},
		{ID: "WG-112", Kind: model.GateKindWarning, DSW: 102},
		{ID: "WG-113", Kind: model.GateKindWarning, DSW: 103},
		{ID: "BG-114", Kind: model.GateKindBarrier, DSW: 104},
		{ID: "WG-119", Kind: model.GateKindWarning, DSW: 105},
		{ID: "WG-115", Kind: model.GateKindWarning, DSW: 106},
		{ID: "WG-117", Kind: model.GateKindWarning, DSW: 107},
		{ID: "WG-118", Kind: model.GateKindWarning, DSW: 108},
		{ID: "WG-121", Kind: model.GateKindWarning, DSW: 109},
		{ID: "WG-122", Kind: model.GateKindWarning, DSW: 110},
		{ID: "WG-123", Kind: model.GateKindWarning, DSW: 111},
		{ID: "WG-128", Kind: model.GateKindWarning, DSW: 112},
		{ID: "BG-124", Kind: model.GateKindBarrier, DSW: 113},
		{ID: "WG-125", Kind: model.GateKindWarning, DSW: 114},
		{ID: "WG-126", Kind: model.GateKindWarning, DSW: 115},
		{ID: "WG-127", Kind: model.GateKindWarning, DSW: 116},
		{ID: "WG-137", Kind: model.GateKindWarning, DSW: 117},
		{ID: "WG-136", Kind: model.GateKindWarning, DSW: 118},
		{ID: "WG-135", Kind: model.GateKindWarning, DSW: 119},
		{ID: "BG-134", Kind: model.GateKindBarrier, DSW: 120},
		{ID: "WG-133", Kind: model.GateKindWarning, DSW: 121},
		{ID: "WG-132", Kind: model.GateKindWarning, DSW: 122},
		{ID: "WG-131", Kind: model.GateKindWarning, DSW: 123},
		{ID: "WG-116", Kind: model.GateKindWarning, DSW: 124},
	}
	schemas := schemaDir(t)
	ref := map[string]string{}
	for _, c := range cases {
		line := flipLine(t, schemas, gates[0], base, c.word, c.name)
		ref[c.name] = strings.ReplaceAll(line, gates[0].ID, "GATE")
		t.Log(strings.TrimRight(line, "\n"))
		if strings.Contains(line, "schema=FAIL") || strings.Contains(line, "encode-error") {
			t.Errorf("%s", strings.TrimRight(line, "\n"))
		}
	}
	for _, g := range gates[1:] {
		for _, c := range cases {
			line := flipLine(t, schemas, g, base, c.word, c.name)
			got := strings.ReplaceAll(line, g.ID, "GATE")
			if got != ref[c.name] {
				t.Errorf("gate %s case %s differs:\n%s", g.ID, c.name, line)
			}
		}
	}
	cab := cameleon.CabinetWords{LFFault: 179, SPFault: 180, Door: 181, GateEstop: 182}
	for _, spec := range []struct {
		name string
		addr int
	}{
		{"cabinet-lf", 179},
		{"cabinet-sp", 180},
		{"cabinet-door", 181},
		{"cabinet-estop", 182},
	} {
		quiet := make([]uint16, 110)
		loud := make([]uint16, 110)
		quiet[gates[0].DSW-90] = base
		loud[gates[0].DSW-90] = base
		loud[spec.addr-90] = 1
		line := spec.name + " " + eventsOf(t, schemas, gates[0], cab, quiet, loud) + "\n"
		t.Log(strings.TrimRight(line, "\n"))
		if strings.Contains(line, "schema=FAIL") || strings.Contains(line, "encode-error") || strings.Contains(line, "events=none") {
			t.Errorf("%s", strings.TrimRight(line, "\n"))
		}
	}
}

func flipLine(t *testing.T, schemas string, g cameleon.GateWord, base, word uint16, name string) string {
	t.Helper()
	quiet := make([]uint16, 110)
	loud := make([]uint16, 110)
	quiet[g.DSW-90] = base
	loud[g.DSW-90] = word
	cab := cameleon.CabinetWords{LFFault: 179, SPFault: 180, Door: 181, GateEstop: 182}
	return name + " " + g.ID + " word=0x" + fmt.Sprintf("%04X", word) + " " + eventsOf(t, schemas, g, cab, quiet, loud) + "\n"
}

func eventsOf(t *testing.T, schemas string, g cameleon.GateWord, cab cameleon.CabinetWords, first, second []uint16) string {
	t.Helper()
	eng := synth.NewEngine(synth.NewGateDiffer(), synth.NewFaultDiffer())
	s0 := mustRead(t, g, cab, first)
	s1 := mustRead(t, g, cab, second)
	s0.DeviceKind, s1.DeviceKind = "acs", "acs"
	_ = eng.Apply(s0)
	evs := eng.Apply(s1)
	if len(evs) == 0 {
		return "events=none"
	}
	em := New("collector")
	var parts []string
	for _, ev := range evs {
		enc, ok, err := em.Encode(ev)
		if err != nil {
			parts = append(parts, "encode-error="+err.Error())
			continue
		}
		if !ok {
			parts = append(parts, "declined kind="+ev.EventKind())
			continue
		}
		parts = append(parts, enc.CEType+" "+keyFields(enc.CEType, enc.Data)+" schema="+validatePayload(schemas, enc.CEType, enc.Data))
	}
	return strings.Join(parts, " | ")
}

func mustRead(t *testing.T, g cameleon.GateWord, cab cameleon.CabinetWords, words []uint16) *model.Snapshot {
	t.Helper()
	s, err := cameleon.ReadBlock("acs-1", 90, words, []cameleon.GateWord{g}, cab)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func keyFields(ceType string, data []byte) string {
	switch ceType {
	case "openits.reversible-lane.gate-status-changed.v1":
		var m rlv1.GateStatusChanged
		if err := proto.Unmarshal(data, &m); err != nil {
			return err.Error()
		}
		return fmt.Sprintf("kind=%s gate=%s pos=%s prevPos=%s mode=%s prevMode=%s",
			m.GetKind(), m.GetGateId(), m.GetPosition(), m.GetPreviousPosition(), m.GetOperatingMode(), m.GetPreviousOperatingMode())
	case "openits.reversible-lane.fault-raised.v1":
		var m commonv1.FaultRaised
		if err := proto.Unmarshal(data, &m); err != nil {
			return err.Error()
		}
		return fmt.Sprintf("kind=%s fault=%s sev=%s component=%s/%s",
			m.GetKind(), m.GetFaultId(), m.GetSeverity(), m.GetComponent().GetComponentClass(), m.GetComponent().GetComponentId())
	default:
		return ceType
	}
}

// The emitter ships application/protobuf. proto3 omits a zero enum, so
// GATE_POSITION_UNKNOWN is absent from the bytes and comes back as the zero
// value on unmarshal. protojson does the same omission. The JSON schema
// requires position, so the projection fills that zero value before checking.
func validatePayload(schemas, ceType string, data []byte) string {
	var msg proto.Message
	var schemaFile string
	switch ceType {
	case "openits.reversible-lane.gate-status-changed.v1":
		msg = &rlv1.GateStatusChanged{}
		schemaFile = filepath.Join(schemas, "reversible_lane", "v1", "openits-reversible-lane-events.gate-status-changed.schema.json")
	case "openits.reversible-lane.fault-raised.v1":
		msg = &commonv1.FaultRaised{}
		schemaFile = filepath.Join(schemas, "common", "v1", "openits-common-fault-events.fault-raised.schema.json")
	default:
		return "FAIL no schema wired for " + ceType
	}
	if err := proto.Unmarshal(data, msg); err != nil {
		return err.Error()
	}
	raw, err := protojson.MarshalOptions{}.Marshal(msg)
	if err != nil {
		return err.Error()
	}
	var body any
	if err := json.Unmarshal(raw, &body); err != nil {
		return err.Error()
	}
	projected, _ := project(body).(map[string]any)
	if gs, ok := msg.(*rlv1.GateStatusChanged); ok {
		if _, present := projected["position"]; !present {
			projected["position"] = yangEnum(gs.GetPosition().String())
		}
	}
	schemaBytes, err := os.ReadFile(schemaFile)
	if err != nil {
		return err.Error()
	}
	var schema map[string]any
	if err := json.Unmarshal(schemaBytes, &schema); err != nil {
		return err.Error()
	}
	if errs := checkSchema(schema, schema, projected); len(errs) > 0 {
		return "FAIL " + strings.Join(errs, "; ")
	}
	return "pass"
}

func schemaDir(t *testing.T) string {
	t.Helper()
	out, err := exec.Command("go", "list", "-m", "-f", "{{.Dir}}", "github.com/Vikasa2M/openits-models").Output()
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Join(strings.TrimSpace(string(out)), "api", "proto", "openits")
}

func project(v any) any {
	switch x := v.(type) {
	case map[string]any:
		out := map[string]any{}
		for k, val := range x {
			out[camelToKebab(k)] = project(val)
		}
		return out
	case []any:
		for i := range x {
			x[i] = project(x[i])
		}
		return x
	case string:
		return yangEnum(x)
	default:
		return v
	}
}

func camelToKebab(s string) string {
	var b strings.Builder
	for i, r := range s {
		if unicode.IsUpper(r) {
			if i > 0 {
				b.WriteByte('-')
			}
			b.WriteRune(unicode.ToLower(r))
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func yangEnum(s string) string {
	switch s {
	case "GATE_POSITION_UNKNOWN":
		return "unknown"
	case "GATE_POSITION_CLOSING":
		return "closing"
	case "GATE_POSITION_CLOSED":
		return "closed"
	case "GATE_POSITION_OPENING":
		return "opening"
	case "GATE_POSITION_OPEN":
		return "open"
	case "GATE_POSITION_INTERMEDIATE":
		return "intermediate"
	case "FAULT_SEVERITY_INFO":
		return "info"
	case "FAULT_SEVERITY_MINOR":
		return "minor"
	case "FAULT_SEVERITY_WARNING":
		return "warning"
	case "FAULT_SEVERITY_MAJOR":
		return "major"
	case "FAULT_SEVERITY_CRITICAL":
		return "critical"
	default:
		return s
	}
}

func checkSchema(root, schema map[string]any, v any) []string {
	if ref, ok := schema["$ref"].(string); ok {
		return checkSchema(root, resolveRef(root, ref), v)
	}
	obj, ok := v.(map[string]any)
	if !ok {
		return []string{fmt.Sprintf("want object, got %T", v)}
	}
	var errs []string
	props, _ := schema["properties"].(map[string]any)
	if schema["additionalProperties"] == false {
		for k := range obj {
			if _, ok := props[k]; !ok {
				errs = append(errs, "unexpected "+k)
			}
		}
	}
	if req, ok := schema["required"].([]any); ok {
		for _, r := range req {
			name := r.(string)
			if _, ok := obj[name]; !ok {
				errs = append(errs, "missing "+name)
			}
		}
	}
	for k, val := range obj {
		ps, ok := props[k].(map[string]any)
		if !ok {
			continue
		}
		if ref, ok := ps["$ref"].(string); ok {
			ps = resolveRef(root, ref)
		}
		switch val.(type) {
		case map[string]any:
			errs = append(errs, checkSchema(root, ps, val)...)
			continue
		}
		if s, ok := val.(string); ok {
			if enum, ok := ps["enum"].([]any); ok {
				found := false
				for _, e := range enum {
					if e == s {
						found = true
					}
				}
				if !found {
					errs = append(errs, k+" enum "+s)
				}
			}
			if pat, ok := ps["pattern"].(string); ok {
				if !regexp.MustCompile(pat).MatchString(s) {
					errs = append(errs, k+" pattern")
				}
			}
			if n, ok := ps["minLength"].(float64); ok && len(s) < int(n) {
				errs = append(errs, k+" minLength")
			}
			if n, ok := ps["maxLength"].(float64); ok && len(s) > int(n) {
				errs = append(errs, k+" maxLength")
			}
			if ps["format"] == "date-time" {
				if _, err := time.Parse(time.RFC3339, s); err != nil {
					errs = append(errs, k+" date-time")
				}
			}
		}
	}
	return errs
}

func resolveRef(root map[string]any, ref string) map[string]any {
	parts := strings.Split(strings.TrimPrefix(ref, "#/"), "/")
	var cur any = root
	for _, p := range parts {
		cur = cur.(map[string]any)[p]
	}
	return cur.(map[string]any)
}
