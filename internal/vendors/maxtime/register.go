package maxtime

import (
	"fmt"
	"time"

	"github.com/Vikasa2M/vikasa-collector/sdk/adapter"
)

type httpConfig struct {
	baseURL          string
	timeout          time.Duration
	detectorChannels []uint32
	username         string
	password         string
}

// RegisterTo registers the special MAXTIME HTTP ASC adapter. Its connection
// block is independent from ntcip-asc:
//
//	connection:
//	  http:
//	    base_url: "http://controller/maxtime"
//	    timeout: "2s"
//	    username: "admin"
//	    password: "use-a-secret-store"
//	    detector_channels: [1, 2, 3]
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
		return &asc{
			deviceID:         deviceID,
			client:           client,
			now:              time.Now,
			detectorChannels: cfg.detectorChannels,
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
	return httpConfig{
		baseURL:          baseURL,
		timeout:          timeout,
		detectorChannels: channels,
		username:         username,
		password:         password,
	}, nil
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
