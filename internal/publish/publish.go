// Package publish writes CloudEvents to the cabinet-local JetStream.
// Local JetStream IS the durability story (WAN-down is invisible here);
// publish is must-succeed with bounded retry, never unbounded buffering.
package publish

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/Vikasa2M/vikasa-collector/internal/cloudevents"
	"github.com/Vikasa2M/vikasa-collector/internal/subject"
)

const (
	publishAttempts = 3
	publishBackoff  = 250 * time.Millisecond
	dedupWindow     = 2 * time.Minute
)

// Publisher owns the NATS connection and the cabinet stream.
type Publisher struct {
	nc   *nats.Conn
	js   jetstream.JetStream
	tmpl *subject.Template
}

// StreamNameForBinding derives a stream name from its subject filter, so the
// two cannot disagree: uppercased, dots to dashes, trailing wildcard dropped.
// "openits.us-ga.metro.d01.>" becomes "OPENITS-US-GA-METRO-D01".
//
// Derived rather than configured because with a stream per namespace a single
// configured name is meaningless, and a per-namespace config block would be a
// second source of truth free to disagree with the bindings it is supposed to
// name. An explicit override stays additive if an operator ever needs one.
func StreamNameForBinding(binding string) string {
	trimmed := strings.TrimSuffix(strings.TrimSuffix(binding, ">"), ".")
	return strings.ToUpper(strings.ReplaceAll(trimmed, ".", "-"))
}

// Connect dials NATS and ensures one stream per ce-type namespace. Subject
// filters are derived from the template, so an operator's grammar and the
// streams that capture it can never disagree.
//
// One stream per namespace, not one overall: collector-internal traffic and
// ITS-domain traffic want different retention and different subject
// permissions (ADR 0011), and a shared stream makes both inexpressible.
func Connect(ctx context.Context, url string, tmpl *subject.Template, ceTypes []string) (*Publisher, error) {
	if tmpl == nil {
		return nil, fmt.Errorf("publish: subject template is required")
	}
	bindings, err := tmpl.Bindings(ceTypes)
	if err != nil {
		return nil, err
	}
	if len(bindings) == 0 {
		return nil, fmt.Errorf("publish: no ce-types given, so there is no stream to provision")
	}
	nc, err := nats.Connect(url, nats.MaxReconnects(-1))
	if err != nil {
		return nil, fmt.Errorf("nats connect: %w", err)
	}
	js, err := jetstream.New(nc)
	if err != nil {
		nc.Close()
		return nil, err
	}
	for _, b := range bindings {
		if _, err := js.CreateOrUpdateStream(ctx, jetstream.StreamConfig{
			Name:       StreamNameForBinding(b),
			Subjects:   []string{b},
			Storage:    jetstream.FileStorage,
			Duplicates: dedupWindow,
		}); err != nil {
			nc.Close()
			return nil, fmt.Errorf("ensure stream for %s: %w", b, err)
		}
	}
	return &Publisher{nc: nc, js: js, tmpl: tmpl}, nil
}

// Headers renders an envelope as CloudEvents binary-mode headers: every
// attribute becomes a ce-* header and the body stays the raw encoded payload.
//
// Nats-Msg-Id carries the same value as ce-id so JetStream's own dedup window
// keys on the event identity rather than on a second, unrelated id.
//
// ce-dataschema is omitted entirely when empty rather than sent blank: the
// collector-owned health schema (ADR 0007) has no registry entry to point at,
// and an empty attribute would claim it does.
func Headers(env cloudevents.Envelope) nats.Header {
	h := nats.Header{}
	h.Set("ce-specversion", env.SpecVersion)
	h.Set("ce-id", env.ID)
	h.Set("ce-source", env.Source)
	h.Set("ce-type", env.Type)
	h.Set("ce-time", env.Time.UTC().Format(time.RFC3339Nano))
	h.Set("ce-datacontenttype", env.DataContentType)
	if env.DataSchema != "" {
		h.Set("ce-dataschema", env.DataSchema)
	}
	h.Set("Nats-Msg-Id", env.ID)
	return h
}

// Publish writes one envelope in CloudEvents binary mode.
func (p *Publisher) Publish(ctx context.Context, env cloudevents.Envelope, ceType, deviceID string) error {
	subj, err := p.tmpl.Render(ceType, deviceID)
	if err != nil {
		return err
	}
	msg := &nats.Msg{Subject: subj, Data: env.Data, Header: Headers(env)}

	var lastErr error
	for attempt := 0; attempt < publishAttempts; attempt++ {
		if attempt > 0 {
			select {
			case <-time.After(publishBackoff):
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		if _, lastErr = p.js.PublishMsg(ctx, msg); lastErr == nil {
			return nil
		}
	}
	return fmt.Errorf("publish %s after %d attempts: %w", subj, publishAttempts, lastErr)
}

// Close drains the connection.
func (p *Publisher) Close() { p.nc.Close() }
