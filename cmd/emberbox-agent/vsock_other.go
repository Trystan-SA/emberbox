//go:build !linux

package main

import (
	"errors"
	"net"
)

// listenVsock is a no-op stub on non-Linux platforms. AF_VSOCK is a Linux-only
// kernel feature, so building the agent for darwin/windows only gives you the
// TCP listen path. This stub exists so the rest of main.go compiles unchanged.
func listenVsock(uint32) (net.Listener, error) {
	return nil, errors.New("vsock listener is only available on linux")
}
