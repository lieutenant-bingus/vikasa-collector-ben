// Package gesrtp implements a read-only GE Fanuc / Emerson SRTP client
// (TCP port 18245) for Series 90 / VersaMax style memory access.
//
// Kept under internal/vendors/cameleon — not sdk/transport — so the collector
// public surface does not advertise a GE protocol implementation.
package gesrtp

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"net"
	"sync"
	"time"
)

const DefaultPort = 18245

// Memory type codes used at SRTP request byte 43.
const (
	MemR      byte = 0x08 // %R register word
	MemAI     byte = 0x0a // %AI analog input word
	MemAQ     byte = 0x0c // %AQ analog output word
	MemIByte  byte = 0x10 // %I discrete input (byte)
	MemQByte  byte = 0x12 // %Q discrete output (byte)
	MemTByte  byte = 0x14 // %T temporary (byte)
	MemMByte  byte = 0x16 // %M marker (byte)
	MemSAByte byte = 0x18 // %SA system (byte)
	MemSBByte byte = 0x20 // %SB system (byte)
	MemSCByte byte = 0x22 // %SC system (byte)
	MemGByte  byte = 0x38 // %G genius (byte)
	MemIBit   byte = 0x46 // %I bit
	MemQBit   byte = 0x48 // %Q bit
	MemTBit   byte = 0x4a // %T bit
	MemMBit   byte = 0x4c // %M bit
)

const (
	svcReadSysMemory  byte = 0x04
	svcControllerType byte = 0x43
)

// Client is a read-focused SRTP connection to a GE/Emerson PLC.
type Client struct {
	ioMu    sync.Mutex
	conn    net.Conn
	timeout time.Duration
	seq     byte
}

// Dial connects to host:port, completes the SRTP init handshake, and returns a client.
func Dial(addr string, timeout time.Duration) (*Client, error) {
	if timeout <= 0 {
		timeout = 3 * time.Second
	}
	conn, err := net.DialTimeout("tcp", addr, timeout)
	if err != nil {
		return nil, fmt.Errorf("dial: %w", err)
	}
	c, err := New(conn, timeout)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	return c, nil
}

// New completes the SRTP handshake on an already-open connection.
// Tests use it to wrap conn (for example to record frames) before the
// handshake. The caller owns conn; Close closes it.
func New(conn net.Conn, timeout time.Duration) (*Client, error) {
	if conn == nil {
		return nil, fmt.Errorf("nil connection")
	}
	if timeout <= 0 {
		timeout = 3 * time.Second
	}
	c := &Client{conn: conn, timeout: timeout, seq: 1}
	if err := c.init(); err != nil {
		return nil, err
	}
	return c, nil
}

// Close ends the SRTP session. It shares ioMu with reads so a close cannot
// Read the reply that belongs to an in-flight ReadMemory.
func (c *Client) Close() error {
	c.ioMu.Lock()
	defer c.ioMu.Unlock()
	if c.conn == nil {
		return nil
	}
	_ = c.conn.SetDeadline(time.Now().Add(c.timeout))
	_, _ = c.conn.Write(make([]byte, 56)) // common close/ack pattern
	buf := make([]byte, 64)
	_, _ = c.conn.Read(buf)
	err := c.conn.Close()
	c.conn = nil
	return err
}

func (c *Client) init() error {
	if err := c.conn.SetDeadline(time.Now().Add(c.timeout)); err != nil {
		return err
	}
	if _, err := c.conn.Write(make([]byte, 56)); err != nil {
		return fmt.Errorf("init write: %w", err)
	}
	resp := make([]byte, 1024)
	n, err := c.conn.Read(resp)
	if err != nil {
		return fmt.Errorf("init read: %w", err)
	}
	if n < 1 || resp[0] != 0x01 {
		return fmt.Errorf("init handshake failed: first byte=0x%02x (want 0x01), n=%d", resp[0], n)
	}
	return nil
}

func (c *Client) nextSeqLocked() byte {
	c.seq++
	if c.seq == 0 {
		c.seq = 1
	}
	return c.seq
}

func baseMessage(seq byte) []byte {
	msg := make([]byte, 56)
	msg[0] = 0x02
	msg[2] = seq
	msg[9] = 0x01
	msg[17] = 0x01
	msg[30] = 0x06
	msg[31] = 0xc0
	msg[36] = 0x10
	msg[37] = 0x0e
	msg[40] = 0x01
	msg[41] = 0x01
	return msg
}

// maxInlineUnits keeps responses in the 56-byte header (bytes 44-55 = 12 bytes).
const maxInlineUnits = 6

// ReadMemory reads count units starting at address0Based.
// For word memories (R/AI/AQ), count is words.
// For byte memories (I/Q/M/...), address is 0-based byte index and count is bytes.
// ctx cancels the in-flight read by expiring the connection deadline.
func (c *Client) ReadMemory(ctx context.Context, memType byte, address0Based int, count int) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if count < 1 {
		return nil, fmt.Errorf("count must be >= 1")
	}
	if address0Based < 0 {
		return nil, fmt.Errorf("address must be >= 0")
	}

	// Chunk at 6 words. A real ACS answers that size with a 0x94 follow-on
	// (12 data bytes after the header) and answers the trailing 2-word
	// chunk inline (0xd4).
	unitBytes := 1
	switch memType {
	case MemR, MemAI, MemAQ:
		unitBytes = 2
	case MemIBit, MemQBit, MemTBit, MemMBit:
		unitBytes = 2
	}
	chunk := maxInlineUnits // 6 words = 12 bytes
	if unitBytes != 2 {
		chunk = 12 // 12 discrete bytes
	}

	out := make([]byte, 0, expectedByteLen(memType, count))
	remaining := count
	addr := address0Based
	for remaining > 0 {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		n := chunk
		if n > remaining {
			n = remaining
		}
		part, err := c.readMemoryOnce(ctx, memType, addr, n)
		if err != nil {
			return nil, err
		}
		out = append(out, part...)
		addr += n
		remaining -= n
	}
	return out, nil
}

func (c *Client) readMemoryOnce(ctx context.Context, memType byte, address0Based int, count int) ([]byte, error) {
	c.ioMu.Lock()
	defer c.ioMu.Unlock()
	if c.conn == nil {
		return nil, fmt.Errorf("srtp connection closed")
	}
	conn := c.conn
	seq := c.nextSeqLocked()
	msg := baseMessage(seq)
	msg[42] = svcReadSysMemory
	msg[43] = memType
	binary.LittleEndian.PutUint16(msg[44:46], uint16(address0Based))
	binary.LittleEndian.PutUint16(msg[46:48], uint16(count))

	stop, err := armDeadline(ctx, conn, c.timeout)
	if err != nil {
		return nil, err
	}
	defer stop()

	if _, err := conn.Write(msg); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("read write: %w", err)
	}

	// TCP is a byte stream: read the whole 56-byte header, not whatever one
	// Read happens to return. Anything after the header (0x94 payload) is
	// read below.
	first := make([]byte, 56)
	if _, err := io.ReadFull(conn, first); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("short response: %w", err)
	}
	n := len(first)
	slog.Debug("srtp reply", "n", n, "hdr", hex.EncodeToString(first[:n]))
	if first[2] != seq {
		return nil, fmt.Errorf("response sequence 0x%02x, want 0x%02x", first[2], seq)
	}

	need := expectedByteLen(memType, count)
	// Byte 31 selects the layout. 0xd4 carries the bytes inline at offset 44
	// and byte 42 is the PLC status. 0x94 carries the bytes after the header
	// and byte 42 is that data length, not a status.
	switch first[31] {
	case 0xd4:
		if first[0] != 0x03 {
			return nil, fmt.Errorf("unexpected response type 0x%02x (want 0x03) hdr31=0xd4", first[0])
		}
		if st := first[42]; st != 0 {
			return nil, fmt.Errorf("srtp status 0x%02x", st)
		}
		inline := first[44:n]
		if len(inline) < need {
			return nil, fmt.Errorf("short inline reply: got %d bytes, want %d", len(inline), need)
		}
		return append([]byte(nil), inline[:need]...), nil
	case 0x94:
		if first[0] != 0x03 {
			return nil, fmt.Errorf("unexpected response type 0x%02x (want 0x03) hdr31=0x94", first[0])
		}
		declared := int(first[42])
		if declared < need {
			return nil, fmt.Errorf("srtp data length 0x%02x shorter than %d bytes", first[42], need)
		}
		data := append([]byte(nil), first[56:n]...)
		if len(data) < declared {
			buf := make([]byte, 4096)
			for len(data) < declared {
				stopMore, err := armDeadline(ctx, conn, c.timeout)
				if err != nil {
					return nil, err
				}
				m, err := conn.Read(buf)
				stopMore()
				if err != nil {
					if ctx.Err() != nil {
						return nil, ctx.Err()
					}
					if err == io.EOF && len(data) > 0 {
						break
					}
					return nil, fmt.Errorf("read data packet: %w", err)
				}
				data = append(data, buf[:m]...)
			}
		}
		if len(data) < need {
			return nil, fmt.Errorf("incomplete data packet: got %d want %d (hdr type=0x94)", len(data), need)
		}
		return data[:need], nil
	default:
		return nil, fmt.Errorf("unexpected srtp reply type 0x%02x status 0x%02x", first[31], first[42])
	}
}

// armDeadline bounds one exchange by the shorter of the client timeout and
// ctx. Cancelling ctx expires the deadline so a blocked Read returns.
func armDeadline(ctx context.Context, conn net.Conn, timeout time.Duration) (func(), error) {
	deadline := time.Now().Add(timeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	if err := conn.SetDeadline(deadline); err != nil {
		return nil, err
	}
	stop := context.AfterFunc(ctx, func() {
		_ = conn.SetDeadline(time.Now())
	})
	return func() { stop() }, nil
}

func expectedByteLen(memType byte, count int) int {
	switch memType {
	case MemR, MemAI, MemAQ:
		return count * 2
	case MemIBit, MemQBit, MemTBit, MemMBit:
		// Bit reads typically return a word; treat count as number of bits requested.
		return ((count + 15) / 16) * 2
	default:
		return count
	}
}

// ReadWords reads %R/%AI/%AQ style word memory. address is 1-based (%R1 => 1).
func (c *Client) ReadWords(ctx context.Context, memType byte, address1Based int, count int) ([]uint16, error) {
	raw, err := c.ReadMemory(ctx, memType, address1Based-1, count)
	if err != nil {
		return nil, err
	}
	out := make([]uint16, count)
	for i := 0; i < count; i++ {
		out[i] = binary.LittleEndian.Uint16(raw[i*2 : i*2+2])
	}
	return out, nil
}

// ReadBytes reads discrete byte memory. byteIndex0 is 0-based (%I1-%I8 => 0).
func (c *Client) ReadBytes(ctx context.Context, memType byte, byteIndex0 int, count int) ([]byte, error) {
	return c.ReadMemory(ctx, memType, byteIndex0, count)
}

// ReadBit reads a single discrete bit. address1Based is GE numbering (%I1 => 1).
func (c *Client) ReadBit(ctx context.Context, memType byte, address1Based int) (bool, error) {
	raw, err := c.ReadMemory(ctx, memType, address1Based-1, 1)
	if err != nil {
		return false, err
	}
	if len(raw) >= 2 {
		return binary.LittleEndian.Uint16(raw[:2]) != 0, nil
	}
	return raw[0] != 0, nil
}

// BitAddressToByteIndex converts GE 1-based bit address to 0-based byte index.
func BitAddressToByteIndex(bit1Based int) int {
	if bit1Based < 1 {
		return 0
	}
	return (bit1Based - 1) / 8
}
