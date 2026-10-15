package cameleon

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Vikasa2M/vikasa-collector/internal/vendors/cameleon/gesrtp"
	"github.com/Vikasa2M/vikasa-collector/sdk/model"
)

// CAMELEON_RECORD names a file under testdata/. The recorder opens a file
// only when that variable is set, and only when the path stays inside testdata.

const recordEnv = "CAMELEON_RECORD"

const acsRecording = "testdata/acs-srtp.hex"

func labGates() []gateCfg {
	return []gateCfg{
		{id: "WG-111", kind: model.GateKindWarning, dsw: 101},
		{id: "BG-114", kind: model.GateKindBarrier, dsw: 104},
	}
}

func labCabinet() cabinetCfg {
	return cabinetCfg{lfFault: 179, spFault: 180, door: 181, gateEstop: 182}
}

type frame struct {
	req bool
	b   []byte
}

// recordingConn writes every successful Read and Write, in order, as hex.
// It is constructed only by startRecord, which refuses to open a file unless
// CAMELEON_RECORD is set to a path inside testdata/.
type recordingConn struct {
	net.Conn
	mu sync.Mutex
	f  *os.File
}

func startRecord(conn net.Conn) (net.Conn, func() error, error) {
	path := os.Getenv(recordEnv)
	if path == "" {
		return conn, func() error { return nil }, nil
	}
	abs, err := recordFile(path)
	if err != nil {
		return nil, nil, err
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return nil, nil, err
	}
	f, err := os.OpenFile(abs, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, nil, err
	}
	if _, err := fmt.Fprintln(f, "# srtp request/reply hex; no addresses"); err != nil {
		_ = f.Close()
		return nil, nil, err
	}
	rc := &recordingConn{Conn: conn, f: f}
	return rc, rc.close, nil
}

func recordFile(path string) (string, error) {
	root, err := filepath.Abs("testdata")
	if err != nil {
		return "", err
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(root, abs)
	if err != nil {
		return "", err
	}
	if rel == "." || rel == "" || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("CAMELEON_RECORD must name a file under testdata")
	}
	return abs, nil
}

func (c *recordingConn) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	n, err := c.Conn.Write(p)
	if n > 0 {
		if werr := c.writeFrameLocked("req", p[:n]); werr != nil {
			return n, werr
		}
	}
	return n, err
}

func (c *recordingConn) Read(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	n, err := c.Conn.Read(p)
	if n > 0 {
		if werr := c.writeFrameLocked("rsp", p[:n]); werr != nil {
			return n, werr
		}
	}
	return n, err
}

func (c *recordingConn) writeFrameLocked(dir string, p []byte) error {
	_, err := fmt.Fprintf(c.f, "%s %s\n", dir, hex.EncodeToString(p))
	return err
}

func (c *recordingConn) close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.f.Close()
}

func parseRecording(r io.Reader) ([]frame, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	var out []frame
	lineNo := 0
	for sc.Scan() {
		lineNo++
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		dir, hexPart, ok := strings.Cut(line, " ")
		if !ok {
			return nil, fmt.Errorf("line %d: want %q", lineNo, "req|rsp <hex>")
		}
		raw, err := hex.DecodeString(hexPart)
		if err != nil {
			return nil, fmt.Errorf("line %d: %w", lineNo, err)
		}
		switch dir {
		case "req":
			out = append(out, frame{req: true, b: raw})
		case "rsp":
			out = append(out, frame{req: false, b: raw})
		default:
			return nil, fmt.Errorf("line %d: direction %q", lineNo, dir)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("recording has no frames")
	}
	return out, nil
}

func loadRecording(path string) ([]frame, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	return parseRecording(f)
}

// serveRecording plays frames on 127.0.0.1. Each recorded request is read
// in full, then every following reply is written, until the next request.
// The returned channel receives one error (nil on success) when the session ends.
func serveRecording(t *testing.T, frames []frame) (string, <-chan error) {
	t.Helper()
	var lc net.ListenConfig
	ln, err := lc.Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	errc := make(chan error, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			errc <- err
			return
		}
		defer func() { _ = conn.Close() }()
		_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
		errc <- playRecording(conn, frames)
	}()
	return ln.Addr().String(), errc
}

func playRecording(conn net.Conn, frames []frame) error {
	var mismatch error
	i := 0
	for i < len(frames) {
		if !frames[i].req {
			return fmt.Errorf("frame %d: reply before request", i)
		}
		got := make([]byte, len(frames[i].b))
		if _, err := io.ReadFull(conn, got); err != nil {
			return err
		}
		if mismatch == nil && !bytes.Equal(got, frames[i].b) {
			mismatch = fmt.Errorf("frame %d: request bytes differ from the recording", i)
		}
		i++
		for i < len(frames) && !frames[i].req {
			if _, err := conn.Write(frames[i].b); err != nil {
				return err
			}
			i++
		}
	}
	return mismatch
}

// wordsFromRecording rebuilds the %R image from recorded read replies,
// independent of gesrtp.Client.
func wordsFromRecording(frames []frame) (map[int]uint16, error) {
	words := map[int]uint16{}
	i := 0
	for i < len(frames) {
		if !frames[i].req {
			return nil, fmt.Errorf("frame %d: reply before request", i)
		}
		req := frames[i].b
		i++
		var rsp []byte
		for i < len(frames) && !frames[i].req {
			rsp = append(rsp, frames[i].b...)
			i++
		}
		if len(req) < 56 || req[42] != 0x04 {
			continue
		}
		if len(rsp) < 56 {
			return nil, fmt.Errorf("short read reply (%d bytes)", len(rsp))
		}
		addr := int(binary.LittleEndian.Uint16(req[44:46]))
		count := int(binary.LittleEndian.Uint16(req[46:48]))
		var payload []byte
		switch rsp[31] {
		case 0xd4:
			if rsp[42] != 0 {
				return nil, fmt.Errorf("inline reply status 0x%02x", rsp[42])
			}
			payload = rsp[44:]
		case 0x94:
			declared := int(rsp[42])
			if 56+declared > len(rsp) {
				return nil, fmt.Errorf("follow-on length %d exceeds reply %d", declared, len(rsp))
			}
			payload = rsp[56 : 56+declared]
		default:
			return nil, fmt.Errorf("reply type 0x%02x", rsp[31])
		}
		if len(payload) < count*2 {
			return nil, fmt.Errorf("payload %d bytes for %d words", len(payload), count)
		}
		for w := 0; w < count; w++ {
			words[addr+w] = binary.LittleEndian.Uint16(payload[w*2:])
		}
	}
	return words, nil
}

func readACS(t *testing.T, addr string) (*model.Snapshot, *model.GateBank, model.FaultSet) {
	t.Helper()
	client, err := gesrtp.Dial(addr, 2*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	a := NewACS("acs-lab", client, 90, 110, labGates(), labCabinet())
	snap, err := a.Read(context.Background())
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if err := client.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	facet, ok := snap.Facet(model.KindGateBank)
	if !ok {
		t.Fatal("missing gate-bank")
	}
	bank := facet.(model.GateBank)
	ff, ok := snap.Facet(model.KindFaultSet)
	if !ok {
		t.Fatal("missing fault-set")
	}
	return snap, &bank, ff.(model.FaultSet)
}

func expectFromWords(t *testing.T, words map[int]uint16) (model.GateBank, model.FaultSet) {
	t.Helper()
	var gates []model.GateReading
	var faults []model.Fault
	for _, g := range labGates() {
		w, ok := words[g.dsw-1]
		if !ok {
			t.Fatalf("recording has no %%R%d", g.dsw)
		}
		gr := DecodeGateWord(g.id, g.kind, w)
		gates = append(gates, gr)
		faults = append(faults, gateFaults(gr)...)
	}
	cab := labCabinet()
	need := []int{cab.lfFault, cab.spFault, cab.door, cab.gateEstop}
	for _, addr := range need {
		if _, ok := words[addr-1]; !ok {
			t.Fatalf("recording has no %%R%d", addr)
		}
	}
	faults = append(faults, cabinetFaults(words[cab.lfFault-1], words[cab.spFault-1], words[cab.door-1], words[cab.gateEstop-1])...)
	sortGates(gates)
	sortFaults(faults)
	return model.GateBank{Gates: gates}, model.FaultSet{Faults: faults}
}

func recordingContainsIdentity(path, addr string) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if host != "" && bytes.Contains(bytes.ToLower(raw), bytes.ToLower([]byte(host))) {
		return fmt.Errorf("recording file contains %q", host)
	}
	frames, err := parseRecording(bytes.NewReader(raw))
	if err != nil {
		return err
	}
	var all []byte
	for _, fr := range frames {
		all = append(all, fr.b...)
	}
	if host != "" && bytes.Contains(bytes.ToLower(all), bytes.ToLower([]byte(host))) {
		return fmt.Errorf("srtp frames contain %q", host)
	}
	if ipv4ASCII.Match(all) || ipv4ASCII.Match(raw) {
		return fmt.Errorf("recording contains an IPv4 address")
	}
	return nil
}

var ipv4ASCII = regexpMust()

// scriptedPLC answers one init and then %R reads from words (0-based).
func scriptedPLC(t *testing.T, words []uint16) string {
	t.Helper()
	var lc net.ListenConfig
	ln, err := lc.Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
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
			if req[42] != 0x04 {
				_, _ = conn.Write(make([]byte, 56))
				return
			}
			addr := int(binary.LittleEndian.Uint16(req[44:46]))
			count := int(binary.LittleEndian.Uint16(req[46:48]))
			payload := make([]byte, count*2)
			for i := 0; i < count; i++ {
				if addr+i < len(words) {
					binary.LittleEndian.PutUint16(payload[i*2:], words[addr+i])
				}
			}
			msg := make([]byte, 56+len(payload))
			msg[0] = 0x03
			msg[2] = req[2]
			msg[31] = 0x94
			msg[42] = byte(len(payload))
			copy(msg[56:], payload)
			if _, err := conn.Write(msg); err != nil {
				return
			}
		}
	}()
	return ln.Addr().String()
}

func regexpMust() *regexp.Regexp {
	return regexp.MustCompile(`(?:\d{1,3}\.){3}\d{1,3}`)
}

func dialRecorded(t *testing.T, addr, path string, timeout time.Duration) {
	t.Helper()
	raw, err := (&net.Dialer{Timeout: timeout}).DialContext(context.Background(), "tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	wrapped, finish, err := startRecord(raw)
	if err != nil {
		_ = raw.Close()
		t.Fatal(err)
	}
	client, err := gesrtp.New(wrapped, timeout)
	if err != nil {
		_ = raw.Close()
		t.Fatal(err)
	}
	a := NewACS("acs-lab", client, 90, 110, labGates(), labCabinet())
	if _, err := a.Read(context.Background()); err != nil {
		_ = a.Close()
		_ = finish()
		t.Fatal(err)
	}
	if err := a.Close(); err != nil {
		_ = finish()
		t.Fatal(err)
	}
	if err := finish(); err != nil {
		t.Fatal(err)
	}
	if err := recordingContainsIdentity(path, addr); err != nil {
		_ = os.Remove(path)
		t.Fatal(err)
	}
}

func TestRecordDisabledDoesNotCreateFile(t *testing.T) {
	t.Setenv(recordEnv, "")
	before := testdataFiles(t)
	conn := &nopConn{}
	got, finish, err := startRecord(conn)
	if err != nil {
		t.Fatal(err)
	}
	if got != conn {
		t.Fatal("unset CAMELEON_RECORD wrapped the connection")
	}
	if err := finish(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, testdataFiles(t)) {
		t.Fatalf("testdata changed: before %v after %v", before, testdataFiles(t))
	}
}

func TestRecordRejectsPathOutsideTestdata(t *testing.T) {
	outside := filepath.Join(t.TempDir(), "acs.hex")
	t.Setenv(recordEnv, outside)
	_, _, err := startRecord(&nopConn{})
	if err == nil {
		t.Fatal("expected path outside testdata to be rejected")
	}
	if _, statErr := os.Stat(outside); !os.IsNotExist(statErr) {
		t.Fatalf("recorder created %s: %v", outside, statErr)
	}
}

type nopConn struct{}

func (nopConn) Read([]byte) (int, error)         { return 0, io.EOF }
func (nopConn) Write(p []byte) (int, error)      { return len(p), nil }
func (nopConn) Close() error                     { return nil }
func (nopConn) LocalAddr() net.Addr              { return nil }
func (nopConn) RemoteAddr() net.Addr             { return nil }
func (nopConn) SetDeadline(time.Time) error      { return nil }
func (nopConn) SetReadDeadline(time.Time) error  { return nil }
func (nopConn) SetWriteDeadline(time.Time) error { return nil }

func testdataFiles(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir("testdata")
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

func TestSRTPRecordReplayRoundTrip(t *testing.T) {
	words := make([]uint16, 200)
	words[100] = 0x0025
	words[103] = 0x0021
	path := filepath.Join("testdata", "roundtrip-"+strings.ReplaceAll(t.Name(), "/", "_")+".hex")
	_ = os.Remove(path)
	t.Cleanup(func() { _ = os.Remove(path) })
	t.Setenv(recordEnv, path)

	dialRecorded(t, scriptedPLC(t, words), path, 2*time.Second)

	frames, err := loadRecording(path)
	if err != nil {
		t.Fatal(err)
	}
	_, bank, faults := replayACS(t, frames)
	wantBank, wantFaults := expectFromWords(t, mustWords(t, frames))
	if !reflect.DeepEqual(bank.Gates, wantBank.Gates) {
		t.Fatalf("gates = %+v\nwant %+v", bank.Gates, wantBank.Gates)
	}
	if !reflect.DeepEqual(faults.Faults, wantFaults.Faults) {
		t.Fatalf("faults = %+v\nwant %+v", faults.Faults, wantFaults.Faults)
	}
	if bank.Gates[0].ID != "BG-114" || bank.Gates[0].Raw != 0x0021 {
		t.Fatalf("BG-114 = %+v", bank.Gates[0])
	}
	if bank.Gates[1].ID != "WG-111" || bank.Gates[1].Raw != 0x0025 {
		t.Fatalf("WG-111 = %+v", bank.Gates[1])
	}
}

func replayACS(t *testing.T, frames []frame) (*model.Snapshot, *model.GateBank, model.FaultSet) {
	t.Helper()
	addr, errc := serveRecording(t, frames)
	snap, bank, faults := readACS(t, addr)
	select {
	case err := <-errc:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("replay server did not finish")
	}
	return snap, bank, faults
}

func mustWords(t *testing.T, frames []frame) map[int]uint16 {
	t.Helper()
	words, err := wordsFromRecording(frames)
	if err != nil {
		t.Fatal(err)
	}
	return words
}

// TestACSReplayGolden replays a recorded lab session through the real
// adapter. It does not run until testdata/acs-srtp.hex exists.
func TestACSReplayGolden(t *testing.T) {
	if _, err := os.Stat(acsRecording); err != nil {
		t.Skip("needs recording")
	}
	frames, err := loadRecording(acsRecording)
	if err != nil {
		t.Fatal(err)
	}
	if err := recordingContainsIdentity(acsRecording, "unused.example:18245"); err != nil {
		t.Fatal(err)
	}
	snap, bank, faults := replayACS(t, frames)
	if len(snap.Errors) != 0 {
		t.Fatalf("facet errors: %+v", snap.Errors)
	}
	wantBank, wantFaults := expectFromWords(t, mustWords(t, frames))
	if !reflect.DeepEqual(bank.Gates, wantBank.Gates) {
		t.Fatalf("GateBank = %+v\nwant %+v", bank.Gates, wantBank.Gates)
	}
	if !reflect.DeepEqual(faults.Faults, wantFaults.Faults) {
		t.Fatalf("FaultSet = %+v\nwant %+v", faults.Faults, wantFaults.Faults)
	}
}

// TestRecordACSRead captures one live poll. It writes only when both
// ACS_SRTP_ADDR and CAMELEON_RECORD are set, and CAMELEON_RECORD is a
// file under testdata/.
//
//	ACS_SRTP_ADDR=<host>:18245 CAMELEON_RECORD=testdata/acs-srtp.hex go test ./internal/vendors/cameleon -run TestRecordACSRead -count=1 -v
func TestRecordACSRead(t *testing.T) {
	addr := os.Getenv("ACS_SRTP_ADDR")
	path := os.Getenv(recordEnv)
	if addr == "" || path == "" {
		t.Skip("set ACS_SRTP_ADDR and CAMELEON_RECORD=testdata/acs-srtp.hex to record a lab poll")
	}
	dialRecorded(t, addr, path, 8*time.Second)
	t.Logf("wrote %s", path)
}
