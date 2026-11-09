package sdnotify

import (
	"net"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// listen opens a unixgram socket where systemd's would be. A short directory:
// macOS caps a socket path at 104 bytes, and t.TempDir's can exceed it.
func listen(t *testing.T) (string, *net.UnixConn) {
	t.Helper()
	dir, err := os.MkdirTemp("", "sdn")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	path := filepath.Join(dir, "notify")
	conn, err := net.ListenUnixgram("unixgram", &net.UnixAddr{Name: path, Net: "unixgram"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return path, conn
}

func recv(t *testing.T, conn *net.UnixConn) string {
	t.Helper()
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 4096)
	n, err := conn.Read(buf)
	if err != nil {
		t.Fatalf("no datagram: %v", err)
	}
	return string(buf[:n])
}

func TestNotifySendsOneDatagram(t *testing.T) {
	path, conn := listen(t)
	n, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := n.Notify("READY=1", "STATUS=polling"); err != nil {
		t.Fatal(err)
	}
	if got := recv(t, conn); got != "READY=1\nSTATUS=polling" {
		t.Fatalf("datagram %q", got)
	}
}

// No supervisor socket means not running under one: every call is a no-op,
// so the collector behaves exactly as it did before this existed.
func TestNoSocketIsANoOp(t *testing.T) {
	n, err := New("")
	if err != nil || n != nil {
		t.Fatalf("New(\"\") = %v, %v; want nil, nil", n, err)
	}
	if err := n.Notify("READY=1"); err != nil {
		t.Fatalf("a nil Notifier must be a no-op, got %v", err)
	}
}

func TestNewRejectsARelativeSocket(t *testing.T) {
	if _, err := New("run/notify"); err == nil {
		t.Fatal("want an error: systemd passes an absolute path or an @abstract name")
	}
}

func TestAbstractSocketName(t *testing.T) {
	n, err := New("@vikasa-test")
	if err != nil {
		t.Fatal(err)
	}
	if n.addr.Name != "\x00vikasa-test" {
		t.Fatalf("abstract name %q; want a leading NUL", n.addr.Name)
	}
}

func TestWatchdogInterval(t *testing.T) {
	pid := strconv.Itoa(os.Getpid())
	cases := []struct {
		name, usec, pid string
		want            time.Duration
		ok              bool
	}{
		{"unset", "", "", 0, false},
		{"set, no pid", "30000000", "", 30 * time.Second, true},
		{"set, our pid", "30000000", pid, 30 * time.Second, true},
		{"another process's watchdog", "30000000", "1", 0, false},
		{"garbage", "thirty", "", 0, false},
		{"zero", "0", "", 0, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := WatchdogInterval(c.usec, c.pid)
			if got != c.want || ok != c.ok {
				t.Fatalf("WatchdogInterval(%q, %q) = %v, %v; want %v, %v", c.usec, c.pid, got, ok, c.want, c.ok)
			}
		})
	}
}
