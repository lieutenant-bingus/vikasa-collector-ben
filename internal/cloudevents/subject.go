package cloudevents

import (
	"fmt"
	"regexp"
)

// Tenant identifies the cabinet: stamped into subjects and the CE source URN.
//
// Region, Agency and AgencyUnit are the profile's identity triple and appear
// in ce-source. Site is NOT in the URN — it stays because the collector still
// uses it for stream naming and health context, but the profile's notion of
// "where" is the triple.
type Tenant struct {
	Region     string
	Agency     string
	AgencyUnit string
	Site       string
}

var tokenRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// Validate rejects tokens that could corrupt subject grammar or the source URN
// (dots, colons, wildcards, uppercase, empty). The URN is colon-delimited and
// the subject is dot-delimited, so a token containing either separator would
// silently change the shape of both rather than fail loudly.
func (t Tenant) Validate() error {
	for _, f := range []struct{ name, value string }{
		{"region", t.Region},
		{"agency", t.Agency},
		{"agency_unit", t.AgencyUnit},
		{"site", t.Site},
	} {
		if !tokenRe.MatchString(f.value) {
			return fmt.Errorf("invalid %s %q (need %s)", f.name, f.value, tokenRe)
		}
	}
	return nil
}

// entityKindFor maps a collector device kind to the profile's ce-source
// entity-kind. These are NOT the same vocabulary: the collector calls a signal
// controller "asc" and a sign "dms", while the profile names the entity
// "controller" and "sign".
//
// This is profile naming rather than schema, which is why it lives with the
// envelope instead of in internal/wire — no openits-models type is involved,
// only the URN convention the NATS binding documents.
//
// An unmapped kind returns false and the caller emits nothing rather than
// falling back to the raw kind: a URN asserting an entity type upstream does
// not define would be inherited by every consumer that parses ce-source.
func entityKindFor(deviceKind string) (string, bool) {
	switch deviceKind {
	case "asc":
		return "controller", true
	case "dms":
		return "sign", true
	case "cctv":
		return "cctv", true
	case "traffic-sensor":
		return "traffic-sensor", true
	case "perception":
		// A camera doing analytics is a perception entity, not a cctv one:
		// upstream's own conformance mock is
		// urn:openits:perception:...:eb-travel-lanes-cam-03. Entity kind
		// follows the event family, not the chassis, so one physical camera
		// can legitimately appear as both.
		return "perception", true
	case "":
		// Collector-level events (collector-started) have no device; the
		// collector itself is the entity.
		return "collector", true
	default:
		return "", false
	}
}

// SourceFor is the profile ce-source URN:
//
//	urn:openits:<entity-kind>:<region>:<agency>:<unit>:<id>
//
// Device-less events use the site as the id, since the collector is then the
// subject of its own event. Returns "" for a device kind with no entity-kind
// mapping, which the caller must treat as "do not publish".
//
// Note this is load-bearing beyond routing: ce-id hashes the literal bytes of
// this string, so any change to the format changes every event id.
func SourceFor(t Tenant, deviceKind, deviceID string) string {
	kind, ok := entityKindFor(deviceKind)
	if !ok {
		return ""
	}
	id := deviceID
	if id == "" {
		id = t.Site
	}
	return "urn:openits:" + kind + ":" + t.Region + ":" + t.Agency + ":" + t.AgencyUnit + ":" + id
}
