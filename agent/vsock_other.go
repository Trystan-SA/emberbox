//go:build !linux

package agent

import (
	"errors"
	"net"
)

// ListenVsock is a stub on non-Linux platforms — AF_VSOCK is a Linux-only
// kernel feature. Cross-compile for linux/amd64 (or another linux arch) to
// run an in-guest agent against a Firecracker microVM.
func ListenVsock(uint32) (net.Listener, error) {
	return nil, errors.New("vsock listener is only available on linux")
}
