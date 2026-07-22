// Package publish writes CloudEvents to the cabinet-local JetStream.
// Local JetStream IS the durability story (WAN-down is invisible here);
// publish is must-succeed with bounded retry, never unbounded buffering.
package publish

import (
	"context"
	"encoding/json"
	"fmt"
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

// Connect dials NATS and ensures the cabinet stream exists. The stream's
// subject filter is derived from the template, so an operator's grammar and
// the stream that captures it can never disagree.
func Connect(ctx context.Context, url string, tmpl *subject.Template, streamName string) (*Publisher, error) {
	if tmpl == nil {
		return nil, fmt.Errorf("publish: subject template is required")
	}
	if streamName == "" {
		return nil, fmt.Errorf("publish: stream name is required")
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
	_, err = js.CreateOrUpdateStream(ctx, jetstream.StreamConfig{
		Name:       streamName,
		Subjects:   []string{tmpl.Binding()},
		Storage:    jetstream.FileStorage,
		Duplicates: dedupWindow,
	})
	if err != nil {
		nc.Close()
		return nil, fmt.Errorf("ensure stream: %w", err)
	}
	return &Publisher{nc: nc, js: js, tmpl: tmpl}, nil
}

// Publish writes one envelope with Nats-Msg-Id = envelope ID for dedup.
func (p *Publisher) Publish(ctx context.Context, env cloudevents.Envelope, ceType string) error {
	subj, err := p.tmpl.Render(ceType)
	if err != nil {
		return err
	}
	data, err := json.Marshal(env)
	if err != nil {
		return fmt.Errorf("marshal envelope: %w", err)
	}
	msg := &nats.Msg{Subject: subj, Data: data}
	msg.Header = nats.Header{}
	msg.Header.Set("Nats-Msg-Id", env.ID)

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
