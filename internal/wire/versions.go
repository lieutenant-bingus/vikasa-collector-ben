package wire

// SupportedModelVersions lists every collector.yaml model_version some
// catalog emitter compiled into this binary implements (ADR 0005: one
// catalog version per instance, chosen at boot). config validates against
// it, so an unknown value fails boot instead of selecting nothing.
//
// It lives here rather than in an emitter package so config can check it
// without importing openits-models. Each emitter package asserts its own
// version is listed, and internal/app asserts every listed version selects
// an emitter, so the three cannot drift.
var SupportedModelVersions = []string{"openits/v1"}
