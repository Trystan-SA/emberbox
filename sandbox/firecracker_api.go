// firecrackerClient is a minimal HTTP-over-Unix-socket client for the
// Firecracker VMM REST API. It deliberately avoids firecracker-go-sdk: the
// surface we need (machine-config, boot-source, drives, vsock, actions) is
// tiny, and pulling the SDK would drag containerd and friends into the
// dependency tree.

package sandbox

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

// firecrackerClient targets a single VMM identified by its API socket path.
type firecrackerClient struct {
	socketPath string
	http       *http.Client
}

// newFirecrackerClient wires an http.Client whose Dial always hits the UDS at
// socketPath. The "host" in URLs is ignored by Firecracker.
func newFirecrackerClient(socketPath string) *firecrackerClient {
	tr := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			d := net.Dialer{}
			return d.DialContext(ctx, "unix", socketPath)
		},
	}
	return &firecrackerClient{
		socketPath: socketPath,
		http:       &http.Client{Transport: tr},
	}
}

// firecrackerMachineConfig mirrors Firecracker's PUT /machine-config body.
type firecrackerMachineConfig struct {
	VcpuCount  int `json:"vcpu_count"`
	MemSizeMib int `json:"mem_size_mib"`
}

// firecrackerBootSource mirrors PUT /boot-source.
type firecrackerBootSource struct {
	KernelImagePath string `json:"kernel_image_path"`
	BootArgs        string `json:"boot_args,omitempty"`
}

// firecrackerDrive mirrors PUT /drives/{drive_id}.
type firecrackerDrive struct {
	DriveID      string `json:"drive_id"`
	PathOnHost   string `json:"path_on_host"`
	IsRootDevice bool   `json:"is_root_device"`
	IsReadOnly   bool   `json:"is_read_only"`
}

// firecrackerVsock mirrors PUT /vsock. UDS multiplexing is what bridges host
// to guest without needing AF_VSOCK on the host — Firecracker creates the
// socket at UdsPath and forwards host CONNECT requests to the guest.
type firecrackerVsock struct {
	GuestCID int    `json:"guest_cid"`
	UdsPath  string `json:"uds_path"`
}

// firecrackerAction mirrors PUT /actions; ActionType is "InstanceStart",
// "SendCtrlAltDel", etc.
type firecrackerAction struct {
	ActionType string `json:"action_type"`
}

// firecrackerAPIError carries Firecracker's JSON error body so callers can log
// it without re-parsing.
type firecrackerAPIError struct {
	Status int
	Body   string
}

func (e *firecrackerAPIError) Error() string {
	return fmt.Sprintf("firecracker api: status=%d body=%s", e.Status, e.Body)
}

// putJSON serializes body and PUTs it to path. Used for every Firecracker
// resource we touch (each one is a single PUT).
func (c *firecrackerClient) putJSON(ctx context.Context, path string, body any) error {
	buf, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal %s: %w", path, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, "http://firecracker"+path, bytes.NewReader(buf))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("PUT %s: %w", path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return &firecrackerAPIError{Status: resp.StatusCode, Body: strings.TrimSpace(string(b))}
	}
	// Drain so the conn returns to the keepalive pool cleanly. Firecracker
	// responses are typically empty, but a few endpoints return JSON.
	_, _ = io.Copy(io.Discard, resp.Body)
	return nil
}

// SetMachineConfig configures vcpus + memory. Must be called before InstanceStart.
func (c *firecrackerClient) SetMachineConfig(ctx context.Context, cfg firecrackerMachineConfig) error {
	return c.putJSON(ctx, "/machine-config", cfg)
}

// SetBootSource configures the kernel image + cmdline.
func (c *firecrackerClient) SetBootSource(ctx context.Context, src firecrackerBootSource) error {
	return c.putJSON(ctx, "/boot-source", src)
}

// SetDrive registers a drive (rootfs or otherwise). DriveID is also the URL segment.
func (c *firecrackerClient) SetDrive(ctx context.Context, d firecrackerDrive) error {
	return c.putJSON(ctx, "/drives/"+d.DriveID, d)
}

// SetVsock configures the vsock device + host UDS multiplexer.
func (c *firecrackerClient) SetVsock(ctx context.Context, v firecrackerVsock) error {
	return c.putJSON(ctx, "/vsock", v)
}

// StartInstance issues PUT /actions {InstanceStart}; the VMM begins booting.
func (c *firecrackerClient) StartInstance(ctx context.Context) error {
	return c.putJSON(ctx, "/actions", firecrackerAction{ActionType: "InstanceStart"})
}

// waitForSocket polls until socketPath is connectable or ctx expires. Used
// after launching the firecracker subprocess to know when the API is up.
func waitForSocket(ctx context.Context, socketPath string, interval time.Duration) error {
	if interval <= 0 {
		interval = 25 * time.Millisecond
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		conn, err := (&net.Dialer{Timeout: interval}).DialContext(ctx, "unix", socketPath)
		if err == nil {
			_ = conn.Close()
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("waitForSocket %s: %w", socketPath, ctx.Err())
		case <-t.C:
		}
	}
}
