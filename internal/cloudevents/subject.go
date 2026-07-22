package cloudevents

import (
	"fmt"
	"regexp"
)

// Tenant identifies the cabinet: stamped into subjects and CE source.
type Tenant struct {
	Agency string
	Site   string
}

var tokenRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// Validate rejects tenant tokens that could corrupt subject grammar
// (dots, wildcards, uppercase, empty).
func (t Tenant) Validate() error {
	if !tokenRe.MatchString(t.Agency) {
		return fmt.Errorf("invalid agency %q (need %s)", t.Agency, tokenRe)
	}
	if !tokenRe.MatchString(t.Site) {
		return fmt.Errorf("invalid site %q (need %s)", t.Site, tokenRe)
	}
	return nil
}

// SourceFor is the CE source URI-ref: //<agency>/<site>[/<device-id>].
// Empty deviceID means the collector itself.
func SourceFor(t Tenant, deviceID string) string {
	s := "//" + t.Agency + "/" + t.Site
	if deviceID != "" {
		s += "/" + deviceID
	}
	return s
}
