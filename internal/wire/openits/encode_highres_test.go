package openits

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"

	scv1 "github.com/Vikasa2M/openits-models/pkg/proto/openits/signal_control/v1"

	"github.com/Vikasa2M/vikasa-collector/sdk/model"
)

// The Indiana table is generated from the pinned YANG; this keeps it so. Every
// identity whose description cites "Indiana N" or "Indiana N-M" must be in
// the table at exactly those codes with its declared base, and the table may
// hold nothing else.
func TestIndianaTableMatchesPinnedYANG(t *testing.T) {
	out, err := exec.CommandContext(t.Context(), "go", "list", "-m", "-f", "{{.Dir}}", modelsModule).Output()
	if err != nil {
		t.Fatalf("locate %s: %v", modelsModule, err)
	}
	raw, err := os.ReadFile(filepath.Join(strings.TrimSpace(string(out)), "yang", "openits-signal-control-types.yang"))
	if err != nil {
		t.Fatal(err)
	}
	block := regexp.MustCompile(`(?s)identity\s+([a-z0-9-]+)\s*\{\s*base\s+([a-z0-9-]+);\s*description\s*"\s*Indiana\s+(\d+)(?:-(\d+))?:`)
	want := map[uint16]struct{ name, base string }{}
	for _, m := range block.FindAllStringSubmatch(string(raw), -1) {
		lo, _ := strconv.Atoi(m[3])
		hi := lo
		if m[4] != "" {
			hi, _ = strconv.Atoi(m[4])
		}
		for c := lo; c <= hi; c++ {
			if prev, dup := want[uint16(c)]; dup {
				t.Fatalf("Indiana %d is cited by both %s and %s", c, prev.name, m[1])
			}
			want[uint16(c)] = struct{ name, base string }{m[1], m[2]}
		}
	}
	if len(want) < 90 {
		t.Fatalf("found only %d Indiana codes; the YANG description pattern no longer matches", len(want))
	}
	for c, w := range want {
		if got, ok := indianaIdentity[c]; !ok || got != w {
			t.Errorf("Indiana %d: table has %+v, pinned YANG declares %+v", c, got, w)
		}
	}
	for c := range indianaIdentity {
		if _, ok := want[c]; !ok {
			t.Errorf("Indiana %d is in the table but no pinned identity cites it", c)
		}
	}
}

// Every code the domain routes to a family must encode under that family:
// the identity the catalog gives it must derive from the family's base. A
// mismatch would make encodeHighRes decline the row.
func TestHighResClassesMatchCatalog(t *testing.T) {
	for code := 0; code <= 255; code++ {
		ev := model.HighResEvent{Base: gbase("asc-1", "asc"), Code: uint16(code), Param: 1}
		class := ev.Class()
		if class == model.HighResUnmapped {
			continue
		}
		id, ok := indianaIdentity[uint16(code)]
		if !ok {
			t.Errorf("code %d is routed to class %d but the catalog names no identity for it", code, class)
			continue
		}
		if id.base != highResBase[class] {
			t.Errorf("code %d (%s) is routed to class %d, but its base is %s", code, id.name, class, id.base)
		}
		if _, ok, err := New("x", testEpoch).Encode(ev); err != nil || !ok {
			t.Errorf("code %d: Encode ok=%v err=%v", code, ok, err)
		}
	}
}

// The param lands on the leaf that names what it is, and nowhere else.
func TestHighResParamPlacement(t *testing.T) {
	enc := func(code, param uint16) proto.Message {
		t.Helper()
		e, ok, err := New("x", testEpoch).Encode(model.HighResEvent{Base: gbase("asc-1", "asc"), Code: code, Param: param})
		if err != nil || !ok {
			t.Fatalf("code %d: ok=%v err=%v", code, ok, err)
		}
		m, err := unmarshalFor(e.CEType, e.Data)
		if err != nil {
			t.Fatal(err)
		}
		return m
	}
	if m := enc(90, 3).(*scv1.PedestrianEvent); m.DetectorChannel == nil || *m.DetectorChannel != 3 || m.PhaseNumber != nil {
		t.Errorf("ped detector on: %v", m)
	}
	if m := enc(67, 2).(*scv1.PedestrianEvent); m.DetectorChannel != nil || m.PhaseNumber != nil {
		t.Errorf("ped overlap walk: the overlap has no leaf, got %v", m)
	}
	if m := enc(116, 4).(*scv1.PreemptionEvent); m.PreemptNumber != nil {
		t.Errorf("preempt force-off: the param is a phase, got %v", m)
	}
	if m := enc(1, 0).(*scv1.UnmappedEvent); m.GetSource().GetIndiana().GetIndianaCode() != 1 {
		t.Errorf("phase 0 is not a phase; want unmapped with the raw code, got %v", m)
	}
}
