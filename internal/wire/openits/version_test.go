package openits

import (
	"slices"
	"testing"

	"github.com/Vikasa2M/vikasa-collector/internal/wire"
)

// config validates model_version against wire.SupportedModelVersions; this
// emitter must be on that list, or no config could ever select it.
func TestModelVersionIsSupported(t *testing.T) {
	if !slices.Contains(wire.SupportedModelVersions, ModelVersion) {
		t.Fatalf("ModelVersion %q is not in wire.SupportedModelVersions %v", ModelVersion, wire.SupportedModelVersions)
	}
}
