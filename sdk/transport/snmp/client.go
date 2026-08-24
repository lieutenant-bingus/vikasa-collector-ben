// Package snmp is an OPTIONAL transport helper adapters may use. The core
// never imports it — transport is entirely the adapter's business.
package snmp

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gosnmp/gosnmp"
)

// Values is one GET's typed results. OIDs the agent does not answer are
// omitted from both maps — callers treat absence as a facet-level read
// failure, never as a zero value. An answered OCTET STRING of length 0 is
// present in Octets (empty), which is distinct from omission.
type Values struct {
	Ints   map[string]int64
	Octets map[string][]byte
}

// Client fetches OIDs. Get is the integer-only convenience the ASC adapter
// uses; GetAll also returns OCTET STRING payloads (MessageIDCode, MULTI).
type Client interface {
	Get(ctx context.Context, oids []string) (map[string]int64, error)
	GetAll(ctx context.Context, oids []string) (Values, error)
	Close() error
}

// DialConfig configures a UDP SNMP session. Version is "v1" or "v2c"; empty
// means v2c, which is what ntcip-asc has always spoken.
type DialConfig struct {
	Address   string // "host:port"
	Community string
	Timeout   time.Duration // default 2s
	Retries   int           // default 1
	Version   string        // "v1" or "v2c"; empty defaults to v2c
}

// Dial opens a gosnmp session. All I/O on the returned Client is
// mutex-serialized: one gosnmp connection must never see concurrent use.
func Dial(cfg DialConfig) (Client, error) {
	host, port, err := splitHostPort(cfg.Address)
	if err != nil {
		return nil, err
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 2 * time.Second
	}
	ver, err := ParseVersion(cfg.Version)
	if err != nil {
		return nil, err
	}
	g := &gosnmp.GoSNMP{
		Target:    host,
		Port:      port,
		Community: cfg.Community,
		Version:   ver,
		Timeout:   cfg.Timeout,
		Retries:   cfg.Retries,
	}
	if err := g.Connect(); err != nil {
		return nil, fmt.Errorf("snmp connect %s: %w", cfg.Address, err)
	}
	return &client{g: g}, nil
}

type client struct {
	mu sync.Mutex
	g  *gosnmp.GoSNMP
}

func (c *client) Get(ctx context.Context, oids []string) (map[string]int64, error) {
	all, err := c.GetAll(ctx, oids)
	if err != nil {
		return nil, err
	}
	return all.Ints, nil
}

func (c *client) GetAll(ctx context.Context, oids []string) (Values, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return Values{}, err
	}
	out := Values{
		Ints:   make(map[string]int64, len(oids)),
		Octets: make(map[string][]byte),
	}
	// gosnmp caps PDUs per request; chunk to stay portable across agents.
	const chunk = 16
	for i := 0; i < len(oids); i += chunk {
		end := min(i+chunk, len(oids))
		pkt, err := c.g.Get(oids[i:end])
		if err != nil {
			return Values{}, fmt.Errorf("snmp get: %w", err)
		}
		for _, pdu := range pkt.Variables {
			if v, ok := toInt64(pdu); ok {
				out.Ints[pdu.Name] = v
				continue
			}
			if v, ok := toOctet(pdu); ok {
				out.Octets[pdu.Name] = v
			}
		}
	}
	return out, nil
}

func (c *client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.g.Conn.Close()
}

func toInt64(pdu gosnmp.SnmpPDU) (int64, bool) {
	switch pdu.Type {
	case gosnmp.Integer, gosnmp.Counter32, gosnmp.Counter64, gosnmp.Gauge32, gosnmp.TimeTicks, gosnmp.Uinteger32:
		return gosnmp.ToBigInt(pdu.Value).Int64(), true
	default:
		return 0, false // NoSuchObject / NoSuchInstance / non-numeric: omit
	}
}

func toOctet(pdu gosnmp.SnmpPDU) ([]byte, bool) {
	if pdu.Type != gosnmp.OctetString {
		return nil, false
	}
	return octetBytes(pdu.Value)
}

func octetBytes(v any) ([]byte, bool) {
	switch t := v.(type) {
	case []byte:
		out := make([]byte, len(t))
		copy(out, t)
		return out, true
	case string:
		return []byte(t), true
	case nil:
		return []byte{}, true
	default:
		return nil, false
	}
}

// ParseVersion maps config strings onto gosnmp versions. Empty is v2c so
// existing ntcip-asc dials keep their historical behaviour.
func ParseVersion(s string) (gosnmp.SnmpVersion, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "v2c", "2c":
		return gosnmp.Version2c, nil
	case "v1", "1":
		return gosnmp.Version1, nil
	default:
		return 0, fmt.Errorf("snmp: version %q not supported (want v1 or v2c)", s)
	}
}

// splitHostPort parses "host:port" into the pieces gosnmp wants. It accepts
// bracketed IPv6 ("[::1]:161") because net.SplitHostPort does.
func splitHostPort(addr string) (string, uint16, error) {
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return "", 0, fmt.Errorf("snmp: address %q must be host:port: %w", addr, err)
	}
	if host == "" {
		return "", 0, fmt.Errorf("snmp: address %q has no host", addr)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return "", 0, fmt.Errorf("snmp: address %q has non-numeric port %q", addr, portStr)
	}
	if port < 1 || port > 65535 {
		return "", 0, fmt.Errorf("snmp: address %q port %d out of range 1-65535", addr, port)
	}
	return host, uint16(port), nil
}
