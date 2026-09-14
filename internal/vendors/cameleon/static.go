package cameleon

import "fmt"

// staticWords is a fixture SRTP client: ReadWords returns a slice of the
// preloaded block starting at address1Based. Addresses outside the block
// error so misconfigured tests fail loudly.
type staticWords struct {
	start int // 1-based %R of words[0]
	words []uint16
}

func (s *staticWords) ReadWords(_ byte, address1Based, count int) ([]uint16, error) {
	if address1Based < s.start {
		return nil, fmt.Errorf("address %%R%d before fixture start %%R%d", address1Based, s.start)
	}
	off := address1Based - s.start
	if off+count > len(s.words) {
		return nil, fmt.Errorf("read %%R%d count %d past fixture end", address1Based, count)
	}
	out := make([]uint16, count)
	copy(out, s.words[off:off+count])
	return out, nil
}

func (s *staticWords) Close() error { return nil }
