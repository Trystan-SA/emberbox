// DockerBackend example: run tools inside a real Docker container.
//
// Prerequisite: build the agent image once.
//
//	docker build -t emberbox-agent:test .
//
// Then:
//
//	go run ./examples/dockerbackend
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Trystan-SA/emberbox/sandbox"
)

func main() {
	backend, err := sandbox.NewDockerBackend(sandbox.DockerConfig{
		Image: "emberbox-agent:test",
	})
	if err != nil {
		panic(err)
	}

	pool, err := sandbox.New(sandbox.Config{
		Backend:        backend,
		DefaultTimeout: 30 * time.Second,
	})
	if err != nil {
		panic(err)
	}
	defer pool.Shutdown(context.Background())

	ctx := context.Background()

	id, err := pool.Allocate(ctx, sandbox.AllocRequest{})
	if err != nil {
		panic(err)
	}
	fmt.Printf("container booted: %s\n", id[:12])

	// Container userland (Alpine) vs host userland — containers share the host
	// kernel, so `uname` is a red herring; check /etc/os-release and the
	// package manager instead.
	cmd := `echo "--- /etc/os-release ---"
cat /etc/os-release
echo
echo "--- which apk apt ---"
which apk apt 2>&1
echo
echo "--- hostname ---"
hostname
echo
echo "--- whoami ---"
whoami`
	payload, _ := json.Marshal(map[string]string{"command": cmd})
	res, err := pool.Execute(ctx, id, "bash", payload)
	if err != nil {
		panic(err)
	}
	fmt.Printf("--- guest output (%dms) ---\n%s", res.DurationMS, res.Content)

	pool.Release(ctx, id)
	fmt.Println("container destroyed")
}
