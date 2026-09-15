package maxtime

import (
	"context"
	"log/slog"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/Vikasa2M/vikasa-collector/internal/vendors/maxtime/j2735"
	"github.com/Vikasa2M/vikasa-collector/sdk/model"
)

const (
	mibMainStreet              = "MainStrt"
	mibSecondStreet            = "SecStrt"
	mibUnitDatabaseDescription = "unitDatabaseDescription"
	mibPreemptDescription      = "preemptDescription"

	mibIntGeoLatitude  = "intGeoLatitude"
	mibIntGeoLongitude = "intGeoLongitude"
	mibMapBinary       = "mapDataJ2735_2016_03_BinaryMessage"
	mibSpatBinary      = "spatJ2735_2016_03_BinaryMessage"
)

// DetectorInventory is optional per-channel geography used to enrich wire events.
type DetectorInventory struct {
	Approach    string
	Lane        string
	PhaseServed uint32
}

// Inventory holds site labels scraped from ASC (and optionally CV) MIBs,
// merged with any static per-channel map from config.
type Inventory struct {
	mu sync.RWMutex

	MainStreet   string
	SecondStreet string
	Description  string

	// PhaseApproach maps NEMA phase number → approach label (e.g. "NB").
	PhaseApproach map[uint32]string

	// Detectors maps channel → labels (config and/or future MAP decode).
	Detectors map[uint32]DetectorInventory

	// CV / geometry (optional).
	LatitudeE7  int64 // microdegrees (J2735-style ×1e7), 0 if unknown
	LongitudeE7 int64
	MapHex      string // UPER hex of MAP MessageFrame, empty if none
	SpatHex     string
	MapMsgID    int
	SpatMsgID   int
}

// ApproachForPhase returns the inventory approach label for a phase, if any.
func (inv *Inventory) ApproachForPhase(phase uint32) string {
	if inv == nil || phase == 0 {
		return ""
	}
	inv.mu.RLock()
	defer inv.mu.RUnlock()
	return inv.PhaseApproach[phase]
}

// LookupDetector returns enrichment for a channel, filling approach from
// phase→approach when the detector only has phase_served.
func (inv *Inventory) LookupDetector(channel uint32) DetectorInventory {
	if inv == nil || channel == 0 {
		return DetectorInventory{}
	}
	inv.mu.RLock()
	defer inv.mu.RUnlock()
	d := inv.Detectors[channel]
	if d.Approach == "" && d.PhaseServed != 0 {
		d.Approach = inv.PhaseApproach[d.PhaseServed]
	}
	return d
}

// Snapshot returns a shallow copy of scalar fields + maps for tests.
func (inv *Inventory) Snapshot() Inventory {
	if inv == nil {
		return Inventory{}
	}
	inv.mu.RLock()
	defer inv.mu.RUnlock()
	out := Inventory{
		MainStreet:   inv.MainStreet,
		SecondStreet: inv.SecondStreet,
		Description:  inv.Description,
		LatitudeE7:   inv.LatitudeE7,
		LongitudeE7:  inv.LongitudeE7,
		MapHex:       inv.MapHex,
		SpatHex:      inv.SpatHex,
		MapMsgID:     inv.MapMsgID,
		SpatMsgID:    inv.SpatMsgID,
	}
	if len(inv.PhaseApproach) > 0 {
		out.PhaseApproach = make(map[uint32]string, len(inv.PhaseApproach))
		for k, v := range inv.PhaseApproach {
			out.PhaseApproach[k] = v
		}
	}
	if len(inv.Detectors) > 0 {
		out.Detectors = make(map[uint32]DetectorInventory, len(inv.Detectors))
		for k, v := range inv.Detectors {
			out.Detectors[k] = v
		}
	}
	return out
}

func (inv *Inventory) setASC(main, second, desc string, phaseApproach map[uint32]string) {
	inv.mu.Lock()
	defer inv.mu.Unlock()
	inv.MainStreet = main
	inv.SecondStreet = second
	inv.Description = desc
	inv.PhaseApproach = phaseApproach
}

func (inv *Inventory) setCV(latE7, lonE7 int64, mapHex, spatHex string, mapID, spatID int) {
	inv.mu.Lock()
	defer inv.mu.Unlock()
	inv.LatitudeE7 = latE7
	inv.LongitudeE7 = lonE7
	if mapHex != "" {
		inv.MapHex = mapHex
		inv.MapMsgID = mapID
	}
	if spatHex != "" {
		inv.SpatHex = spatHex
		inv.SpatMsgID = spatID
	}
}

func (inv *Inventory) setDetectors(dets map[uint32]DetectorInventory) {
	inv.mu.Lock()
	defer inv.mu.Unlock()
	inv.Detectors = dets
}

// EnrichEvents stamps inventory onto detector events in place.
func (inv *Inventory) EnrichEvents(events []model.Event) {
	if inv == nil {
		return
	}
	for i, ev := range events {
		switch e := ev.(type) {
		case model.DetectorTransition:
			d := inv.LookupDetector(e.Channel)
			if d.PhaseServed != 0 && e.PhaseServed == 0 {
				e.PhaseServed = d.PhaseServed
			}
			if d.Approach != "" && e.Approach == "" {
				e.Approach = d.Approach
			}
			if d.Lane != "" && e.Lane == "" {
				e.Lane = d.Lane
			}
			if e.Approach == "" && e.PhaseServed != 0 {
				e.Approach = inv.ApproachForPhase(e.PhaseServed)
			}
			events[i] = e
		case model.DetectorReport:
			changed := false
			for j, r := range e.Readings {
				d := inv.LookupDetector(r.Channel)
				if d.PhaseServed != 0 && r.PhaseServed == 0 {
					e.Readings[j].PhaseServed = d.PhaseServed
					changed = true
				}
			}
			if changed {
				events[i] = e
			}
		}
	}
}

func refreshASCInventory(ctx context.Context, client mibReader, inv *Inventory) {
	if client == nil || inv == nil {
		return
	}
	main, _ := client.Get(ctx, mibMainStreet)
	sec, _ := client.Get(ctx, mibSecondStreet)
	desc, _ := client.Get(ctx, mibUnitDatabaseDescription)
	preempt, err := client.Get(ctx, mibPreemptDescription)
	if err != nil {
		slog.Debug("maxtime inventory: preemptDescription", "err", err)
	}
	inv.setASC(mibString(main), mibString(sec), mibString(desc), parsePreemptDescriptions(preempt))
}

func refreshCVInventory(ctx context.Context, client mibReader, inv *Inventory) {
	if client == nil || inv == nil {
		return
	}
	latVals, err := client.Get(ctx, mibIntGeoLatitude)
	if err != nil {
		slog.Debug("maxtime-cv inventory: latitude", "err", err)
		return
	}
	lonVals, err := client.Get(ctx, mibIntGeoLongitude)
	if err != nil {
		slog.Debug("maxtime-cv inventory: longitude", "err", err)
		return
	}
	latE7, _ := firstInt(latVals, mibIntGeoLatitude)
	lonE7, _ := firstInt(lonVals, mibIntGeoLongitude)

	mapHex, mapID := binaryMIBHex(ctx, client, mibMapBinary)
	spatHex, spatID := binaryMIBHex(ctx, client, mibSpatBinary)
	inv.setCV(latE7, lonE7, mapHex, spatHex, mapID, spatID)
}

func binaryMIBHex(ctx context.Context, client mibReader, name string) (hex string, msgID int) {
	vals, err := client.Get(ctx, name)
	if err != nil {
		slog.Debug("maxtime-cv inventory: binary mib", "mib", name, "err", err)
		return "", 0
	}
	raw := mibString(vals)
	if raw == "" {
		return "", 0
	}
	hex, err = j2735.MIBPayloadToHex(raw)
	if err != nil {
		slog.Debug("maxtime-cv inventory: unwrap", "mib", name, "err", err)
		return "", 0
	}
	if id, ok := j2735.PeekMessageIDHex(hex); ok {
		msgID = id
	}
	return hex, msgID
}

func mibString(values map[string]string) string {
	if len(values) == 0 {
		return ""
	}
	if raw, ok := values["1"]; ok {
		return decodeMIBString(raw)
	}
	for _, raw := range values {
		if s := decodeMIBString(raw); s != "" {
			return s
		}
	}
	return ""
}

func decodeMIBString(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if strings.Contains(raw, "%") {
		if decoded, err := url.QueryUnescape(raw); err == nil {
			return strings.TrimSpace(decoded)
		}
	}
	return raw
}

// preemptPhaseRe matches labels like "NB - Ph 1&6" or "EB - Ph 4&7".
var preemptPhaseRe = regexp.MustCompile(`(?i)^\s*([NSEW]{1,2}B(?:\s+[^-\d]+)?)\s*-\s*Ph\s*([\d&]+)\s*$`)

// parsePreemptDescriptions builds phase→approach from preemptDescription MIB values.
func parsePreemptDescriptions(values map[string]string) map[uint32]string {
	out := make(map[uint32]string)
	for _, raw := range values {
		s := decodeMIBString(raw)
		if s == "" {
			continue
		}
		if m := preemptPhaseRe.FindStringSubmatch(s); m != nil {
			approach := normalizeApproach(m[1])
			for _, tok := range strings.Split(m[2], "&") {
				tok = strings.TrimSpace(tok)
				n, err := strconv.ParseUint(tok, 10, 32)
				if err != nil || n == 0 {
					continue
				}
				out[uint32(n)] = approach
			}
			continue
		}
		parts := strings.SplitN(s, "-", 2)
		if len(parts) != 2 {
			continue
		}
		approach := strings.TrimSpace(parts[0])
		rest := strings.TrimSpace(parts[1])
		rest = strings.TrimPrefix(strings.ToLower(rest), "ph")
		rest = strings.TrimSpace(rest)
		for _, tok := range strings.FieldsFunc(rest, func(r rune) bool {
			return r == '&' || r == ',' || r == '/' || r == ' '
		}) {
			n, err := strconv.ParseUint(tok, 10, 32)
			if err != nil || n == 0 {
				continue
			}
			out[uint32(n)] = normalizeApproach(approach)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func normalizeApproach(s string) string {
	s = strings.TrimSpace(s)
	fields := strings.Fields(s)
	if len(fields) == 0 {
		return ""
	}
	head := strings.ToUpper(fields[0])
	switch head {
	case "NB", "SB", "EB", "WB", "NE", "NW", "SE", "SW":
		if len(fields) == 1 {
			return head
		}
		return head + " " + strings.Join(fields[1:], " ")
	default:
		return s
	}
}
