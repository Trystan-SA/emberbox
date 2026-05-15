//go:build linux

// ListenVsock binds an AF_VSOCK listener on the given port and returns a
// net.Listener that Agent.Serve can drive. CID is VMADDR_CID_ANY so the agent
// accepts whatever CID the guest was assigned (typically 3 from the host).
//
// Implementation note: Go's stdlib net package doesn't recognize AF_VSOCK, so
// we drive the syscalls via golang.org/x/sys/unix and wrap the resulting fds
// as *os.File. os.NewFile registers fds with Go's poller, which gives us
// goroutine-friendly accept/read/write plus working SetDeadline.
package agent

import (
	"errors"
	"fmt"
	"net"
	"os"
	"time"

	"golang.org/x/sys/unix"
)

// vsockAddr satisfies net.Addr for connections/listeners we hand back to the
// agent. The agent only logs the address; precise CID/port detail isn't
// required, so we keep this minimal.
type vsockAddr struct {
	cid  uint32
	port uint32
}

func (a vsockAddr) Network() string { return "vsock" }
func (a vsockAddr) String() string  { return fmt.Sprintf("vsock://%d:%d", a.cid, a.port) }

// vsockListener wraps an AF_VSOCK listening socket as a net.Listener.
type vsockListener struct {
	f    *os.File
	port uint32
}

// ListenVsock creates a non-blocking AF_VSOCK socket, binds it on VMADDR_CID_ANY
// at port, and starts listening. The returned net.Listener integrates with Go's
// runtime poller via os.File and can be passed straight to Agent.Serve.
//
// Linux-only: on other platforms this returns an error.
func ListenVsock(port uint32) (net.Listener, error) {
	fd, err := unix.Socket(unix.AF_VSOCK, unix.SOCK_STREAM|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("vsock socket: %w", err)
	}
	if err := unix.SetNonblock(fd, true); err != nil {
		_ = unix.Close(fd)
		return nil, fmt.Errorf("vsock set nonblock: %w", err)
	}
	sa := &unix.SockaddrVM{CID: unix.VMADDR_CID_ANY, Port: port}
	if err := unix.Bind(fd, sa); err != nil {
		_ = unix.Close(fd)
		return nil, fmt.Errorf("vsock bind: %w", err)
	}
	if err := unix.Listen(fd, 16); err != nil {
		_ = unix.Close(fd)
		return nil, fmt.Errorf("vsock listen: %w", err)
	}
	return &vsockListener{
		f:    os.NewFile(uintptr(fd), fmt.Sprintf("vsock:%d", port)),
		port: port,
	}, nil
}

// Accept blocks until a new connection arrives. We drive accept(2) through
// SyscallConn so the goroutine parks on Go's runtime poller instead of an OS
// thread, and so closing the listener unblocks it cleanly.
func (l *vsockListener) Accept() (net.Conn, error) {
	rawConn, err := l.f.SyscallConn()
	if err != nil {
		return nil, err
	}
	var (
		nfd       int
		acceptErr error
		sa        unix.Sockaddr
	)
	ctrlErr := rawConn.Read(func(fd uintptr) bool {
		nfd, sa, acceptErr = unix.Accept4(int(fd), unix.SOCK_CLOEXEC|unix.SOCK_NONBLOCK)
		// Returning false re-arms the poller and parks the goroutine; true
		// means "I'm done with the fd for now."
		return !errors.Is(acceptErr, unix.EAGAIN)
	})
	if ctrlErr != nil {
		return nil, ctrlErr
	}
	if acceptErr != nil {
		return nil, fmt.Errorf("vsock accept: %w", acceptErr)
	}
	remote := vsockAddr{}
	if vsa, ok := sa.(*unix.SockaddrVM); ok {
		remote = vsockAddr{cid: vsa.CID, port: vsa.Port}
	}
	return &vsockConn{
		f:      os.NewFile(uintptr(nfd), fmt.Sprintf("vsock-conn:%d", nfd)),
		local:  vsockAddr{port: l.port},
		remote: remote,
	}, nil
}

func (l *vsockListener) Close() error   { return l.f.Close() }
func (l *vsockListener) Addr() net.Addr { return vsockAddr{port: l.port} }

// vsockConn is an AF_VSOCK net.Conn. The underlying *os.File handles poller
// integration, deadlines, and graceful close — we only have to satisfy the
// LocalAddr/RemoteAddr part of the interface.
type vsockConn struct {
	f      *os.File
	local  vsockAddr
	remote vsockAddr
}

func (c *vsockConn) Read(b []byte) (int, error)         { return c.f.Read(b) }
func (c *vsockConn) Write(b []byte) (int, error)        { return c.f.Write(b) }
func (c *vsockConn) Close() error                       { return c.f.Close() }
func (c *vsockConn) LocalAddr() net.Addr                { return c.local }
func (c *vsockConn) RemoteAddr() net.Addr               { return c.remote }
func (c *vsockConn) SetDeadline(t time.Time) error      { return c.f.SetDeadline(t) }
func (c *vsockConn) SetReadDeadline(t time.Time) error  { return c.f.SetReadDeadline(t) }
func (c *vsockConn) SetWriteDeadline(t time.Time) error { return c.f.SetWriteDeadline(t) }
