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
	"strings"
)

// DefaultTemplate reproduces the pre-template subject scheme exactly. Config
// that omits a subject block gets this, so existing deployments are unaffected.
const DefaultTemplate = "{prefix}.{agency}.{site}.{service}.{event}.{version}"

// DefaultPrefix is the {prefix} value assumed when config omits it.
const DefaultPrefix = "openits"

// Per-event placeholders vary per published event; instance-constants are
// fixed for the process lifetime. The split determines where the stream
// binding can be truncated (see Binding).
var perEventNames = map[string]bool{
	"service": true,
	"event":   true,
	"version": true,
}

// Reserved names may not be redefined in vars. An operator who redefined
// "service" would get subjects that disagree with their own ce-types, and it
// would surface as unroutable events rather than a config error.
var reservedNames = map[string]bool{
	"service": true, "event": true, "version": true,
	"agency": true, "site": true,
	// device_id is reserved but unsupported in v1: collector-level events
	// (collector-started) have no device, so any template using it could never
	// render a legal subject for them. Reserved so a future version can add it
	// without colliding with an operator's var of the same name.
	"device_id": true,
}

// Config is the operator's subject configuration.
type Config struct {
	Template string            // "" means DefaultTemplate
	Vars     map[string]string // operator-defined instance-constants
}

// token is one dot-separated element: either a literal or a single placeholder.
type token struct {
	literal     string // rendered value for literals and resolved constants
	perEventVar string // non-empty if this token is a per-event placeholder
}

// Template is a validated, ready-to-render subject template.
type Template struct {
	tokens  []token
	binding string
	raw     string
}

// New validates cfg and returns a Template. Every failure here is a boot
// failure by design: config is the trust boundary, and a subject typo must
// not surface at 3am as an unroutable event.
func New(cfg Config, agency, site string) (*Template, error) {
	raw := cfg.Template
	if raw == "" {
		raw = DefaultTemplate
	}

	// Validate agency and site parameters. isLegalToken is only the
	// subject-level floor (bars ".", "*", ">", and whitespace) — it permits
	// things like "/" and uppercase that would corrupt the CE `source` URI
	// (cloudevents.SourceFor embeds agency/site verbatim as //agency/site/...).
	// agency/site additionally must satisfy the stricter ^[a-z0-9][a-z0-9-]*$
	// rule enforced by cloudevents.Tenant.Validate, which config.Load runs
	// before ever calling subject.New. That makes config.Load the
	// authoritative gate for these two values: a *config.Config built without
	// going through config.Load bypasses the stricter check and could reach
	// here with an agency/site that passes isLegalToken but would corrupt
	// `source`. Deliberately not duplicating the stricter regex here — that
	// would couple this package to cloudevents for a rule it doesn't own.
	if !isLegalToken(agency) {
		return nil, fmt.Errorf("subject: agency = %q is not a legal NATS token (no %q, %q, %q or whitespace, and must be non-empty)", agency, ".", "*", ">")
	}
	if !isLegalToken(site) {
		return nil, fmt.Errorf("subject: site = %q is not a legal NATS token (no %q, %q, %q or whitespace, and must be non-empty)", site, ".", "*", ">")
	}

	// Resolve instance-constants: operator vars plus agency/site.
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
	vars["agency"] = agency
	vars["site"] = site

	toks, err := parseTokens(raw, vars)
	if err != nil {
		return nil, err
	}

	t := &Template{tokens: toks, raw: raw}
	b, err := deriveBinding(toks)
	if err != nil {
		return nil, err
	}
	t.binding = b
	return t, nil
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
		if name == "device_id" {
			return nil, fmt.Errorf("subject: {device_id} is not supported: collector-level events have no device, so such a template could never render a legal subject for them")
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

// Render produces the subject for one ce-type.
func (t *Template) Render(ceType string) (string, error) {
	service, event, version, err := decompose(ceType)
	if err != nil {
		return "", err
	}
	parts := make([]string, len(t.tokens))
	for i, tok := range t.tokens {
		switch tok.perEventVar {
		case "":
			parts[i] = tok.literal
		case "service":
			parts[i] = service
		case "event":
			parts[i] = event
		case "version":
			parts[i] = version
		}
	}
	return strings.Join(parts, "."), nil
}

// decompose splits a ce-type into its subject-relevant parts. The first token
// is the schema namespace (openits vs openits-collector) and is deliberately
// NOT a subject token: catalog and health events share a subject root, which is
// what the pre-template scheme did.
func decompose(ceType string) (service, event, version string, err error) {
	parts := strings.Split(ceType, ".")
	if len(parts) != 4 {
		return "", "", "", fmt.Errorf("subject: ce-type %q must be <namespace>.<service>.<event>.<version>", ceType)
	}
	for _, p := range parts {
		if p == "" {
			return "", "", "", fmt.Errorf("subject: ce-type %q has an empty token", ceType)
		}
	}
	return parts[1], parts[2], parts[3], nil
}

// isLegalToken reports whether s can appear as a single NATS subject token.
func isLegalToken(s string) bool {
	if s == "" {
		return false
	}
	return !strings.ContainsAny(s, ".*> \t\n\r")
}

// Binding is the JetStream stream subject filter that captures everything this
// template can render.
func (t *Template) Binding() string { return t.binding }

// ValidateCETypes renders every ce-type the collector can emit and checks each
// produces a legal subject inside the binding. Exhaustive rather than sampled:
// this is what turns a subject typo into a boot failure instead of a 3am
// unroutable event.
func (t *Template) ValidateCETypes(ceTypes []string) error {
	for _, ceType := range ceTypes {
		subj, err := t.Render(ceType)
		if err != nil {
			return fmt.Errorf("subject: template %q cannot render ce-type %q: %w", t.raw, ceType, err)
		}
		for _, tok := range strings.Split(subj, ".") {
			if !isLegalToken(tok) {
				return fmt.Errorf("subject: ce-type %q renders to %q, which has an illegal token %q", ceType, subj, tok)
			}
		}
		if !withinBinding(subj, t.binding) {
			return fmt.Errorf("subject: ce-type %q renders to %q, outside the stream binding %q", ceType, subj, t.binding)
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
