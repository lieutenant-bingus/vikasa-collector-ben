// Package subject renders NATS subjects from an operator-supplied template.
//
// Subject grammar belongs to the operator (ADR 0009): agencies fit the
// collector into namespaces they already own. The CloudEvent envelope does
// NOT — `type` stays catalog-verbatim (schema identity) and `source` stays
// canonical (fleet identity), so events remain interpretable regardless of
// local routing choices.
package subject

import (
	"fmt"
	"sort"
	"strings"
)

// DefaultTemplate is the OpenITS NATS profile's seven-token grammar, which the
// profile's conformance harness asserts by shape.
//
// There is deliberately no {version} token: the ce-type already carries the
// major version, and repeating it in the subject would be a second source of
// truth free to disagree with the first. {version} stays available to operator
// templates that want it.
//
// This replaced a five-token, tenant-first default. Changing it moves every
// subject AND the derived stream binding, which is only free because there are
// no deployments — see ADR 0009's consequences.
const DefaultTemplate = "{namespace}.{region}.{agency}.{agency_unit}.{service}.{device_id}.{event}"

// DefaultPrefix is the {prefix} value assumed when config omits it.
const DefaultPrefix = "openits"

// Per-event placeholders vary per published event; instance-constants are
// fixed for the process lifetime. The split determines where the stream
// binding can be truncated (see Binding).
var perEventNames = map[string]bool{
	"namespace": true,
	"service":   true,
	"event":     true,
	"version":   true,
	"device_id": true,
}

// Reserved names may not be redefined in vars. An operator who redefined
// "service" would get subjects that disagree with their own ce-types, and it
// would surface as unroutable events rather than a config error.
var reservedNames = map[string]bool{
	"namespace": true, "service": true, "event": true, "version": true,
	"region": true, "agency": true, "agency_unit": true, "site": true,
	// device_id is now a real per-event placeholder. It was reserved-but-
	// unsupported because collector-level events have no device and so could
	// never render a legal subject; rendering them as the literal "collector"
	// is what resolved that.
	"device_id": true,
}

// Config is the operator's subject configuration.
type Config struct {
	Template string            // "" means DefaultTemplate
	Vars     map[string]string // operator-defined instance-constants
}

// Identity is the instance-constant tenant identity available to templates.
type Identity struct {
	Region     string
	Agency     string
	AgencyUnit string
	Site       string
}

// deviceLessID is what {device_id} renders for events with no device. The
// collector is the subject of its own boot event, so naming it "collector"
// keeps the token count constant rather than producing a short subject that
// falls outside the binding.
const deviceLessID = "collector"

// token is one dot-separated element: either a literal or a single placeholder.
type token struct {
	literal     string // rendered value for literals and resolved constants
	perEventVar string // non-empty if this token is a per-event placeholder
}

// Template is a validated, ready-to-render subject template.
type Template struct {
	tokens []token
	raw    string
}

// New validates cfg and returns a Template. Every failure here is a boot
// failure by design: config is the trust boundary, and a subject typo must
// not surface at 3am as an unroutable event.
func New(cfg Config, id Identity) (*Template, error) {
	raw := cfg.Template
	if raw == "" {
		raw = DefaultTemplate
	}

	// isLegalToken is only the subject-level floor (bars ".", "*", ">", and
	// whitespace). It permits "/" and uppercase, which would be fine in a
	// subject but corrupt the CE source URN, so the identity tokens must ALSO
	// satisfy the stricter ^[a-z0-9][a-z0-9-]*$ rule that
	// cloudevents.Tenant.Validate enforces — which config.Load runs before
	// ever calling here. That makes config.Load the authoritative gate: a
	// *config.Config built without it bypasses the stricter check and could
	// reach here with a value that renders a legal subject and an unparseable
	// URN. Deliberately not duplicating the stricter regex, which would couple
	// this package to cloudevents for a rule it does not own.
	for _, f := range []struct{ name, value string }{
		{"region", id.Region}, {"agency", id.Agency},
		{"agency_unit", id.AgencyUnit}, {"site", id.Site},
	} {
		if !isLegalToken(f.value) {
			return nil, fmt.Errorf("subject: %s = %q is not a legal NATS token (no %q, %q, %q or whitespace, and must be non-empty)", f.name, f.value, ".", "*", ">")
		}
	}

	// Resolve instance-constants: operator vars plus the identity tokens.
	vars := make(map[string]string, len(cfg.Vars)+3)
	for k, v := range cfg.Vars {
		if reservedNames[k] {
			return nil, fmt.Errorf("subject: vars may not redefine reserved name %q", k)
		}
		if !isLegalToken(v) {
			return nil, fmt.Errorf("subject: vars[%q] = %q is not a legal NATS token (no %q, %q, %q or whitespace, and must be non-empty)", k, v, ".", "*", ">")
		}
		vars[k] = v
	}
	// {prefix} is conventional rather than built-in; default it so the default
	// template works with no vars at all.
	if _, ok := vars["prefix"]; !ok {
		vars["prefix"] = DefaultPrefix
	}
	vars["region"] = id.Region
	vars["agency"] = id.Agency
	vars["agency_unit"] = id.AgencyUnit
	vars["site"] = id.Site

	toks, err := parseTokens(raw, vars)
	if err != nil {
		return nil, err
	}

	return &Template{tokens: toks, raw: raw}, nil
}

// parseTokens splits the template on "." and resolves each token. Each token is
// either a literal or exactly one whole-token placeholder — a placeholder may
// not share a token with other text, because the binding must be truncatable at
// a token boundary.
func parseTokens(raw string, vars map[string]string) ([]token, error) {
	if raw == "" {
		return nil, fmt.Errorf("subject: template is empty")
	}
	parts := strings.Split(raw, ".")
	out := make([]token, 0, len(parts))
	for _, p := range parts {
		if p == "" {
			return nil, fmt.Errorf("subject: template %q has an empty token", raw)
		}
		opens := strings.Count(p, "{")
		closes := strings.Count(p, "}")
		if opens != closes {
			return nil, fmt.Errorf("subject: template %q has unbalanced braces in token %q", raw, p)
		}
		if opens == 0 {
			if !isLegalToken(p) {
				return nil, fmt.Errorf("subject: template %q has illegal literal token %q", raw, p)
			}
			out = append(out, token{literal: p})
			continue
		}
		// A placeholder sharing a token with other text leaves the binding no
		// boundary to truncate on (see deriveBinding).
		if opens > 1 || !strings.HasPrefix(p, "{") || !strings.HasSuffix(p, "}") {
			return nil, fmt.Errorf("subject: template %q token %q must be a literal or a single whole-token placeholder like {service}", raw, p)
		}
		name := p[1 : len(p)-1]
		if name == "" {
			return nil, fmt.Errorf("subject: template %q has an empty placeholder", raw)
		}
		if perEventNames[name] {
			out = append(out, token{perEventVar: name})
			continue
		}
		v, ok := vars[name]
		if !ok {
			return nil, fmt.Errorf("subject: template %q references {%s}, which is neither built-in (service, event, version) nor defined in vars", raw, name)
		}
		out = append(out, token{literal: v})
	}
	return out, nil
}

// Render produces the subject for one ce-type and device. An empty deviceID
// means a collector-level event and renders as deviceLessID.
func (t *Template) Render(ceType, deviceID string) (string, error) {
	namespace, service, event, version, err := decompose(ceType)
	if err != nil {
		return "", err
	}
	parts := make([]string, len(t.tokens))
	for i, tok := range t.tokens {
		switch tok.perEventVar {
		case "":
			parts[i] = tok.literal
		case "namespace":
			parts[i] = namespace
		case "service":
			parts[i] = service
		case "event":
			parts[i] = event
		case "version":
			parts[i] = version
		case "device_id":
			id := deviceID
			if id == "" {
				id = deviceLessID
			}
			// Checked HERE, before substitution, not on the joined subject.
			// A device id containing a dot would merge into the rendered
			// string as two perfectly legal tokens — the subject would just
			// silently gain a token, land outside the binding, and read as
			// valid to anything inspecting it afterwards.
			if !isLegalToken(id) {
				return "", fmt.Errorf("subject: device id %q is not a legal NATS token (no %q, %q, %q or whitespace)", id, ".", "*", ">")
			}
			parts[i] = id
		}
	}
	return strings.Join(parts, "."), nil
}

// decompose splits a ce-type into its subject-relevant parts. The first token
// is the schema namespace — `openits` for catalog events, `openits-collector`
// for the collector-owned health schema (ADR 0007) — and it IS a subject
// token: it roots each family in its own space so they can carry different
// retention and different access control (ADR 0011).
//
// It was previously discarded, so both families shared one root. That choice
// was made for the pre-template scheme, before collector-internal traffic and
// ITS-domain traffic had any reason to diverge.
func decompose(ceType string) (namespace, service, event, version string, err error) {
	parts := strings.Split(ceType, ".")
	if len(parts) != 4 {
		return "", "", "", "", fmt.Errorf("subject: ce-type %q must be <namespace>.<service>.<event>.<version>", ceType)
	}
	for _, p := range parts {
		if p == "" {
			return "", "", "", "", fmt.Errorf("subject: ce-type %q has an empty token", ceType)
		}
	}
	return parts[0], parts[1], parts[2], parts[3], nil
}

// namespaceOf returns just the ce-type's subject root.
func namespaceOf(ceType string) (string, error) {
	ns, _, _, _, err := decompose(ceType)
	return ns, err
}

// isLegalToken reports whether s can appear as a single NATS subject token.
func isLegalToken(s string) bool {
	if s == "" {
		return false
	}
	return !strings.ContainsAny(s, ".*> \t\n\r")
}

// Bindings returns the JetStream subject filters that capture everything this
// template can render, one per distinct ce-type namespace, sorted.
//
// One filter per namespace rather than one overall: the namespace is the
// leftmost token and varies per event, so a single filter would have to be
// ">" — which would capture every subject on the server, including other
// tenants sharing the broker.
func (t *Template) Bindings(ceTypes []string) ([]string, error) {
	seen := make(map[string]bool)
	var out []string
	for _, ceType := range ceTypes {
		ns, err := namespaceOf(ceType)
		if err != nil {
			return nil, err
		}
		if seen[ns] {
			continue
		}
		seen[ns] = true
		b, err := t.bindingFor(ns)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	sort.Strings(out)
	return out, nil
}

// bindingFor substitutes one namespace and truncates at the first REMAINING
// per-event token. Substituting first is what lets the static-prefix guard
// still work now that the leftmost token is itself per-event.
func (t *Template) bindingFor(namespace string) (string, error) {
	var prefix []string
	for _, tok := range t.tokens {
		switch tok.perEventVar {
		case "":
			prefix = append(prefix, tok.literal)
		case "namespace":
			prefix = append(prefix, namespace)
		default:
			if len(prefix) == 0 {
				return "", fmt.Errorf("subject: template %q has no static prefix — its leftmost token varies per event, "+
					"so the stream binding would be \">\" and would capture every subject on the server. "+
					"Put constant tokens (the namespace, region, agency) leftmost", t.raw)
			}
			return strings.Join(prefix, ".") + ".>", nil
		}
	}
	if len(prefix) == 0 {
		return "", fmt.Errorf("subject: template %q rendered an empty binding", t.raw)
	}
	// No per-event tokens at all: every event renders the same subject. Legal,
	// if odd; bind it exactly rather than with a wildcard.
	return strings.Join(prefix, "."), nil
}

// ValidateCETypes renders every ce-type against every configured device and
// checks each produces a legal subject inside the binding. Exhaustive rather
// than sampled, across BOTH axes: this is what turns a subject typo — or a
// device id carrying a dot, which would silently add a token and land the
// event outside the binding — into a boot failure instead of a 3am unroutable
// event.
func (t *Template) ValidateCETypes(ceTypes []string, deviceIDs []string) error {
	// Device-less events are always possible (the boot event), so the empty id
	// is checked alongside every configured device.
	ids := append([]string{""}, deviceIDs...)
	for _, ceType := range ceTypes {
		for _, id := range ids {
			subj, err := t.Render(ceType, id)
			if err != nil {
				return fmt.Errorf("subject: template %q cannot render ce-type %q: %w", t.raw, ceType, err)
			}
			for _, tok := range strings.Split(subj, ".") {
				if !isLegalToken(tok) {
					return fmt.Errorf("subject: ce-type %q with device %q renders to %q, which has an illegal token %q", ceType, id, subj, tok)
				}
			}
			ns, err := namespaceOf(ceType)
			if err != nil {
				return err
			}
			binding, err := t.bindingFor(ns)
			if err != nil {
				return err
			}
			if !withinBinding(subj, binding) {
				return fmt.Errorf("subject: ce-type %q with device %q renders to %q, outside the stream binding %q", ceType, id, subj, binding)
			}
		}
	}
	return nil
}

// withinBinding reports whether subj would be captured by the binding filter.
func withinBinding(subj, binding string) bool {
	if strings.HasSuffix(binding, ".>") {
		return strings.HasPrefix(subj, strings.TrimSuffix(binding, ">"))
	}
	return subj == binding
}

// deriveBinding substitutes instance-constants and truncates at the first
// per-event token, because a JetStream stream needs a static subject filter.
func deriveBinding(toks []token) (string, error) {
	var prefix []string
	for _, tok := range toks {
		if tok.perEventVar != "" {
			break
		}
		prefix = append(prefix, tok.literal)
	}
	if len(prefix) == 0 {
		return "", fmt.Errorf("subject: template has no static prefix — its leftmost token varies per event, " +
			"so the stream binding would be \">\" and would capture every subject on the server. " +
			"Put constant tokens (agency, site, a prefix) leftmost")
	}
	if len(prefix) == len(toks) {
		// No per-event tokens at all: every event renders the same subject.
		// Legal, if odd; bind it exactly rather than with a wildcard.
		return strings.Join(prefix, "."), nil
	}
	return strings.Join(prefix, ".") + ".>", nil
}
