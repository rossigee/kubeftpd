package ftp

import (
	"bufio"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// acceptLoop feeds accepted connections to a channel until the listener closes.
func acceptLoop(t *testing.T, l net.Listener) <-chan net.Conn {
	t.Helper()
	accepted := make(chan net.Conn, 8)
	go func() {
		defer close(accepted)
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			accepted <- c
		}
	}()
	return accepted
}

func TestLimitedListener_RefusesOverLimitThenRecovers(t *testing.T) {
	raw, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	l := &limitedListener{Listener: raw, max: 1}
	t.Cleanup(func() { _ = l.Close() })
	accepted := acceptLoop(t, l)
	addr := raw.Addr().String()

	first, err := net.Dial("tcp", addr)
	require.NoError(t, err)
	t.Cleanup(func() { _ = first.Close() })
	serverFirst := <-accepted

	// The second client is turned away with a 421 reply.
	second, err := net.Dial("tcp", addr)
	require.NoError(t, err)
	t.Cleanup(func() { _ = second.Close() })
	require.NoError(t, second.SetReadDeadline(time.Now().Add(5*time.Second)))
	reply, err := bufio.NewReader(second).ReadString('\n')
	require.NoError(t, err)
	assert.Equal(t, refusalMessage, reply)

	// Once the first session ends, its slot is free for a new client.
	require.NoError(t, serverFirst.Close())
	third, err := net.Dial("tcp", addr)
	require.NoError(t, err)
	t.Cleanup(func() { _ = third.Close() })
	select {
	case <-accepted:
	case <-time.After(5 * time.Second):
		t.Fatal("a client was not accepted after a slot was freed")
	}
}

func TestLimitedListener_NoLimitAcceptsEveryone(t *testing.T) {
	raw, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	l := &limitedListener{Listener: raw}
	t.Cleanup(func() { _ = l.Close() })
	accepted := acceptLoop(t, l)

	for i := 0; i < 3; i++ {
		c, err := net.Dial("tcp", raw.Addr().String())
		require.NoError(t, err)
		t.Cleanup(func() { _ = c.Close() })
	}
	for i := 0; i < 3; i++ {
		select {
		case <-accepted:
		case <-time.After(5 * time.Second):
			t.Fatalf("connection %d was not accepted with no limit set", i+1)
		}
	}
}

// A client that sends nothing is disconnected after the idle timeout.
func TestLimitedConn_IdleTimeoutDisconnectsQuietClient(t *testing.T) {
	raw, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	l := &limitedListener{Listener: raw, idle: 50 * time.Millisecond}
	t.Cleanup(func() { _ = l.Close() })
	accepted := acceptLoop(t, l)

	client, err := net.Dial("tcp", raw.Addr().String())
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })

	server := <-accepted
	defer func() { _ = server.Close() }()

	buf := make([]byte, 16)
	_, err = server.Read(buf)
	var netErr net.Error
	require.ErrorAs(t, err, &netErr)
	assert.True(t, netErr.Timeout(), "the read must end on the idle deadline")
}

// Closing a connection releases its slot exactly once, however often it is closed.
func TestLimitedConn_ReleasesOnce(t *testing.T) {
	a, b := net.Pipe()
	t.Cleanup(func() { _ = b.Close() })

	releases := 0
	c := &limitedConn{Conn: a, release: func() { releases++ }}
	_ = c.Close()
	_ = c.Close()
	assert.Equal(t, 1, releases)
}
