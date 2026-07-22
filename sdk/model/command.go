package model

// Command is the reserved write-back seam (ADR 0004). v1 is collect-only:
// these types exist so the Commander adapter capability compiles, but
// nothing dispatches them yet.
type Command interface{ CommandKind() string }

// SetPlan requests activation of a timing plan.
type SetPlan struct{ PlanID uint32 }

func (SetPlan) CommandKind() string { return "set-plan" }
