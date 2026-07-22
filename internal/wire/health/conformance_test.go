package health

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/Vikasa2M/vikasa-collector/sdk/model"
)

// asyncAPI is the slice of the document this test cares about.
type asyncAPI struct {
	Channels map[string]struct {
		Address string `yaml:"address"`
	} `yaml:"channels"`
	Components struct {
		Messages map[string]struct {
			ContentType string `yaml:"contentType"`
			Payload     struct {
				Required   []string       `yaml:"required"`
				Properties map[string]any `yaml:"properties"`
			} `yaml:"payload"`
		} `yaml:"messages"`
	} `yaml:"components"`
}

// Every health event the emitter can produce, one sample each. Paired with
// CETypes() below: the test asserts the two agree, so adding an event without
// updating both fails rather than silently escaping documentation.
var samples = []model.Event{
	model.DeviceStatusChanged{
		Base:      model.Base{DeviceID: "asc-1", OccurredAt: time.Now().UTC()},
		Reachable: false, Reason: "read timeout", ConsecutiveFailures: 2,
	},
	model.CollectorStarted{
		Base: model.Base{OccurredAt: time.Now().UTC()}, Version: "dev",
	},
}

func loadAsyncAPI(t *testing.T) asyncAPI {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "asyncapi.yaml"))
	if err != nil {
		t.Fatalf("read asyncapi.yaml: %v", err)
	}
	var doc asyncAPI
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse asyncapi.yaml: %v", err)
	}
	return doc
}

// CETypes is the emitter's own declaration of what it can produce; the samples
// are what it actually produces. Drift between them means one of the two is
// lying, and boot validation trusts CETypes.
func TestCETypesMatchesWhatTheEmitterActuallyEmits(t *testing.T) {
	em := NewHealthEmitter()
	declared := em.CETypes()

	var actual []string
	for _, ev := range samples {
		enc, ok, err := em.Encode(ev)
		if err != nil || !ok {
			t.Fatalf("%s: emitter did not claim its own event (ok=%v err=%v)", ev.EventKind(), ok, err)
		}
		actual = append(actual, enc.CEType)
	}
	sort.Strings(actual)

	if !equal(declared, actual) {
		t.Errorf("CETypes() disagrees with emitted types\n  declared: %v\n  emitted:  %v", declared, actual)
	}
	if !sort.StringsAreSorted(declared) {
		t.Errorf("CETypes() must be sorted, got %v", declared)
	}
}

func TestAsyncAPICoversEveryEmittedType(t *testing.T) {
	doc := loadAsyncAPI(t)
	em := NewHealthEmitter()

	for _, ceType := range em.CETypes() {
		if _, documented := doc.Channels[ceType]; !documented {
			t.Errorf("ce-type %q is emittable but has no channel in asyncapi.yaml", ceType)
		}
		if _, documented := doc.Components.Messages[ceType]; !documented {
			t.Errorf("ce-type %q has no message definition in asyncapi.yaml", ceType)
		}
	}

	// Payload shape must match the bytes actually emitted.
	for _, ev := range samples {
		enc, ok, err := em.Encode(ev)
		if err != nil || !ok {
			t.Fatalf("%s: Encode ok=%v err=%v", ev.EventKind(), ok, err)
		}
		msg := doc.Components.Messages[enc.CEType]
		if msg.ContentType != enc.ContentType {
			t.Errorf("%s: asyncapi says contentType %q, emitter sends %q", enc.CEType, msg.ContentType, enc.ContentType)
		}
		var body map[string]any
		if err := json.Unmarshal(enc.Data, &body); err != nil {
			t.Fatalf("%s: emitted payload is not JSON: %v", enc.CEType, err)
		}
		emitted := keysOf(body)
		if !equal(emitted, keysOf(msg.Payload.Properties)) {
			t.Errorf("%s: payload drift\n  emitted:    %v\n  documented: %v", enc.CEType, emitted, keysOf(msg.Payload.Properties))
		}
		if !equal(emitted, sorted(msg.Payload.Required)) {
			t.Errorf("%s: every field is always emitted, so all must be required\n  emitted:  %v\n  required: %v", enc.CEType, emitted, sorted(msg.Payload.Required))
		}
	}
}

// A channel nobody can emit is a lie to consumers.
func TestAsyncAPIDocumentsNothingUnemittable(t *testing.T) {
	doc := loadAsyncAPI(t)
	emittable := map[string]bool{}
	for _, ceType := range NewHealthEmitter().CETypes() {
		emittable[ceType] = true
	}
	for ceType := range doc.Channels {
		if !emittable[ceType] {
			t.Errorf("asyncapi.yaml documents %q but no health event produces it", ceType)
		}
	}
}

func keysOf[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sorted(s []string) []string {
	out := append([]string(nil), s...)
	sort.Strings(out)
	return out
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
