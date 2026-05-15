package sandbox

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// fakeVMM is an in-process stand-in for a real `firecracker` subprocess.
//
// It mimics the parts of Firecracker we touch:
//   - serves the Firecracker REST API over the api-sock path
//   - on receiving PUT /vsock, starts a UDS multiplexer at the configured
//     uds_path that speaks the CONNECT/OK framing and tunnels bytes to a
//     pretend in-VM agent
//   - on receiving PUT /actions {InstanceStart}, starts the pretend agent
//
// The fake records every PUT it sees so tests can assert on the API calls
// the backend made. It exposes an Exit channel that, when closed, simulates
// a VMM crash.
type fakeVMM struct {
	apiPath string
	udsPath string // populated once PUT /vsock is received

	mu       sync.Mutex
	puts     []fakeVMMPut
	started  bool
	vsockLn  net.Listener // UDS multiplexer
	agentFn  func(net.Conn)
	stopOnce sync.Once
	stopped  chan struct{}
	apiLn    net.Listener
}

type fakeVMMPut struct {
	Path string
	Body []byte
}

// newFakeVMM creates a fake VMM that responds with an in-process agent that
// returns canned content for any tool. Use newFakeVMMWithAgent for custom
// agent behavior.
func newFakeVMM() *fakeVMM {
	return &fakeVMM{
		stopped: make(chan struct{}),
		agentFn: defaultFakeAgent,
	}
}

// defaultFakeAgent decodes one agentRequest and replies with a fixed Output.
func defaultFakeAgent(c net.Conn) {
	defer func() { _ = c.Close() }()
	var req agentRequest
	if err := json.NewDecoder(c).Decode(&req); err != nil {
		return
	}
	_ = json.NewEncoder(c).Encode(agentResponse{
		Output:     fmt.Sprintf("fake-agent:%s", req.ToolName),
		IsError:    false,
		DurationMS: 1,
	})
}

// serveAPI listens on apiPath and handles PUTs by recording them and starting
// the vsock multiplexer / fake agent on the appropriate triggers. Returns
// once the API listener is closed.
func (v *fakeVMM) serveAPI(apiPath string) error {
	ln, err := net.Listen("unix", apiPath)
	if err != nil {
		return err
	}
	v.mu.Lock()
	v.apiPath = apiPath
	v.apiLn = ln
	v.mu.Unlock()

	mux := http.NewServeMux()
	mux.HandleFunc("/", v.handle)
	srv := &http.Server{Handler: mux, ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second}
	go func() {
		<-v.stopped
		_ = srv.Close()
	}()
	return srv.Serve(ln)
}

func (v *fakeVMM) handle(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	body, _ := io.ReadAll(r.Body)
	v.mu.Lock()
	v.puts = append(v.puts, fakeVMMPut{Path: r.URL.Path, Body: body})
	v.mu.Unlock()

	switch r.URL.Path {
	case "/vsock":
		var vs firecrackerVsock
		if err := json.Unmarshal(body, &vs); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := v.startVsockUDS(vs.UdsPath); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	case "/actions":
		v.mu.Lock()
		v.started = true
		v.mu.Unlock()
	}
	w.WriteHeader(http.StatusNoContent)
}

// startVsockUDS opens the host-side UDS multiplexer at udsPath. Each incoming
// connection: read "CONNECT <port>\n", reply "OK <port>\n", then hand off to
// the agent function. Mirrors Firecracker's wire protocol closely enough that
// dialAgentVsockUDS and waitForVsockAgent can't tell the difference.
func (v *fakeVMM) startVsockUDS(udsPath string) error {
	ln, err := net.Listen("unix", udsPath)
	if err != nil {
		return fmt.Errorf("listen vsock uds %s: %w", udsPath, err)
	}
	v.mu.Lock()
	v.udsPath = udsPath
	v.vsockLn = ln
	v.mu.Unlock()

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go v.serveVsockConn(conn)
		}
	}()
	go func() {
		<-v.stopped
		_ = ln.Close()
	}()
	return nil
}

func (v *fakeVMM) serveVsockConn(c net.Conn) {
	br := bufio.NewReader(c)
	line, err := br.ReadString('\n')
	if err != nil {
		_ = c.Close()
		return
	}
	if !strings.HasPrefix(strings.TrimSpace(line), "CONNECT ") {
		_, _ = fmt.Fprintf(c, "REJECTED %s\n", strings.TrimSpace(line))
		_ = c.Close()
		return
	}
	if _, err := fmt.Fprintf(c, "OK 1\n"); err != nil {
		_ = c.Close()
		return
	}
	// Wrap so any bytes already buffered in br stay visible to the agent.
	v.agentFn(&vsockBufferedConn{Conn: c, br: br})
}

// stop shuts down both the API and vsock listeners.
func (v *fakeVMM) stop() {
	v.stopOnce.Do(func() { close(v.stopped) })
}

// putsCopy returns a snapshot of recorded PUTs, ordered by receipt.
func (v *fakeVMM) putsCopy() []fakeVMMPut {
	v.mu.Lock()
	defer v.mu.Unlock()
	out := make([]fakeVMMPut, len(v.puts))
	copy(out, v.puts)
	return out
}

// installFake wires fake VMM lifecycle into a FirecrackerBackend by injecting
// a launchVMM that starts the fake API server on apiPath and returns a stub
// *exec.Cmd. The Cmd is `sleep 600` so Destroy's SIGTERM path is exercised.
func installFake(t *testing.T, b *FirecrackerBackend, fake *fakeVMM) {
	t.Helper()
	b.launchVMM = func(_ context.Context, apiPath, _ string, logFile *os.File) (*exec.Cmd, error) {
		ready := make(chan error, 1)
		go func() {
			err := fake.serveAPI(apiPath)
			// http.Server.Close returns ErrServerClosed; treat that as a
			// clean shutdown rather than test failure.
			if err != nil && !errors.Is(err, http.ErrServerClosed) {
				select {
				case ready <- err:
				default:
				}
			}
		}()
		// Wait for the API socket to appear so Boot's waitForSocket succeeds
		// without racing.
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			if _, err := os.Stat(apiPath); err == nil {
				break
			}
			select {
			case err := <-ready:
				return nil, err
			default:
				time.Sleep(5 * time.Millisecond)
			}
		}
		// Setpgid=true mirrors the real launch so Destroy's pgid-targeted
		// SIGTERM/SIGKILL reaches the fake process the same way it reaches
		// real firecracker.
		cmd := exec.Command("/bin/sh", "-c", "exec sleep 600")
		cmd.Stdout = logFile
		cmd.Stderr = logFile
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		if err := cmd.Start(); err != nil {
			fake.stop()
			return nil, err
		}
		t.Cleanup(fake.stop)
		return cmd, nil
	}
}

// shortTempDir creates a temp dir under /tmp with a short prefix so the full
// path of any UDS we put inside it stays under Linux's 108-byte sun_path
// limit. Using t.TempDir() would bake the (long) test name into the path,
// which blows the cap once we add the per-VM subdir + "firecracker.sock" on
// top.
func shortTempDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "ebx-")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

func newFakeFirecrackerBackend(t *testing.T) (*FirecrackerBackend, *fakeVMM) {
	t.Helper()
	dir := shortTempDir(t)
	// Sentinel files; the backend doesn't open them in fake mode but
	// validateConfig requires non-empty paths.
	kernel := filepath.Join(dir, "k")
	rootfs := filepath.Join(dir, "r")
	require.NoError(t, os.WriteFile(kernel, []byte("fake kernel"), 0o644))
	require.NoError(t, os.WriteFile(rootfs, []byte("fake rootfs"), 0o644))

	b := NewFirecrackerBackend(FirecrackerConfig{
		FirecrackerBinary: "/bin/sh", // unused under installFake, but kept legal
		KernelPath:        kernel,
		RootfsPath:        rootfs,
		WorkRoot:          dir,
		BootTimeout:       5 * time.Second,
		StopTimeout:       2 * time.Second,
	})
	fake := newFakeVMM()
	installFake(t, b, fake)
	return b, fake
}

func TestFirecrackerBackend_Boot_RequiresKernelAndRootfs(t *testing.T) {
	b := NewFirecrackerBackend(FirecrackerConfig{})
	_, err := b.Boot(context.Background(), AllocRequest{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "KernelPath")
	require.Contains(t, err.Error(), "RootfsPath")
}

func TestFirecrackerBackend_RejectsForeignHandle(t *testing.T) {
	b := NewFirecrackerBackend(FirecrackerConfig{KernelPath: "/x", RootfsPath: "/y"})
	_, err := b.Exec(context.Background(), &hostHandle{id: "x"}, "any", nil)
	require.Error(t, err)
	err = b.Destroy(context.Background(), &hostHandle{id: "x"})
	require.Error(t, err)
}

func TestFirecrackerBackend_BootSendsExpectedAPISequence(t *testing.T) {
	b, fake := newFakeFirecrackerBackend(t)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	h, err := b.Boot(ctx, AllocRequest{MemoryMB: 256, VCPUs: 2})
	require.NoError(t, err)
	t.Cleanup(func() { _ = b.Destroy(context.Background(), h) })

	puts := fake.putsCopy()
	require.GreaterOrEqual(t, len(puts), 5)

	// The API sequence is order-sensitive: machine-config → boot-source →
	// drive → vsock → actions. Anything else is a Firecracker rejection
	// waiting to happen, so we assert order here rather than just presence.
	require.Equal(t, "/machine-config", puts[0].Path)
	require.Equal(t, "/boot-source", puts[1].Path)
	require.Equal(t, "/drives/rootfs", puts[2].Path)
	require.Equal(t, "/vsock", puts[3].Path)
	require.Equal(t, "/actions", puts[4].Path)

	var mc firecrackerMachineConfig
	require.NoError(t, json.Unmarshal(puts[0].Body, &mc))
	require.Equal(t, 256, mc.MemSizeMib)
	require.Equal(t, 2, mc.VcpuCount)

	var drive firecrackerDrive
	require.NoError(t, json.Unmarshal(puts[2].Body, &drive))
	require.True(t, drive.IsRootDevice)
	require.True(t, drive.IsReadOnly, "rootfs must default to read-only so the same image is safe to share across VMs")

	var action firecrackerAction
	require.NoError(t, json.Unmarshal(puts[4].Body, &action))
	require.Equal(t, "InstanceStart", action.ActionType)
}

func TestFirecrackerBackend_ExecRoundTrip(t *testing.T) {
	b, _ := newFakeFirecrackerBackend(t)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	h, err := b.Boot(ctx, AllocRequest{})
	require.NoError(t, err)
	t.Cleanup(func() { _ = b.Destroy(context.Background(), h) })

	res, err := b.Exec(ctx, h, "echo", json.RawMessage(`{"x":1}`))
	require.NoError(t, err)
	require.False(t, res.IsError)
	require.Equal(t, "fake-agent:echo", res.Content)
}

func TestFirecrackerBackend_DestroyIsIdempotent(t *testing.T) {
	b, _ := newFakeFirecrackerBackend(t)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	h, err := b.Boot(ctx, AllocRequest{})
	require.NoError(t, err)

	require.NoError(t, b.Destroy(ctx, h))
	require.NoError(t, b.Destroy(ctx, h), "Destroy must be idempotent")
}

func TestFirecrackerBackend_DestroyCleansUpWorkDir(t *testing.T) {
	b, _ := newFakeFirecrackerBackend(t)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	h, err := b.Boot(ctx, AllocRequest{})
	require.NoError(t, err)
	fh := h.(*firecrackerHandle)
	require.DirExists(t, fh.workDir)

	require.NoError(t, b.Destroy(ctx, h))
	_, statErr := os.Stat(fh.workDir)
	require.True(t, os.IsNotExist(statErr), "Destroy must remove per-VM work dir")
}

func TestFirecrackerBackend_RealBinaryIntegration(t *testing.T) {
	// Skip unless a Firecracker binary, kernel, and rootfs are all available.
	// Mirrors docker_test's "build the image yourself" pattern — the test
	// asserts the integration works when the host has everything set up, but
	// doesn't try to download multi-megabyte VM images in CI.
	bin := os.Getenv("EMBERBOX_FIRECRACKER_BIN")
	kernel := os.Getenv("EMBERBOX_FIRECRACKER_KERNEL")
	rootfs := os.Getenv("EMBERBOX_FIRECRACKER_ROOTFS")
	if bin == "" || kernel == "" || rootfs == "" {
		t.Skip("EMBERBOX_FIRECRACKER_BIN, _KERNEL, and _ROOTFS not all set; skipping real-VM integration test")
	}
	if _, err := exec.LookPath(bin); err != nil {
		t.Skipf("firecracker binary %q not executable: %v", bin, err)
	}

	b := NewFirecrackerBackend(FirecrackerConfig{
		FirecrackerBinary: bin,
		KernelPath:        kernel,
		RootfsPath:        rootfs,
		BootTimeout:       45 * time.Second,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	h, err := b.Boot(ctx, AllocRequest{})
	require.NoError(t, err)
	t.Cleanup(func() { _ = b.Destroy(context.Background(), h) })

	res, err := b.Exec(ctx, h, "bash", json.RawMessage(`{"command":"echo hi"}`))
	require.NoError(t, err)
	require.False(t, res.IsError, "result: %+v", res)
	require.Equal(t, "hi\n", res.Content)
}
