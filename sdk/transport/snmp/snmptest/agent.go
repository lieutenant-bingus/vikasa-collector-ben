package snmptest

import (
	"context"
	"errors"
	"net"
	"testing"

	"github.com/gosnmp/gosnmp"
)

// ServeAgent runs a minimal SNMP v1/v2c agent on loopback UDP for the life
// of the test and returns its "host:port". It answers GET only, from ints and
// octets keyed by numeric OID with a leading dot, the same maps Static
// replays. It exists so code that dials a real agent — the fixture recorder,
// an adapter's end-to-end test — runs against the real client and wire
// encoding without a device.
//
// It behaves as an agent does where that changes what a client sees:
//   - a request with the wrong community gets no reply, so the client times out;
//   - v2c answers an unknown OID with noSuchObject, per varbind;
//   - v1 has no per-varbind exceptions, so one unknown OID fails the whole
//     request with noSuchName and no value is returned for any OID in it.
func ServeAgent(t testing.TB, community string, ints map[string]int64, octets map[string][]byte) string {
	t.Helper()
	conn, err := new(net.ListenConfig).ListenPacket(context.Background(), "udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("snmptest agent: %v", err)
	}
	done := make(chan struct{})
	t.Cleanup(func() {
		_ = conn.Close()
		<-done
	})
	go func() {
		defer close(done)
		decoder := &gosnmp.GoSNMP{Version: gosnmp.Version2c, Logger: gosnmp.NewLogger(nil)}
		buf := make([]byte, 65535)
		for {
			n, from, err := conn.ReadFrom(buf)
			if errors.Is(err, net.ErrClosed) {
				return
			}
			if err != nil {
				continue
			}
			req, err := decoder.SnmpDecodePacket(buf[:n])
			if err != nil || req.PDUType != gosnmp.GetRequest || req.Community != community {
				continue // as an agent does: no reply to what it won't answer
			}
			out, err := respond(req, ints, octets).MarshalMsg()
			if err != nil {
				continue
			}
			_, _ = conn.WriteTo(out, from)
		}
	}()
	return conn.LocalAddr().String()
}

func respond(req *gosnmp.SnmpPacket, ints map[string]int64, octets map[string][]byte) *gosnmp.SnmpPacket {
	resp := &gosnmp.SnmpPacket{
		Version:   req.Version,
		Community: req.Community,
		PDUType:   gosnmp.GetResponse,
		RequestID: req.RequestID,
	}
	for i, vb := range req.Variables {
		switch {
		case hasKey(ints, vb.Name):
			resp.Variables = append(resp.Variables, gosnmp.SnmpPDU{Name: vb.Name, Type: gosnmp.Integer, Value: int(ints[vb.Name])})
		case hasKey(octets, vb.Name):
			resp.Variables = append(resp.Variables, gosnmp.SnmpPDU{Name: vb.Name, Type: gosnmp.OctetString, Value: octets[vb.Name]})
		case req.Version == gosnmp.Version1:
			resp.Error = gosnmp.NoSuchName
			resp.ErrorIndex = uint8(min(i+1, 255)) //nolint:gosec // bounded by min
			resp.Variables = nullsFor(req.Variables)
			return resp
		default:
			resp.Variables = append(resp.Variables, gosnmp.SnmpPDU{Name: vb.Name, Type: gosnmp.NoSuchObject})
		}
	}
	return resp
}

func hasKey[V any](m map[string]V, k string) bool {
	_, ok := m[k]
	return ok
}

// nullsFor echoes a v1 request's varbinds with no values, as an agent does
// alongside an error status.
func nullsFor(vbs []gosnmp.SnmpPDU) []gosnmp.SnmpPDU {
	out := make([]gosnmp.SnmpPDU, len(vbs))
	for i, vb := range vbs {
		out[i] = gosnmp.SnmpPDU{Name: vb.Name, Type: gosnmp.Null}
	}
	return out
}
