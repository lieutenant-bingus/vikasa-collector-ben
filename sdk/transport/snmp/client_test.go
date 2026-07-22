package snmp

import "testing"

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
