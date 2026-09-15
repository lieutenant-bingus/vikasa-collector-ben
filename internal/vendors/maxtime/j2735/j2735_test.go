package j2735

import "testing"

func TestDecodeMIBBinaryURLEncoded(t *testing.T) {
	// MAP messageId 18 → 0x0012
	raw, err := DecodeMIBBinary("%00%12%80")
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) != 3 || raw[0] != 0x00 || raw[1] != 0x12 || raw[2] != 0x80 {
		t.Fatalf("got %x", raw)
	}
	hexStr, err := MIBPayloadToHex("%00%12%80")
	if err != nil || hexStr != "001280" {
		t.Fatalf("hex=%q err=%v", hexStr, err)
	}
	id, ok := PeekMessageID(raw)
	if !ok || id != MessageIDMAP {
		t.Fatalf("id=%d ok=%v", id, ok)
	}
}

func TestDecodeMIBBinaryHex(t *testing.T) {
	raw, err := DecodeMIBBinary("00142506")
	if err != nil {
		t.Fatal(err)
	}
	id, ok := PeekMessageID(raw)
	if !ok || id != MessageIDBSM {
		t.Fatalf("id=%d ok=%v", id, ok)
	}
}
