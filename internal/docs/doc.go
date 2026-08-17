// Package docs holds tests that keep documentation honest.
//
// Only STRUCTURAL claims are checked — a named enforcer that vanished, a
// config field that appeared, a subject address that drifted. Prose accuracy
// is a review problem and is deliberately not attempted here: a check that
// cannot fail on real decay is worse than no check, because it reads as
// coverage.
//
// It lives under internal/ because it imports internal/config and
// internal/subject. It ships no production code.
package docs
