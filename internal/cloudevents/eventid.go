package cloudevents

import (
	"crypto/sha256"
	"time"
)

// crockford is Crockford base32, the ULID alphabet: no I, L, O or U, so the
// id survives being read aloud or transcribed off a screen.
const crockford = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// unitSep separates the digest's fields. Without it, ("ab","c") and ("a","bc")
// would hash identically and adjacent fields could be shifted without changing
// the id — which is why the spec pins the separator rather than leaving it to
// the implementer.
const unitSep = 0x1f

// EventID derives the deterministic CloudEvents id defined by openits-models'
// ce-id-spec.md:
//
//	digest = SHA-256( source ‖ ceType ‖ stableTime ‖ identity )
//	id     = ULID(timestamp = stableTime-ms, randomness = digest[0:10])
//
// stableTime is the event's occurred-at — NOT ce-time. It feeds both the digest
// and the ULID timestamp, which is what makes the id a pure function of the
// event: two collectors observing one occurrence, or one collector before and
// after a restart, produce the same id without coordinating.
//
// identity is the payload with producer-assigned leaves (sequence, observed-by)
// already cleared. Clearing them is the emitter's job, because only the wire
// layer may know the proto shape (ADR 0002); this function takes bytes.
//
// No clock is read here. Every input arrives as an argument, on purpose: a
// single time.Now() inside would silently destroy the property the whole
// construction exists for.
func EventID(source, ceType string, stableTime time.Time, identity []byte) string {
	h := sha256.New()
	for i, part := range [][]byte{
		[]byte(source), []byte(ceType), []byte(stableMillis(stableTime)), identity,
	} {
		if i > 0 {
			h.Write([]byte{unitSep})
		}
		h.Write(part)
	}
	return ulid(stableTime.UTC().UnixMilli(), h.Sum(nil)[:10])
}

// stableMillis renders the time exactly as the digest expects: RFC 3339 with
// millisecond precision, UTC, "Z". Fixed precision matters — the same instant
// formatted with a different number of fractional digits hashes differently.
func stableMillis(t time.Time) string {
	return t.UTC().Format("2006-01-02T15:04:05.000Z")
}

// ulid encodes a 48-bit millisecond timestamp and 80 bits of randomness as the
// 26-character Crockford base32 ULID.
func ulid(ms int64, randomness []byte) string {
	var v [16]byte
	for i := 0; i < 6; i++ {
		v[i] = byte(ms >> (8 * (5 - i)))
	}
	copy(v[6:], randomness)

	// 128 bits rendered as 26 five-bit groups, most significant first. The
	// leading group carries only the top 3 bits of v[0].
	out := make([]byte, 26)
	for i := range out {
		bit := i * 5
		var acc uint16
		for j := 0; j < 5; j++ {
			acc <<= 1
			// The first two positions of the 130-bit field are padding.
			if p := bit + j - 2; p >= 0 {
				acc |= uint16(v[p/8]>>(7-p%8)) & 1
			}
		}
		out[i] = crockford[acc]
	}
	return string(out)
}
