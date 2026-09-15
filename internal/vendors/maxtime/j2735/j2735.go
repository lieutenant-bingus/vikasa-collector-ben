// Package j2735 unwraps MaxTime-CV URL-encoded MessageFrame binaries.
// Full ASN.1 UPER→JER decode stays in Python (pycrate + j2735_202409);
// this package only normalizes MIB payloads to hex/bytes and peeks messageId.
package j2735

import (
	"encoding/hex"
	"fmt"
	"net/url"
	"strings"
)

// Known MessageFrame messageId values (SAE J2735).
const (
	MessageIDMAP  = 18
	MessageIDSPaT = 19
	MessageIDBSM  = 20
)

// DecodeMIBBinary turns a MaxTime MIB string value into raw MessageFrame bytes.
// Controllers return URL-encoded octets ("%00%12%80..."); some paths already
// store lowercase hex without '%'.
func DecodeMIBBinary(encoded string) ([]byte, error) {
	encoded = strings.TrimSpace(encoded)
	if encoded == "" {
		return nil, fmt.Errorf("empty j2735 payload")
	}
	if strings.Contains(encoded, "%") {
		// PathUnescape: do not treat '+' as space (binary payloads).
		b, err := url.PathUnescape(encoded)
		if err != nil {
			return nil, fmt.Errorf("url-decode j2735 payload: %w", err)
		}
		return []byte(b), nil
	}
	// Already hex (even length, hex digits only).
	if len(encoded)%2 == 0 && isHex(encoded) {
		b, err := hex.DecodeString(encoded)
		if err != nil {
			return nil, fmt.Errorf("hex-decode j2735 payload: %w", err)
		}
		return b, nil
	}
	return []byte(encoded), nil
}

// MIBPayloadToHex returns lowercase hex of the MessageFrame bytes.
func MIBPayloadToHex(encoded string) (string, error) {
	b, err := DecodeMIBBinary(encoded)
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// PeekMessageID reads the leading 16-bit big-endian messageId used by
// typical J2735 MessageFrame UPER encodings in this lab (e.g. 0x0012=MAP).
func PeekMessageID(data []byte) (int, bool) {
	if len(data) < 2 {
		return 0, false
	}
	id := int(data[0])<<8 | int(data[1])
	switch id {
	case MessageIDMAP, MessageIDSPaT, MessageIDBSM, 131, 132:
		return id, true
	default:
		// Still return the value; callers may accept unknown IDs.
		if id > 0 && id < 512 {
			return id, true
		}
		return 0, false
	}
}

// PeekMessageIDHex is PeekMessageID for a hex string.
func PeekMessageIDHex(h string) (int, bool) {
	b, err := hex.DecodeString(strings.TrimSpace(h))
	if err != nil {
		return 0, false
	}
	return PeekMessageID(b)
}

func isHex(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= '0' && c <= '9':
		case c >= 'a' && c <= 'f':
		case c >= 'A' && c <= 'F':
		default:
			return false
		}
	}
	return true
}
