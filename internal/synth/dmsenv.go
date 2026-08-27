package synth

import "github.com/Vikasa2M/vikasa-collector/sdk/model"

// NewDMSEnvironmentDiffer turns the dms-environment facet into a
// sign-status-report every poll — the signal differ's operational-report
// contract, not a transition differ. The facet is continuous readings
// (temperatures, ambient light); "did it change since last poll" is the
// wrong question for values that always drift, so the report rides the
// poll cadence and consumers do their own trending.
//
// The absence rule still does all its work here: a failed environment read
// means no facet, no Diff call, and no report this poll — a gap in the
// series, never a fabricated repeat of the last reading.
func NewDMSEnvironmentDiffer() Differ { return dmsEnvDiffer{} }

type dmsEnvDiffer struct{}

func (dmsEnvDiffer) Kind() model.Kind { return model.KindDMSEnvironment }

func (dmsEnvDiffer) Diff(prev, curr model.Facet, base model.Base) []model.Event {
	c := curr.(model.DMSEnvironment)
	return []model.Event{model.DMSSignStatusReport{Base: base, DMSEnvironment: c}}
}
