package docker

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
)

const (
	alpineImage      = "alpine:latest"
	rootOpTimeout    = 30 * time.Second
	archiveOpTimeout = 120 * time.Second
)

// resolveHostPath translates a container-internal path to the corresponding
// host path by inspecting our own container's mount points. This is needed
// when CasaOS runs inside a Docker container — the Alpine cleanup container
// is created via the host Docker daemon so it needs host paths.
func resolveHostPath(ctx context.Context, cli *client.Client, containerPath string) string {
	hostname, err := os.Hostname()
	if err != nil {
		return containerPath
	}

	info, err := cli.ContainerInspect(ctx, hostname)
	if err != nil {
		return containerPath
	}

	for _, mount := range info.Mounts {
		if strings.HasPrefix(containerPath, mount.Destination) {
			return mount.Source + containerPath[len(mount.Destination):]
		}
	}

	return containerPath
}

// ensureAlpineImage pulls the alpine image if it's not already available.
func ensureAlpineImage(ctx context.Context, cli *client.Client) error {
	_, _, err := cli.ImageInspectWithRaw(ctx, alpineImage)
	if err != nil {
		pullReader, pullErr := cli.ImagePull(ctx, alpineImage, types.ImagePullOptions{})
		if pullErr != nil {
			return fmt.Errorf("failed to pull alpine image: %w", pullErr)
		}
		defer pullReader.Close()
		io.Copy(io.Discard, pullReader)
	}
	return nil
}

// runContainerAndWait creates a container, starts it, waits for completion,
// and cleans it up. Returns an error if the container fails.
func runContainerAndWait(ctx context.Context, cli *client.Client, config *container.Config, hostConfig *container.HostConfig) error {
	resp, err := cli.ContainerCreate(ctx, config, hostConfig, nil, nil, "")
	if err != nil {
		return fmt.Errorf("failed to create container: %w", err)
	}

	defer func() {
		removeCtx, removeCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer removeCancel()
		cli.ContainerRemove(removeCtx, resp.ID, types.ContainerRemoveOptions{Force: true})
	}()

	if err := cli.ContainerStart(ctx, resp.ID, types.ContainerStartOptions{}); err != nil {
		return fmt.Errorf("failed to start container: %w", err)
	}

	statusCh, errCh := cli.ContainerWait(ctx, resp.ID, container.WaitConditionNotRunning)
	select {
	case err := <-errCh:
		if err != nil {
			return fmt.Errorf("error waiting for container: %w", err)
		}
	case status := <-statusCh:
		if status.StatusCode != 0 {
			return fmt.Errorf("container exited with status %d", status.StatusCode)
		}
	case <-ctx.Done():
		return fmt.Errorf("timeout waiting for container")
	}

	return nil
}

// ArchivePathAsRoot creates a zip archive of the given directory using a Docker
// container running as root, then sets the archive ownership to the specified
// uid:gid so non-root users can manage it. The archive is saved to archiveDir.
func ArchivePathAsRoot(ctx context.Context, sourcePath string, archiveDir string, archiveName string) error {
	if sourcePath == "" {
		return fmt.Errorf("source path cannot be empty")
	}

	absSource, err := filepath.Abs(sourcePath)
	if err != nil {
		return fmt.Errorf("failed to resolve source path: %w", err)
	}

	if _, err := os.Stat(absSource); os.IsNotExist(err) {
		return nil
	}

	absArchiveDir, err := filepath.Abs(archiveDir)
	if err != nil {
		return fmt.Errorf("failed to resolve archive dir: %w", err)
	}

	// Create archive directory if it doesn't exist
	if err := os.MkdirAll(absArchiveDir, 0755); err != nil {
		return fmt.Errorf("failed to create archive directory: %w", err)
	}

	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return fmt.Errorf("failed to create docker client: %w", err)
	}
	defer cli.Close()

	opCtx, cancel := context.WithTimeout(ctx, archiveOpTimeout)
	defer cancel()

	hostSourcePath := resolveHostPath(opCtx, cli, absSource)
	hostArchiveDir := resolveHostPath(opCtx, cli, absArchiveDir)

	if err := ensureAlpineImage(opCtx, cli); err != nil {
		return err
	}

	// Get PUID/PGID from environment
	puid := os.Getenv("PUID")
	if puid == "" {
		puid = "1000"
	}
	pgid := os.Getenv("PGID")
	if pgid == "" {
		pgid = "1000"
	}

	// Create archive using Alpine container with zip
	cmd := fmt.Sprintf(
		"apk add --no-cache zip > /dev/null 2>&1 && "+
			"cd /source && zip -r /archive/%s . > /dev/null 2>&1 && "+
			"chown %s:%s /archive/%s",
		archiveName, puid, pgid, archiveName,
	)

	err = runContainerAndWait(opCtx, cli,
		&container.Config{
			Image: alpineImage,
			Cmd:   []string{"sh", "-c", cmd},
		},
		&container.HostConfig{
			Binds: []string{
				hostSourcePath + ":/source:ro",
				hostArchiveDir + ":/archive",
			},
		},
	)
	if err != nil {
		return fmt.Errorf("failed to archive path: %w", err)
	}

	return nil
}

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

	// Translate container-internal path to host path
	hostPath := resolveHostPath(opCtx, cli, absPath)

	if err := ensureAlpineImage(opCtx, cli); err != nil {
		return err
	}

	// Delete the contents of the mounted path using an Alpine container.
	// We delete /target/* and /target/.* (hidden files) but not /target itself
	// because /target is the mount point and cannot be removed from inside.
	err = runContainerAndWait(opCtx, cli,
		&container.Config{
			Image: alpineImage,
			Cmd:   []string{"sh", "-c", "rm -rf /target/* /target/.* 2>/dev/null; exit 0"},
		},
		&container.HostConfig{
			Binds: []string{hostPath + ":/target"},
		},
	)
	if err != nil {
		return fmt.Errorf("failed to remove path contents: %w", err)
	}

	// Now remove the empty directory from the host
	if err := os.Remove(absPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove empty directory: %w", err)
	}

	return nil
}
