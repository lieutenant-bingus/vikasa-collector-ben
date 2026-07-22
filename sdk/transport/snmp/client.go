// Package snmp is an OPTIONAL transport helper adapters may use. The core
// never imports it — transport is entirely the adapter's business.
package snmp

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/gosnmp/gosnmp"
)

// Client fetches integer-valued OIDs. OIDs the agent does not answer are
// omitted from the result map — callers treat absence as a facet-level
// read failure, never as a zero value.
type Client interface {
	Get(ctx context.Context, oids []string) (map[string]int64, error)
	Close() error
}

// DialConfig configures a UDP SNMP v2c session.
type DialConfig struct {
	Address   string // "host:port"
	Community string
	Timeout   time.Duration // default 2s
	Retries   int           // default 1
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
	g := &gosnmp.GoSNMP{
		Target:    host,
		Port:      port,
		Community: cfg.Community,
		Version:   gosnmp.Version2c,
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
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	out := make(map[string]int64, len(oids))
	// gosnmp caps PDUs per request; chunk to stay portable across agents.
	const chunk = 16
	for i := 0; i < len(oids); i += chunk {
		end := min(i+chunk, len(oids))
		pkt, err := c.g.Get(oids[i:end])
		if err != nil {
			return nil, fmt.Errorf("snmp get: %w", err)
		}
		for _, pdu := range pkt.Variables {
			if v, ok := toInt64(pdu); ok {
				out[pdu.Name] = v
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
