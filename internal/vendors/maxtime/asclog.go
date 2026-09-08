package maxtime

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Vikasa2M/vikasa-collector/sdk/adapter"
	"github.com/Vikasa2M/vikasa-collector/sdk/model"
)

// asclog is the MAXTIME high-resolution controller log EventReader.
// It GETs /v1/asclog/xml/full, decodes Indiana EventTypeID records, and
// checkpoints on Log ID so a repeated full-window dump does not re-emit.
type asclog struct {
	deviceID string
	logURL   *url.URL
	client   *http.Client
	now      func() time.Time

	mu     sync.Mutex
	lastID uint64 // 0 means no checkpoint yet — first fetch is priming only
}

func newASCLog(deviceID, logURL string, timeout time.Duration) (*asclog, error) {
	parsed, err := url.Parse(strings.TrimSpace(logURL))
	if err != nil {
		return nil, fmt.Errorf("invalid asclog_url: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, fmt.Errorf("asclog_url must use http or https")
	}
	if parsed.Host == "" {
		return nil, fmt.Errorf("asclog_url host is required")
	}
	if timeout <= 0 {
		return nil, fmt.Errorf("timeout must be positive")
	}
	return &asclog{
		deviceID: deviceID,
		logURL:   parsed,
		client:   &http.Client{Timeout: timeout},
		now:      time.Now,
	}, nil
}

func (a *asclog) Descriptor() adapter.Descriptor {
	return adapter.Descriptor{Vendor: "maxtime", DeviceKind: "asc", Caps: adapter.CapEvents}
}

func (a *asclog) Close() error { return nil }

func (a *asclog) Fetch(ctx context.Context) ([]model.Event, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.logURL.String(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := a.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("GET %s: HTTP %s", a.logURL.Path, resp.Status)
	}
	records, err := parseASCLogXML(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("decode asclog: %w", err)
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	if a.lastID == 0 {
		// Prime the checkpoint from the newest id without emitting the whole
		// ring buffer on first contact — same spirit as detector differ's
		// first-poll silence.
		for _, rec := range records {
			if rec.ID > a.lastID {
				a.lastID = rec.ID
			}
		}
		return nil, nil
	}

	events := make([]model.Event, 0)
	var maxID uint64
	for _, rec := range records {
		if rec.ID > maxID {
			maxID = rec.ID
		}
		if rec.ID <= a.lastID {
			continue
		}
		base := model.Base{
			DeviceID:   a.deviceID,
			OccurredAt: rec.Time.UTC(),
		}
		events = append(events, decodeIndiana(rec.ID, rec.TypeID, rec.Parameter, base))
	}
	if maxID > a.lastID {
		a.lastID = maxID
	}
	return events, nil
}

type ascLogRecord struct {
	ID        uint64
	Time      time.Time
	TypeID    uint32
	Parameter uint32
}

type ascLogXML struct {
	XMLName xml.Name `xml:"EventResponses"`
	Events  []struct {
		ID        string `xml:"ID,attr"`
		TimeStamp string `xml:"TimeStamp,attr"`
		TypeID    string `xml:"EventTypeID,attr"`
		Parameter string `xml:"Parameter,attr"`
	} `xml:"EventResponse>Event"`
}

func parseASCLogXML(r io.Reader) ([]ascLogRecord, error) {
	var doc ascLogXML
	if err := xml.NewDecoder(r).Decode(&doc); err != nil {
		return nil, err
	}
	out := make([]ascLogRecord, 0, len(doc.Events))
	for _, ev := range doc.Events {
		id, err := strconv.ParseUint(ev.ID, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("event id %q: %w", ev.ID, err)
		}
		typeID, err := strconv.ParseUint(ev.TypeID, 10, 32)
		if err != nil {
			return nil, fmt.Errorf("event %d type %q: %w", id, ev.TypeID, err)
		}
		param, err := strconv.ParseUint(ev.Parameter, 10, 32)
		if err != nil {
			return nil, fmt.Errorf("event %d parameter %q: %w", id, ev.Parameter, err)
		}
		ts, err := parseASCLogTime(ev.TimeStamp)
		if err != nil {
			return nil, fmt.Errorf("event %d timestamp %q: %w", id, ev.TimeStamp, err)
		}
		out = append(out, ascLogRecord{
			ID: id, Time: ts, TypeID: uint32(typeID), Parameter: uint32(param),
		})
	}
	return out, nil
}

// parseASCLogTime parses MAXTIME's "MM-DD-YYYY HH:MM:SS.t" timestamps.
// Tenths are optional; the controller clock is treated as local wall time
// with location Local so OccurredAt can be normalized to UTC by the caller.
func parseASCLogTime(raw string) (time.Time, error) {
	raw = strings.TrimSpace(raw)
	layouts := []string{
		"01-02-2006 15:04:05.0",
		"01-02-2006 15:04:05",
	}
	var last error
	for _, layout := range layouts {
		ts, err := time.ParseInLocation(layout, raw, time.Local)
		if err == nil {
			return ts, nil
		}
		last = err
	}
	return time.Time{}, last
}
