package snmptest

import (
	"context"
	"errors"
	"testing"
)

func TestStaticReturnsKnownOIDsOnly(t *testing.T) {
	s := &Static{Values: map[string]int64{".1.3.6.1.4.1.1206.4.2.1.3.2.0": 3}}
	got, err := s.Get(context.Background(), []string{
		".1.3.6.1.4.1.1206.4.2.1.3.2.0",
		".1.3.6.1.4.1.1206.4.2.1.2.7.0", // not in fixture — must be absent, not zero
	})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if v, ok := got[".1.3.6.1.4.1.1206.4.2.1.3.2.0"]; !ok || v != 3 {
		t.Fatalf("known OID = %v,%v; want 3,true", v, ok)
	}
	if _, ok := got[".1.3.6.1.4.1.1206.4.2.1.2.7.0"]; ok {
		t.Fatal("unknown OID must be omitted")
	}
}

func TestStaticErr(t *testing.T) {
	s := &Static{Err: errors.New("boom")}
	if _, err := s.Get(context.Background(), []string{".1"}); err == nil {
		t.Fatal("expected error")
	}
}

func TestStaticFailCallFailsOnlyThatCall(t *testing.T) {
	s := &Static{
		Values:   map[string]int64{".1.3.6.1.4.1.1206.4.2.1.3.2.0": 3},
		FailCall: map[int]error{2: errors.New("timeout")},
	}

	got, err := s.Get(context.Background(), []string{".1.3.6.1.4.1.1206.4.2.1.3.2.0"})
	if err != nil {
		t.Fatalf("first Get: unexpected error: %v", err)
	}
	if v, ok := got[".1.3.6.1.4.1.1206.4.2.1.3.2.0"]; !ok || v != 3 {
		t.Fatalf("first Get result = %v,%v; want 3,true", v, ok)
	}

	if _, err := s.Get(context.Background(), []string{".1.3.6.1.4.1.1206.4.2.1.3.2.0"}); err == nil {
		t.Fatal("second Get: expected the FailCall error")
	}

	if _, err := s.Get(context.Background(), []string{".1.3.6.1.4.1.1206.4.2.1.3.2.0"}); err != nil {
		t.Fatalf("third Get: unexpected error (only call 2 should fail): %v", err)
	}
}

// Err must still fail every call, including when FailCall is also set —
// existing tests (e.g. TestStaticErr and adapter tests using Err alone)
// depend on this.
func TestStaticErrStillFailsEveryCall(t *testing.T) {
	s := &Static{
		Err:      errors.New("boom"),
		FailCall: map[int]error{2: errors.New("should not be reached")},
	}
	for i := 1; i <= 3; i++ {
		if _, err := s.Get(context.Background(), []string{".1"}); err == nil || err.Error() != "boom" {
			t.Fatalf("call %d: err = %v, want %q", i, err, "boom")
		}
	}
}

func TestStaticGetAllReturnsOctets(t *testing.T) {
	oid := ".1.3.6.1.4.1.1206.4.2.3.6.5.0"
	want := []byte{3, 0, 3, 0x29, 0x2e}
	s := &Static{Octets: map[string][]byte{oid: want}}
	got, err := s.GetAll(context.Background(), []string{oid, ".1.missing"})
	if err != nil {
		t.Fatalf("GetAll: %v", err)
	}
	if _, ok := got.Ints[oid]; ok {
		t.Fatal("octet OID must not appear in Ints")
	}
	if b, ok := got.Octets[oid]; !ok || string(b) != string(want) {
		t.Fatalf("Octets[%s] = %v,%v; want %v,true", oid, b, ok, want)
	}
	if _, ok := got.Octets[".1.missing"]; ok {
		t.Fatal("unknown OID must be omitted from Octets")
	}
}

func TestStaticGetDoesNotReturnOctets(t *testing.T) {
	oid := ".1.3.6.1.4.1.1206.4.2.3.6.5.0"
	s := &Static{Octets: map[string][]byte{oid: {1, 2, 3}}}
	got, err := s.Get(context.Background(), []string{oid})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("Get must stay integer-only, got %v", got)
	}
}
