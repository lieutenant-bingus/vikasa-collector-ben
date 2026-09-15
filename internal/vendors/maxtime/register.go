package maxtime

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Vikasa2M/vikasa-collector/sdk/adapter"
)

type httpConfig struct {
	baseURL          string
	cvBaseURL        string
	timeout          time.Duration
	detectorChannels []uint32
	username         string
	password         string
	asclogURL        string
	asclogEnabled    bool
	asclogInterval   time.Duration
	detectors        map[uint32]DetectorInventory
}

// RegisterTo registers the special MAXTIME HTTP ASC adapter. Its connection
// block is independent from ntcip-asc:
//
//	connection:
//	  http:
//	    base_url: "http://controller/maxtime"
//	    cv_base_url: "http://controller/maxtime-cv"  # optional MAP/SPaT/geo
//	    timeout: "2s"
//	    username: "admin"
//	    password: "use-a-secret-store"
//	    detector_channels: [1, 2, 3]
//	    asclog: true                         # optional; default true
//	    asclog_url: "http://controller/v1/asclog/xml/full"  # optional override
//	    asclog_poll_interval: "15s"          # optional; EventReader cadence
//	    inventory:                           # optional static detector map
//	      detectors:
//	        "3": { approach: "NB", lane: "NB thru", phase_served: 1 }
func RegisterTo(r *adapter.Registry) {
	r.Register(ascDescriptor, func(deviceID string, conn map[string]any) (adapter.Adapter, error) {
		cfg, err := parseHTTPBlock(conn)
		if err != nil {
			return nil, fmt.Errorf("maxtime-asc %s: %w", deviceID, err)
		}
		client, err := newHTTPMIBReader(cfg.baseURL, cfg.timeout, cfg.username, cfg.password)
		if err != nil {
			return nil, fmt.Errorf("maxtime-asc %s: %w", deviceID, err)
		}
		inv := &Inventory{}
		if len(cfg.detectors) > 0 {
			inv.setDetectors(cfg.detectors)
		}
		state := &asc{
			deviceID:         deviceID,
			client:           client,
			now:              time.Now,
			detectorChannels: cfg.detectorChannels,
			inv:              inv,
		}
		if cfg.cvBaseURL != "" {
			cv, err := newHTTPMIBReader(cfg.cvBaseURL, cfg.timeout, cfg.username, cfg.password)
			if err != nil {
				return nil, fmt.Errorf("maxtime-asc %s: cv_base_url: %w", deviceID, err)
			}
			state.cv = cv
		}
		if !cfg.asclogEnabled {
			// Return *asc alone so the type does not satisfy EventReader —
			// CapEvents and the Go interface assertion must agree.
			return state, nil
		}
		log, err := newASCLog(deviceID, cfg.asclogURL, cfg.timeout)
		if err != nil {
			return nil, fmt.Errorf("maxtime-asc %s: %w", deviceID, err)
		}
		return &device{
			asc: state, log: log, eventInterval: cfg.asclogInterval,
		}, nil
	})
}

func parseHTTPBlock(conn map[string]any) (httpConfig, error) {
	raw, ok := conn["http"].(map[string]any)
	if !ok {
		return httpConfig{}, fmt.Errorf("connection.http block required")
	}
	baseURL, _ := raw["base_url"].(string)
	if baseURL == "" {
		return httpConfig{}, fmt.Errorf("connection.http.base_url required")
	}
	cvBaseURL, _ := raw["cv_base_url"].(string)

	timeout := 2 * time.Second
	if rawTimeout, ok := raw["timeout"]; ok {
		timeoutValue, ok := rawTimeout.(string)
		if !ok {
			return httpConfig{}, fmt.Errorf("connection.http.timeout must be a duration string")
		}
		parsed, err := time.ParseDuration(timeoutValue)
		if err != nil {
			return httpConfig{}, fmt.Errorf("connection.http.timeout: %w", err)
		}
		timeout = parsed
	}
	if timeout <= 0 {
		return httpConfig{}, fmt.Errorf("connection.http.timeout must be positive")
	}

	rawUsername, usernamePresent := raw["username"]
	rawPassword, passwordPresent := raw["password"]
	username, usernameOK := rawUsername.(string)
	password, passwordOK := rawPassword.(string)
	if usernamePresent != passwordPresent ||
		(usernamePresent && (!usernameOK || !passwordOK || username == "" || password == "")) {
		return httpConfig{}, fmt.Errorf("connection.http.username and password must be provided together and non-empty")
	}

	channels, err := parseDetectorChannels(raw["detector_channels"])
	if err != nil {
		return httpConfig{}, err
	}

	asclogEnabled := true
	if rawASC, ok := raw["asclog"]; ok {
		enabled, ok := rawASC.(bool)
		if !ok {
			return httpConfig{}, fmt.Errorf("connection.http.asclog must be a boolean")
		}
		asclogEnabled = enabled
	}

	asclogURL, _ := raw["asclog_url"].(string)
	if asclogEnabled && asclogURL == "" {
		derived, err := deriveASCLogURL(baseURL)
		if err != nil {
			return httpConfig{}, err
		}
		asclogURL = derived
	}

	asclogInterval := 15 * time.Second
	if rawInterval, ok := raw["asclog_poll_interval"]; ok {
		intervalValue, ok := rawInterval.(string)
		if !ok {
			return httpConfig{}, fmt.Errorf("connection.http.asclog_poll_interval must be a duration string")
		}
		parsed, err := time.ParseDuration(intervalValue)
		if err != nil {
			return httpConfig{}, fmt.Errorf("connection.http.asclog_poll_interval: %w", err)
		}
		if parsed <= 0 {
			return httpConfig{}, fmt.Errorf("connection.http.asclog_poll_interval must be positive")
		}
		asclogInterval = parsed
	}

	dets, err := parseInventoryDetectors(raw["inventory"])
	if err != nil {
		return httpConfig{}, err
	}

	return httpConfig{
		baseURL:          baseURL,
		cvBaseURL:        cvBaseURL,
		timeout:          timeout,
		detectorChannels: channels,
		username:         username,
		password:         password,
		asclogURL:        asclogURL,
		asclogEnabled:    asclogEnabled,
		asclogInterval:   asclogInterval,
		detectors:        dets,
	}, nil
}

func deriveASCLogURL(baseURL string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil {
		return "", fmt.Errorf("connection.http.base_url: %w", err)
	}
	if parsed.Host == "" {
		return "", fmt.Errorf("connection.http.base_url host is required to derive asclog_url")
	}
	parsed.Path = "/v1/asclog/xml/full"
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String(), nil
}

func parseDetectorChannels(raw any) ([]uint32, error) {
	if raw == nil {
		return nil, nil
	}
	values, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("connection.http.detector_channels must be a list")
	}
	channels := make([]uint32, 0, len(values))
	seen := make(map[uint32]bool, len(values))
	for i, value := range values {
		var channel uint32
		switch v := value.(type) {
		case int:
			if v > 0 {
				channel = uint32(v)
			}
		case int64:
			if v > 0 && v <= int64(^uint32(0)) {
				channel = uint32(v)
			}
		case uint64:
			if v > 0 && v <= uint64(^uint32(0)) {
				channel = uint32(v)
			}
		case float64:
			if v > 0 && v == float64(uint32(v)) {
				channel = uint32(v)
			}
		}
		if channel == 0 {
			return nil, fmt.Errorf("connection.http.detector_channels[%d] must be a positive integer", i)
		}
		if !seen[channel] {
			channels = append(channels, channel)
			seen[channel] = true
		}
	}
	return channels, nil
}

func parseInventoryDetectors(raw any) (map[uint32]DetectorInventory, error) {
	if raw == nil {
		return nil, nil
	}
	block, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("connection.http.inventory must be a map")
	}
	rawDets, ok := block["detectors"]
	if !ok || rawDets == nil {
		return nil, nil
	}
	dets, ok := rawDets.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("connection.http.inventory.detectors must be a map")
	}
	out := make(map[uint32]DetectorInventory, len(dets))
	for key, value := range dets {
		ch, err := strconv.ParseUint(strings.TrimSpace(key), 10, 32)
		if err != nil || ch == 0 {
			return nil, fmt.Errorf("connection.http.inventory.detectors key %q must be a positive channel number", key)
		}
		entry, ok := value.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("connection.http.inventory.detectors.%s must be a map", key)
		}
		var d DetectorInventory
		if approach, _ := entry["approach"].(string); approach != "" {
			d.Approach = approach
		}
		if lane, _ := entry["lane"].(string); lane != "" {
			d.Lane = lane
		}
		if rawPhase, ok := entry["phase_served"]; ok {
			phase, err := positiveUint32(rawPhase)
			if err != nil {
				return nil, fmt.Errorf("connection.http.inventory.detectors.%s.phase_served: %w", key, err)
			}
			d.PhaseServed = phase
		}
		out[uint32(ch)] = d
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

func positiveUint32(raw any) (uint32, error) {
	switch v := raw.(type) {
	case int:
		if v > 0 {
			return uint32(v), nil
		}
	case int64:
		if v > 0 && v <= int64(^uint32(0)) {
			return uint32(v), nil
		}
	case uint64:
		if v > 0 && v <= uint64(^uint32(0)) {
			return uint32(v), nil
		}
	case float64:
		if v > 0 && v == float64(uint32(v)) {
			return uint32(v), nil
		}
	case string:
		n, err := strconv.ParseUint(strings.TrimSpace(v), 10, 32)
		if err == nil && n > 0 {
			return uint32(n), nil
		}
	}
	return 0, fmt.Errorf("must be a positive integer")
}
