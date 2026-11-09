package snmp

import (
	"testing"

	"github.com/gosnmp/gosnmp"
)

// FuzzResponseDecode feeds arbitrary bytes through the path a device's
// response takes: gosnmp's packet decode, then this package's conversion of
// each varbind into Values. Whatever the device sends, it must come back as
// a decode error or as values — never a panic. Seeded with well-formed
// responses so the fuzzer mutates realistic packets.
func FuzzResponseDecode(f *testing.F) {
	for _, pkt := range []*gosnmp.SnmpPacket{
		response(gosnmp.Version2c, gosnmp.SnmpPDU{Name: ".1.3.6.1.4.1.1206.4.2.3.6.1.0", Type: gosnmp.Integer, Value: 4}),
		response(gosnmp.Version2c, gosnmp.SnmpPDU{Name: ".1.3.6.1.4.1.1206.4.2.3.6.5.0", Type: gosnmp.OctetString, Value: []byte{3, 0, 3, 0x29, 0x2e}}),
		response(gosnmp.Version2c, gosnmp.SnmpPDU{Name: ".1.3.6.1.2.1.1.3.0", Type: gosnmp.TimeTicks, Value: uint32(12345)}),
		response(gosnmp.Version2c, gosnmp.SnmpPDU{Name: ".1.3.6.1.2.1.31.1.1.1.6.1", Type: gosnmp.Counter64, Value: uint64(1) << 63}),
		response(gosnmp.Version2c, gosnmp.SnmpPDU{Name: ".1.3.6.1.4.1.1206.4.2.3.6.99.0", Type: gosnmp.NoSuchObject}),
		response(gosnmp.Version1, gosnmp.SnmpPDU{Name: ".1.3.6.1.4.1.1206.4.2.3.6.1.0", Type: gosnmp.Null}),
	} {
		raw, err := pkt.MarshalMsg()
		if err != nil {
			f.Fatal(err)
		}
		f.Add(raw)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		decoder := &gosnmp.GoSNMP{Version: gosnmp.Version2c, Logger: gosnmp.NewLogger(nil)}
		pkt, err := decoder.SnmpDecodePacket(data)
		if err != nil {
			return
		}
		for _, pdu := range pkt.Variables {
			if _, ok := toInt64(pdu); ok {
				continue
			}
			_, _ = toOctet(pdu)
		}
	})
}

func response(v gosnmp.SnmpVersion, pdus ...gosnmp.SnmpPDU) *gosnmp.SnmpPacket {
	return &gosnmp.SnmpPacket{
		Version: v, Community: "public", PDUType: gosnmp.GetResponse, RequestID: 7, Variables: pdus,
	}
}
