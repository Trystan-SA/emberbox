//go:build linux

// listenVsock binds an AF_VSOCK listener on the given port. Same implementation
// as cmd/emberbox-agent/vsock_linux.go — the duplication is intentional: this
// example is meant to be readable on its own and to mirror what an external
// project would write when shipping its own guest agent.
package main

import (
	"errors"
	"fmt"
	"net"
	"os"
	"time"

	"golang.org/x/sys/unix"
)

type vsockAddr struct {
	cid  uint32
	port uint32
}

func (a vsockAddr) Network() string { return "vsock" }
func (a vsockAddr) String() string  { return fmt.Sprintf("vsock://%d:%d", a.cid, a.port) }

type vsockListener struct {
	f    *os.File
	port uint32
}

func listenVsock(port uint32) (net.Listener, error) {
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
