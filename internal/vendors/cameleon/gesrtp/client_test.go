package gesrtp

import (
	"context"
	"encoding/binary"
	"io"
	"net"
	"testing"
	"time"
)

// fakeSRTP speaks the init handshake and then one scripted read reply.
type fakeSRTP struct {
	ln   net.Listener
	addr string
	// reply builds the 56+ byte response for a request. seq is the request's byte 2.
	reply func(req []byte) []byte
	// hold blocks the reply until released, for cancel and close tests.
	hold chan struct{}
}

func startFake(t *testing.T, reply func(req []byte) []byte) *fakeSRTP {
	t.Helper()
	var lc net.ListenConfig
	ln, err := lc.Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeSRTP{ln: ln, addr: ln.Addr().String(), reply: reply}
	go f.serve(t)
	t.Cleanup(func() { _ = ln.Close() })
	return f
}

func (f *fakeSRTP) serve(t *testing.T) {
	conn, err := f.ln.Accept()
	if err != nil {
		return
	}
	defer func() { _ = conn.Close() }()
	init := make([]byte, 56)
	if _, err := io.ReadFull(conn, init); err != nil {
		return
	}
	ack := make([]byte, 56)
	ack[0] = 0x01
	if _, err := conn.Write(ack); err != nil {
		return
	}
	for {
		req := make([]byte, 56)
		if _, err := io.ReadFull(conn, req); err != nil {
			return
		}
		if f.hold != nil {
			select {
			case <-f.hold:
			case <-time.After(2 * time.Second):
				return
			}
		}
		if _, err := conn.Write(f.reply(req)); err != nil {
			return
		}
	}
}

func okReply(words []uint16) func(req []byte) []byte {
	return func(req []byte) []byte {
		msg := make([]byte, 56)
		msg[0] = 0x03
		msg[2] = req[2]
		// 0xd4: data sits in bytes 44 onward, and byte 42 is the PLC status.
		msg[31] = 0xd4
		binary.LittleEndian.PutUint16(msg[44:46], words[0])
		if len(words) > 1 {
			binary.LittleEndian.PutUint16(msg[46:48], words[1])
		}
		return msg
	}
}

// captured6WordHdr and captured2WordHdr are the first 56 bytes of replies
// from a 110-word %R read. The 6-word chunks came back as 68-byte 0x94
// messages (byte 42 = 0x0c, the follow-on length). The last 2-word chunk
// came back as a 56-byte 0xd4 message with byte 42 = 0x00. Byte 2 is the
// echoed sequence and is overwritten from the request. The 12 register
// bytes that followed the 6-word header are not stored here.
var captured6WordHdr = []byte{
	0x03, 0x00, 0x02, 0x00, 0x0c, 0x00, 0x00, 0x00,
	0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
	0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
	0x00, 0x00, 0x41, 0x12, 0x02, 0x00, 0x06, 0x94,
	0x10, 0x0e, 0x00, 0x00, 0x10, 0x34, 0x00, 0x00,
	0x01, 0x01, 0x0c, 0x00, 0x0c, 0x0a, 0x37, 0x38,
	0x01, 0x01, 0xff, 0x02, 0x3c, 0x03, 0x1c, 0x01,
}

var captured2WordHdr = []byte{
	0x03, 0x00, 0x02, 0x00, 0x00, 0x00, 0x00, 0x00,
	0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
	0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
	0x00, 0x00, 0x0c, 0x0a, 0x41, 0x12, 0x02, 0xd4,
	0x10, 0x0e, 0x00, 0x00, 0x10, 0x34, 0x00, 0x00,
	0x01, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
	0x00, 0x00, 0xff, 0x02, 0x57, 0x03, 0x1c, 0x01,
}

func TestReplay110WordRead(t *testing.T) {
	if len(captured6WordHdr) != 56 || len(captured2WordHdr) != 56 {
		t.Fatalf("captured headers are %d and %d bytes", len(captured6WordHdr), len(captured2WordHdr))
	}
	f := startFake(t, func(req []byte) []byte {
		count := binary.LittleEndian.Uint16(req[46:48])
		addr := binary.LittleEndian.Uint16(req[44:46])
		switch count {
		case 6:
			msg := append([]byte(nil), captured6WordHdr...)
			msg[2] = req[2]
			payload := make([]byte, 12)
			for i := 0; i < 6; i++ {
				binary.LittleEndian.PutUint16(payload[i*2:], addr+uint16(i))
			}
			return append(msg, payload...)
		case 2:
			msg := append([]byte(nil), captured2WordHdr...)
			msg[2] = req[2]
			return msg
		default:
			// Close writes 56 zero bytes. That is not a memory read.
			if count != 0 {
				t.Errorf("unexpected chunk count %d", count)
			}
			return make([]byte, 56)
		}
	})
	c, err := Dial(context.Background(), f.addr, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	got, err := c.ReadWords(context.Background(), MemR, 1, 110)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 110 {
		t.Fatalf("words = %d", len(got))
	}
	for i := 0; i < 108; i++ {
		if got[i] != uint16(i) {
			t.Fatalf("word %d = %d, want %d", i, got[i], i)
		}
	}
	// The captured 2-word reply carries zeros in the inline data bytes.
	if got[108] != 0 || got[109] != 0 {
		t.Fatalf("tail = %04x %04x", got[108], got[109])
	}
}

func TestReadWordsAgainstFakeServer(t *testing.T) {
	f := startFake(t, okReply([]uint16{0x1111, 0x2222}))
	c, err := Dial(context.Background(), f.addr, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	got, err := c.ReadWords(context.Background(), MemR, 1, 2)
	if err != nil {
		t.Fatal(err)
	}
	if got[0] != 0x1111 || got[1] != 0x2222 {
		t.Fatalf("words = %x", got)
	}
}

func TestShortInlineReplyErrors(t *testing.T) {
	// 56-byte header holds 12 inline bytes. Asking for 8 words needs 16, so
	// the old path zero-padded. readMemoryOnce is the chunker that sees it.
	f := startFake(t, okReply([]uint16{1}))
	c, err := Dial(context.Background(), f.addr, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	_, err = c.readMemoryOnce(context.Background(), MemR, 0, 8)
	if err == nil {
		t.Fatal("short inline reply returned success")
	}
}

func TestResponseSequenceMismatch(t *testing.T) {
	f := startFake(t, func(req []byte) []byte {
		msg := okReply([]uint16{1, 2})(req)
		msg[2] = req[2] + 1
		return msg
	})
	c, err := Dial(context.Background(), f.addr, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	_, err = c.ReadWords(context.Background(), MemR, 1, 2)
	if err == nil {
		t.Fatal("mismatched sequence returned success")
	}
}

func TestUnexpectedReplyTypeErrors(t *testing.T) {
	f := startFake(t, func(req []byte) []byte {
		msg := okReply([]uint16{1, 2})(req)
		msg[31] = 0x00
		return msg
	})
	c, err := Dial(context.Background(), f.addr, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	_, err = c.ReadWords(context.Background(), MemR, 1, 2)
	if err == nil {
		t.Fatal("reply type 0x00 returned success")
	}
}

func TestResponseStatusErrors(t *testing.T) {
	f := startFake(t, func(req []byte) []byte {
		msg := okReply([]uint16{1, 2})(req)
		msg[42] = 0x07
		return msg
	})
	c, err := Dial(context.Background(), f.addr, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	_, err = c.ReadWords(context.Background(), MemR, 1, 2)
	if err == nil {
		t.Fatal("nonzero status returned success")
	}
}

func TestReadRespectsContext(t *testing.T) {
	f := startFake(t, okReply([]uint16{1, 2}))
	f.hold = make(chan struct{})
	c, err := Dial(context.Background(), f.addr, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	ctx, cancel := context.WithCancel(context.Background())
	errc := make(chan error, 1)
	go func() {
		_, err := c.ReadWords(ctx, MemR, 1, 2)
		errc <- err
	}()
	time.Sleep(30 * time.Millisecond)
	cancel()
	select {
	case err := <-errc:
		if err == nil {
			t.Fatal("cancelled read returned success")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cancelled read did not return")
	}
}

func TestCloseDuringReadDoesNotRace(t *testing.T) {
	f := startFake(t, okReply([]uint16{1, 2}))
	f.hold = make(chan struct{})
	c, err := Dial(context.Background(), f.addr, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	errc := make(chan error, 1)
	go func() {
		_, err := c.ReadWords(context.Background(), MemR, 1, 2)
		errc <- err
	}()
	time.Sleep(30 * time.Millisecond)
	closeErr := make(chan error, 1)
	go func() { closeErr <- c.Close() }()
	// Release the reply so the in-flight read can finish and Close can take ioMu.
	close(f.hold)
	select {
	case <-errc:
	case <-time.After(3 * time.Second):
		t.Fatal("read did not finish")
	}
	select {
	case <-closeErr:
	case <-time.After(3 * time.Second):
		t.Fatal("close did not finish")
	}
}
