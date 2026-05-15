package sandbox

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const dockerImageEnv = "EMBERBOX_DOCKER_IMAGE" // override via env; defaults to emberbox-agent:test

func dockerAvailable() bool {
	cmd := exec.Command("docker", "info")
	return cmd.Run() == nil
}

func requireDocker(t *testing.T) string {
	t.Helper()
	if !dockerAvailable() {
		t.Skip("docker not available; skipping")
	}
	image := os.Getenv(dockerImageEnv)
	if image == "" {
		image = "emberbox-agent:test"
	}
	// Verify the image is present locally — building it is the consumer's
	// responsibility (see Dockerfile at repo root).
	if err := exec.Command("docker", "image", "inspect", image).Run(); err != nil {
		t.Skipf("image %q not present; build it with `docker build -t %s .`", image, image)
	}
	return image
}

func TestDockerBackend_BootExecDestroy(t *testing.T) {
	image := requireDocker(t)

	b, err := NewDockerBackend(DockerConfig{Image: image})
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	h, err := b.Boot(ctx, AllocRequest{})
	require.NoError(t, err)
	t.Cleanup(func() { _ = b.Destroy(context.Background(), h) })

	res, err := b.Exec(ctx, h, "bash", json.RawMessage(`{"command":"echo hi"}`))
	require.NoError(t, err)
	require.False(t, res.IsError, "result: %+v", res)
	require.Equal(t, "hi\n", res.Content)

	require.NoError(t, b.Destroy(ctx, h))
}

func TestDockerBackend_RequiresImage(t *testing.T) {
	_, err := NewDockerBackend(DockerConfig{})
	require.Error(t, err)
}

func TestDockerBackend_RejectsForeignHandle(t *testing.T) {
	b, err := NewDockerBackend(DockerConfig{Image: "anything"})
	require.NoError(t, err)
	_, err = b.Exec(context.Background(), &hostHandle{id: "x"}, "any", nil)
	require.Error(t, err)
}
