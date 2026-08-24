package snmp

import (
	"testing"

	"github.com/gosnmp/gosnmp"
)

func TestSplitHostPort(t *testing.T) {
	cases := []struct {
		addr     string
		wantHost string
		wantPort uint16
		wantErr  bool
	}{
		{addr: "10.0.0.12:161", wantHost: "10.0.0.12", wantPort: 161},
		{addr: "controller.example.org:161", wantHost: "controller.example.org", wantPort: 161},
		{addr: "[::1]:161", wantHost: "::1", wantPort: 161},
		{addr: "10.0.0.12", wantErr: true},   // no port
		{addr: "10.0.0.12:", wantErr: true},  // empty port
		{addr: "", wantErr: true},            // empty address
		{addr: "10.0.0.12:0", wantErr: true}, // port below range
		{addr: "10.0.0.12:70000", wantErr: true},
		{addr: "10.0.0.12:snmp", wantErr: true}, // non-numeric port
	}
	for _, c := range cases {
		host, port, err := splitHostPort(c.addr)
		if c.wantErr {
			if err == nil {
				t.Errorf("splitHostPort(%q) = %q,%d,nil; want error", c.addr, host, port)
			}
			continue
		}
		if err != nil {
			t.Errorf("splitHostPort(%q): unexpected error %v", c.addr, err)
			continue
		}
		if host != c.wantHost || port != c.wantPort {
			t.Errorf("splitHostPort(%q) = %q,%d; want %q,%d", c.addr, host, port, c.wantHost, c.wantPort)
		}
	}
}

func TestParseVersion(t *testing.T) {
	cases := []struct {
		in      string
		want    gosnmp.SnmpVersion
		wantErr bool
	}{
		{in: "", want: gosnmp.Version2c},
		{in: "v2c", want: gosnmp.Version2c},
		{in: "2c", want: gosnmp.Version2c},
		{in: "v1", want: gosnmp.Version1},
		{in: "1", want: gosnmp.Version1},
		{in: "V1", want: gosnmp.Version1},
		{in: "v3", wantErr: true},
		{in: "snmpv1", wantErr: true},
	}
	for _, c := range cases {
		got, err := ParseVersion(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("ParseVersion(%q) = %v, nil; want error", c.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseVersion(%q): unexpected error %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("ParseVersion(%q) = %v; want %v", c.in, got, c.want)
		}
	}
}

func TestOctetBytes(t *testing.T) {
	got, ok := octetBytes([]byte{3, 0, 3})
	if !ok || string(got) != string([]byte{3, 0, 3}) {
		t.Fatalf("[]byte: got %v,%v", got, ok)
	}
	got, ok = octetBytes("abc")
	if !ok || string(got) != "abc" {
		t.Fatalf("string: got %v,%v", got, ok)
	}
	got, ok = octetBytes(nil)
	if !ok || got == nil || len(got) != 0 {
		t.Fatalf("nil: got %v,%v; want empty slice", got, ok)
	}
	if _, ok := octetBytes(12); ok {
		t.Fatal("int must not decode as octet")
	}
}
