//go:build !linux

package main

import (
	"errors"
	"net"
)

// listenVsock is a stub on non-Linux platforms — AF_VSOCK is a Linux-only
// kernel feature. Cross-compile for linux/amd64 to actually run the agent.
func listenVsock(uint32) (net.Listener, error) {
	return nil, errors.New("vsock listener is only available on linux")
}
