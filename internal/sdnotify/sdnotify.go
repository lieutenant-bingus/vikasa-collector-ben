// Package sdnotify speaks systemd's notify protocol: one datagram of
// newline-separated KEY=VALUE assignments to the socket named by
// $NOTIFY_SOCKET. podman passes the same socket through with
// --sdnotify=container. It is a few lines of protocol, so it is implemented
// here rather than pulled in, and needs no cgo.
package sdnotify

import (
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"time"
)

// Notifier sends notifications to a supervisor. A nil *Notifier is valid and
// does nothing: that is the collector running without one.
type Notifier struct {
	addr *net.UnixAddr
}

// New returns a Notifier for socket, the value of $NOTIFY_SOCKET. Empty means
// no supervisor is listening, and returns nil. systemd passes an absolute
// path, or an abstract-namespace name written with a leading "@"; anything
// else is a misconfigured supervisor.
func New(socket string) (*Notifier, error) {
	switch {
	case socket == "":
		return nil, nil
	case strings.HasPrefix(socket, "@"):
		return &Notifier{addr: &net.UnixAddr{Name: "\x00" + socket[1:], Net: "unixgram"}}, nil
	case strings.HasPrefix(socket, "/"):
		return &Notifier{addr: &net.UnixAddr{Name: socket, Net: "unixgram"}}, nil
	default:
		return nil, fmt.Errorf("NOTIFY_SOCKET %q: want an absolute path or an @abstract name", socket)
	}
}

// Notify sends the assignments as one datagram. A send is a fresh dial, as
// libsystemd's is: the socket may be replaced while the service runs.
func (n *Notifier) Notify(assignments ...string) error {
	if n == nil {
		return nil
	}
	conn, err := net.DialUnix("unixgram", nil, n.addr)
	if err != nil {
		return fmt.Errorf("sd_notify: %w", err)
	}
	defer func() { _ = conn.Close() }()
	if _, err := conn.Write([]byte(strings.Join(assignments, "\n"))); err != nil {
		return fmt.Errorf("sd_notify: %w", err)
	}
	return nil
}

// WatchdogInterval reads $WATCHDOG_USEC and $WATCHDOG_PID. It reports the
// watchdog timeout when the supervisor armed one for this process; a
// WATCHDOG_PID naming another process means the watchdog is not ours.
func WatchdogInterval(usec, pid string) (time.Duration, bool) {
	if usec == "" {
		return 0, false
	}
	if pid != "" && pid != strconv.Itoa(os.Getpid()) {
		return 0, false
	}
	n, err := strconv.ParseInt(usec, 10, 64)
	if err != nil || n <= 0 {
		return 0, false
	}
	return time.Duration(n) * time.Microsecond, true
}
