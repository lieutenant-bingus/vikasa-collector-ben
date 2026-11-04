package maxtime

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/Vikasa2M/vikasa-collector/sdk/adapter"
	"github.com/Vikasa2M/vikasa-collector/sdk/model"
	"github.com/Vikasa2M/vikasa-collector/sdk/transport/httpapi"
)

// The high-resolution log ("ASC log") is MaxTime's ATSPM event log: every
// row an Indiana enumeration code, its parameter, and the controller's
// timestamp, kept in a ring of about ten thousand rows. It is read through
// the legacy XML endpoint
//
//	GET /v1/asclog/xml/full[?since=MM-DD-YYYY HH:MM:SS.T]
//	→ <EventResponses><EventResponse>
//	    <Event ID="3712593" TimeStamp="09-08-2026 09:59:27.8" EventTypeID="3" Parameter="7"/>
//	  ...
//
// ID increases by one per row the controller logs (gaps are rows filtered
// out of the response, not lost). It is the cursor's primary key; since only
// trims the download.

// defaultLogPath is where the lab controller answered on 2026-09-08. MaxTime's
// API notes place it under the MIB prefix on some builds
// (/maxtime/v1/asclog/...); asclog.path overrides it.
const defaultLogPath = "/v1/asclog/xml/full"

// maxEventsPerRead bounds one read. The ring holds ~10k rows; after a long
// outage the first read would otherwise hand the publisher all of them at
// once. The rest follow on the next interval from the advanced cursor.
const maxEventsPerRead = 2000

// sinceLayout is the controller's timestamp format, in its own local time.
const sinceLayout = "01-02-2006 15:04:05.0"

type logConfig struct {
	path     string
	interval time.Duration
	loc      *time.Location
	since    bool
}

// logCursor is what the core stores for this device between reads.
type logCursor struct {
	ID uint64    `json:"id"`
	At time.Time `json:"at"` // the row's instant, UTC
}

type logRow struct {
	id          uint64
	at          time.Time
	code, param uint16
}

// ascLog reads the high-resolution log. It shares the state poll's mibAPI,
// so it authenticates the same way, on the same session.
type ascLog struct {
	api *mibAPI
	cfg logConfig
	now func() time.Time
}

// ascWithLog is the adapter for a MaxTime whose high-resolution log is
// enabled: the state poll plus an EventReader.
type ascWithLog struct {
	*asc
	log *ascLog
}

var _ adapter.EventReader = (*ascWithLog)(nil)

func (a *ascWithLog) Descriptor() adapter.Descriptor {
	d := a.asc.Descriptor()
	d.Caps |= adapter.CapEvents
	return d
}

func (a *ascWithLog) EventInterval() time.Duration { return a.log.cfg.interval }

func (a *ascWithLog) ReadEvents(ctx context.Context, after []byte) ([]model.LogEvent, []byte, error) {
	return a.log.read(ctx, after)
}

func (l *ascLog) read(ctx context.Context, after []byte) ([]model.LogEvent, []byte, error) {
	var cur *logCursor
	if len(after) > 0 {
		var c logCursor
		// A cursor primed on an empty log has ID 0 and the prime's instant;
		// it is valid, and every row logged since has a higher ID.
		if err := json.Unmarshal(after, &c); err == nil && (c.ID > 0 || !c.At.IsZero()) {
			cur = &c
		}
		// An unreadable cursor is treated as none: prime, rather than fail
		// every read until someone clears the bucket.
	}

	req := httpapi.Request{Path: l.cfg.path}
	if cur != nil && l.cfg.since {
		req.Query = url.Values{"since": {l.sinceFor(cur.At)}}
	}
	resp, err := l.api.do(ctx, req)
	if err != nil {
		return nil, nil, err
	}
	if !resp.OK() {
		return nil, nil, fmt.Errorf("GET %s: HTTP %d", req.Path, resp.Status)
	}
	rows, err := decodeLog(resp.Body, l.cfg.loc)
	if err != nil {
		return nil, nil, fmt.Errorf("decode %s: %w", req.Path, err)
	}
	slices.SortFunc(rows, func(a, b logRow) int {
		switch {
		case a.id < b.id:
			return -1
		case a.id > b.id:
			return 1
		}
		return 0
	})

	if cur == nil {
		// First read: no backfill. The cursor starts at the log's end; when
		// the log is empty, at ID 0 and now, so the first rows it gains are
		// published.
		next := logCursor{At: l.now().UTC()}
		if n := len(rows); n > 0 {
			next = logCursor{ID: rows[n-1].id, At: rows[n-1].at}
		}
		return nil, encodeCursor(next), nil
	}

	fresh := newerThan(rows, *cur)
	if len(fresh) > maxEventsPerRead {
		fresh = fresh[:maxEventsPerRead]
	}
	if len(fresh) == 0 {
		return nil, after, nil
	}
	events := make([]model.LogEvent, len(fresh))
	for i, r := range fresh {
		events[i] = model.HighResEvent{Base: model.Base{OccurredAt: r.at}, Code: r.code, Param: r.param}
	}
	last := fresh[len(fresh)-1]
	return events, encodeCursor(logCursor{ID: last.id, At: last.at}), nil
}

// newerThan is the rows the cursor has not seen, oldest first. Normally that
// is every ID above the cursor's. If the log holds rows but none reaches the
// cursor's ID, the controller has renumbered — a reboot or a cleared log —
// and the rows logged after the cursor's instant are the new ones.
func newerThan(rows []logRow, cur logCursor) []logRow {
	if len(rows) == 0 {
		return nil
	}
	if rows[len(rows)-1].id >= cur.ID {
		i, _ := slices.BinarySearchFunc(rows, cur.ID+1, func(r logRow, id uint64) int {
			switch {
			case r.id < id:
				return -1
			case r.id > id:
				return 1
			}
			return 0
		})
		return rows[i:]
	}
	var out []logRow
	for _, r := range rows {
		if r.at.After(cur.At) {
			out = append(out, r)
		}
	}
	return out
}

// sinceFor formats the cursor's instant as the controller's wall clock.
// When the zone has stepped back since then (the autumn DST change), the
// controller relogs an hour of wall times it has already used; asking from
// the earlier wall time keeps that hour, and the ID filter drops the rows
// already published. A cursor older than any zone change (a long outage
// across it) asks an hour wider than it needs; that only costs download.
func (l *ascLog) sinceFor(at time.Time) string {
	_, then := at.In(l.cfg.loc).Zone()
	_, now := l.now().In(l.cfg.loc).Zone()
	wall := at.In(l.cfg.loc)
	if back := then - now; back > 0 {
		wall = wall.Add(-time.Duration(back) * time.Second)
	}
	// Truncated to the controller's tenths, never rounded up past the row.
	return wall.Truncate(100 * time.Millisecond).Format(sinceLayout)
}

func encodeCursor(c logCursor) []byte {
	b, _ := json.Marshal(c) // two fixed fields; cannot fail
	return b
}

// decodeLog parses the XML log. A row that does not parse is skipped, not
// fatal: failing the read would stop the cursor on it forever. The rows
// either side of it are still published, and the gap shows in their IDs.
func decodeLog(body []byte, loc *time.Location) ([]logRow, error) {
	var doc struct {
		XMLName xml.Name `xml:"EventResponses"`
		Events  []struct {
			ID        string `xml:"ID,attr"`
			TimeStamp string `xml:"TimeStamp,attr"`
			TypeID    string `xml:"EventTypeID,attr"`
			Parameter string `xml:"Parameter,attr"`
		} `xml:"EventResponse>Event"`
	}
	if err := xml.NewDecoder(bytes.NewReader(body)).Decode(&doc); err != nil {
		return nil, err
	}
	out := make([]logRow, 0, len(doc.Events))
	for _, e := range doc.Events {
		id, err1 := strconv.ParseUint(e.ID, 10, 64)
		code, err2 := strconv.ParseUint(e.TypeID, 10, 16)
		param, err3 := strconv.ParseUint(e.Parameter, 10, 16)
		at, err4 := parseLogTime(e.TimeStamp, loc)
		if err1 != nil || err2 != nil || err3 != nil || err4 != nil || id == 0 {
			continue
		}
		out = append(out, logRow{id: id, at: at, code: uint16(code), param: uint16(param)})
	}
	return out, nil
}

// parseLogTime reads "MM-DD-YYYY HH:MM:SS[.T]" in the controller's zone and
// returns the instant in UTC. In the repeated hour of a DST fall-back the
// wall time is ambiguous; Go picks one of the two, so OccurredAt can be an
// hour off for that hour. The cursor orders by ID and is unaffected.
func parseLogTime(raw string, loc *time.Location) (time.Time, error) {
	raw = strings.TrimSpace(raw)
	for _, layout := range []string{"01-02-2006 15:04:05.999999999", "01-02-2006 15:04:05"} {
		if t, err := time.ParseInLocation(layout, raw, loc); err == nil {
			return t.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("timestamp %q", raw)
}
