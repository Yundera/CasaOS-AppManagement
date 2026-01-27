package docker

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
)

const (
	alpineImage   = "alpine:latest"
	rootOpTimeout = 30 * time.Second
)

// RemovePathAsRoot removes a directory and all its contents using a Docker
// container running as root. This solves the problem where volume folders
// created by containers running as root cannot be deleted by a non-root
// CasaOS process.
func RemovePathAsRoot(ctx context.Context, path string) error {
	if path == "" {
		return fmt.Errorf("path cannot be empty")
	}

	absPath, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("failed to resolve absolute path: %w", err)
	}

	// Check if path exists
	if _, err := os.Stat(absPath); os.IsNotExist(err) {
		return nil // Already gone, nothing to do
	}

	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return fmt.Errorf("failed to create docker client: %w", err)
	}
	defer cli.Close()

	opCtx, cancel := context.WithTimeout(ctx, rootOpTimeout)
	defer cancel()

	// Ensure alpine image is available
	_, _, err = cli.ImageInspectWithRaw(opCtx, alpineImage)
	if err != nil {
		pullReader, pullErr := cli.ImagePull(opCtx, alpineImage, types.ImagePullOptions{})
		if pullErr != nil {
			return fmt.Errorf("failed to pull alpine image: %w", pullErr)
		}
		defer pullReader.Close()
		io.Copy(io.Discard, pullReader) // Wait for pull to complete
	}

	// Create container that deletes the contents of the mounted path.
	// We delete /target/* and /target/.* (hidden files) but not /target itself
	// because /target is the mount point and cannot be removed from inside.
	resp, err := cli.ContainerCreate(opCtx,
		&container.Config{
			Image: alpineImage,
			Cmd:   []string{"sh", "-c", "rm -rf /target/* /target/.* 2>/dev/null; exit 0"},
		},
		&container.HostConfig{
			Binds: []string{absPath + ":/target"},
		},
		nil, nil, "",
	)
	if err != nil {
		return fmt.Errorf("failed to create container: %w", err)
	}

	// Always clean up the container
	defer func() {
		removeCtx, removeCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer removeCancel()
		cli.ContainerRemove(removeCtx, resp.ID, types.ContainerRemoveOptions{Force: true})
	}()

	// Start and wait for the container
	if err := cli.ContainerStart(opCtx, resp.ID, types.ContainerStartOptions{}); err != nil {
		return fmt.Errorf("failed to start container: %w", err)
	}

	statusCh, errCh := cli.ContainerWait(opCtx, resp.ID, container.WaitConditionNotRunning)
	select {
	case err := <-errCh:
		if err != nil {
			return fmt.Errorf("error waiting for container: %w", err)
		}
	case status := <-statusCh:
		if status.StatusCode != 0 {
			return fmt.Errorf("container exited with status %d", status.StatusCode)
		}
	case <-opCtx.Done():
		return fmt.Errorf("timeout waiting for container")
	}

	// Now remove the empty directory from the host
	if err := os.Remove(absPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove empty directory: %w", err)
	}

	return nil
}
