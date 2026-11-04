// Package httpfixture is the recorded-fixture file format for HTTP adapters
// (ADR 0008): which requests an adapter's Read made of a real device, what the
// device answered, and where the recording came from. The collector's
// record-fixture mode writes it; httpapitest.LoadFixture replays it in a
// golden test.
//
// A recording never holds a credential. Request headers and bodies are not
// recorded at all, and an exchange that is not a GET — a login, in practice —
// is passed through but not written: its response is a session token, and a
// replay does not need it, because the fake answers whatever the adapter asks
// next from the recorded GETs.
package httpfixture

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"os"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/Vikasa2M/vikasa-collector/sdk/transport/httpapi"
)

// Fixture is one recorded poll: every GET the adapter made, keyed by
// httpapi.Request.Key, with what the device answered.
type Fixture struct {
	Provenance Provenance          `json:"provenance"`
	Exchanges  map[string]Exchange `json:"exchanges"`
	// Unrecorded lists the keys of non-GET exchanges that were made but
	// deliberately not written (see the package doc).
	Unrecorded []string `json:"unrecorded,omitempty"`
	// Redacted lists keys whose recorded body was replaced before the file
	// was written, because it identified the deployment. Their bodies are
	// synthetic.
	Redacted []string `json:"redacted,omitempty"`
}

// Exchange is one answered request. Body is the response body verbatim when
// it is valid UTF-8 text, which a JSON or XML API always is.
type Exchange struct {
	Status      int    `json:"status"`
	ContentType string `json:"content_type,omitempty"`
	Body        string `json:"body"`
}

// Provenance says where a recording came from. Every field but Note is
// required, as for SNMP fixtures.
type Provenance struct {
	Adapter    string `json:"adapter"`  // registry key, e.g. "maxtime-asc"
	Make       string `json:"make"`     // device manufacturer
	Model      string `json:"model"`    // device model
	Firmware   string `json:"firmware"` // firmware version as the device reports it
	Scheme     string `json:"scheme"`   // "http" or "https", as dialled
	Recorded   string `json:"recorded"` // UTC date, YYYY-MM-DD
	RecordedBy string `json:"recorded_by"`
	Note       string `json:"note,omitempty"`
}

// Read loads and validates a fixture file.
func Read(path string) (Fixture, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Fixture{}, err
	}
	var f Fixture
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&f); err != nil {
		return Fixture{}, fmt.Errorf("%s: %w", path, err)
	}
	if err := f.Validate(); err != nil {
		return Fixture{}, fmt.Errorf("%s: %w", path, err)
	}
	return f, nil
}

// Write validates f and writes it to path, indented with sorted keys.
func Write(path string, f Fixture) error {
	if err := f.Validate(); err != nil {
		return err
	}
	out, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(out, '\n'), 0o644) //nolint:gosec // a fixture is meant to be committed and read
}

// Validate checks provenance is complete and every exchange is a GET.
func (f Fixture) Validate() error {
	p := f.Provenance
	var errs []error
	for name, v := range map[string]string{
		"adapter": p.Adapter, "make": p.Make, "model": p.Model, "firmware": p.Firmware,
		"scheme": p.Scheme, "recorded": p.Recorded, "recorded_by": p.RecordedBy,
	} {
		if strings.TrimSpace(v) == "" {
			errs = append(errs, fmt.Errorf("provenance.%s is required", name))
		}
	}
	if _, err := time.Parse(time.DateOnly, p.Recorded); p.Recorded != "" && err != nil {
		errs = append(errs, fmt.Errorf("provenance.recorded %q is not YYYY-MM-DD", p.Recorded))
	}
	for k, x := range f.Exchanges {
		if !strings.HasPrefix(k, http.MethodGet+" /") {
			errs = append(errs, fmt.Errorf("exchange %q: only GET exchanges are recorded", k))
		}
		if x.Status < 100 || x.Status > 599 {
			errs = append(errs, fmt.Errorf("exchange %q: status %d", k, x.Status))
		}
	}
	slices.SortFunc(errs, func(a, b error) int { return strings.Compare(a.Error(), b.Error()) })
	return errors.Join(errs...)
}

// Recorder is an httpapi.Client that passes every request through to a real
// one and remembers each GET and its answer.
type Recorder struct {
	inner httpapi.Client

	mu         sync.Mutex
	exchanges  map[string]Exchange
	unrecorded map[string]bool
}

// NewRecorder wraps c.
func NewRecorder(c httpapi.Client) *Recorder {
	return &Recorder{inner: c, exchanges: map[string]Exchange{}, unrecorded: map[string]bool{}}
}

var _ httpapi.Client = (*Recorder)(nil)

// Do passes the request through. A failed request records nothing.
func (r *Recorder) Do(ctx context.Context, req httpapi.Request) (httpapi.Response, error) {
	resp, err := r.inner.Do(ctx, req)
	if err != nil {
		return resp, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	k := req.Key()
	if !strings.HasPrefix(k, http.MethodGet+" ") {
		r.unrecorded[k] = true
		return resp, nil
	}
	if !utf8.Valid(resp.Body) {
		// Not a text API answer; record the status so the replay is honest
		// about the request having been made, with an empty body.
		r.exchanges[k] = Exchange{Status: resp.Status, ContentType: resp.Header.Get("Content-Type")}
		return resp, nil
	}
	r.exchanges[k] = Exchange{Status: resp.Status, ContentType: resp.Header.Get("Content-Type"), Body: string(resp.Body)}
	return resp, nil
}

func (r *Recorder) Close() error { return r.inner.Close() }

// Fixture returns what was recorded, with p as its provenance.
func (r *Recorder) Fixture(p Provenance) Fixture {
	r.mu.Lock()
	defer r.mu.Unlock()
	return Fixture{
		Provenance: p,
		Exchanges:  maps.Clone(r.exchanges),
		Unrecorded: slices.Sorted(maps.Keys(r.unrecorded)),
	}
}
