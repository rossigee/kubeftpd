package ftp

import (
	"context"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

// refusalMessage is sent to a client that connects while the limit is reached.
const refusalMessage = "421 Too many connections, try again later\r\n"

// connContexts holds a context for each live control connection. The context
// is cancelled when the connection closes, so work started for a session stops
// when the client disconnects.
//
// goftp gives drivers no per-connection hook, but it does close the connection
// it accepted, and the connection goes through limitedConn.Close. A session is
// found through its remote address, which is unique among live connections.
type connContexts struct {
	mu      sync.Mutex
	entries map[string]connEntry
	// onAdd and onRemove, when set, run after a connection is added and after
	// it is removed. onRemove receives how long the connection was open. Both
	// are set once, before the listener serves connections.
	onAdd    func(addr string)
	onRemove func(addr string, open time.Duration)
}

type connEntry struct {
	ctx     context.Context
	cancel  context.CancelFunc
	started time.Time
}

func newConnContexts() *connContexts {
	return &connContexts{entries: make(map[string]connEntry)}
}

// add creates the context of the connection with the given remote address.
func (cc *connContexts) add(addr string) {
	ctx, cancel := context.WithCancel(context.Background())
	cc.mu.Lock()
	cc.entries[addr] = connEntry{ctx: ctx, cancel: cancel, started: time.Now()}
	cc.mu.Unlock()
	if cc.onAdd != nil {
		cc.onAdd(addr)
	}
}

// remove cancels the context of the connection with the given remote address.
func (cc *connContexts) remove(addr string) {
	cc.mu.Lock()
	entry, ok := cc.entries[addr]
	delete(cc.entries, addr)
	cc.mu.Unlock()
	if !ok {
		return
	}
	entry.cancel()
	if cc.onRemove != nil {
		cc.onRemove(addr, time.Since(entry.started))
	}
}

// lookup returns the context of the live connection with the given remote
// address, and false when there is none.
func (cc *connContexts) lookup(addr string) (context.Context, bool) {
	if cc == nil {
		return nil, false
	}
	cc.mu.Lock()
	defer cc.mu.Unlock()
	entry, ok := cc.entries[addr]
	return entry.ctx, ok
}

// limitedListener caps the number of concurrent control connections, gives
// each connection an idle timeout, and tracks each connection's context.
// Zero for the limit and the timeout disables them.
type limitedListener struct {
	net.Listener
	max    int
	idle   time.Duration
	active atomic.Int64
	conns  *connContexts
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
		conn := &limitedConn{Conn: c, idle: l.idle, release: func() { l.active.Add(-1) }}
		if l.conns != nil {
			addr := c.RemoteAddr().String()
			l.conns.add(addr)
			conn.untrack = func() { l.conns.remove(addr) }
		}
		return conn, nil
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
	// untrack removes this connection from the registry and cancels its context.
	// It is nil when the listener tracks nothing.
	untrack func()
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
	c.once.Do(func() {
		c.release()
		if c.untrack != nil {
			c.untrack()
		}
	})
	return c.Conn.Close()
}
