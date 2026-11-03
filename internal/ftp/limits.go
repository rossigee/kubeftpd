package ftp

import (
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

// refusalMessage is sent to a client that connects while the limit is reached.
const refusalMessage = "421 Too many connections, try again later\r\n"

// limitedListener caps the number of concurrent control connections and gives
// each connection an idle timeout. Zero for either setting disables it.
type limitedListener struct {
	net.Listener
	max    int
	idle   time.Duration
	active atomic.Int64
	// onRefuse is called when a connection is turned away; it may be nil.
	onRefuse func(remote net.Addr)
}

func (l *limitedListener) Accept() (net.Conn, error) {
	for {
		c, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}
		if l.max > 0 && l.active.Load() >= int64(l.max) {
			l.refuse(c)
			continue
		}
		l.active.Add(1)
		return &limitedConn{Conn: c, idle: l.idle, release: func() { l.active.Add(-1) }}, nil
	}
}

// refuse tells the client why it is being turned away, then drops the connection.
// The write is bounded so a stalled client cannot hold up the accept loop.
func (l *limitedListener) refuse(c net.Conn) {
	_ = c.SetWriteDeadline(time.Now().Add(time.Second))
	_, _ = io.WriteString(c, refusalMessage)
	_ = c.Close()
	if l.onRefuse != nil {
		l.onRefuse(c.RemoteAddr())
	}
}

// limitedConn releases its slot in the listener exactly once, when closed, and
// applies the idle timeout to each read.
type limitedConn struct {
	net.Conn
	idle    time.Duration
	release func()
	once    sync.Once
}

// Read sets the deadline immediately before each read. The deadline therefore
// only applies while the server is waiting for the client; a long transfer on a
// data connection, or a command still being executed, is not cut short.
func (c *limitedConn) Read(p []byte) (int, error) {
	if c.idle > 0 {
		if err := c.SetReadDeadline(time.Now().Add(c.idle)); err != nil {
			return 0, err
		}
	}
	return c.Conn.Read(p)
}

func (c *limitedConn) Close() error {
	c.once.Do(c.release)
	return c.Conn.Close()
}
