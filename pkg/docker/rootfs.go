package docker

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/IceWhaleTech/CasaOS-Common/utils/logger"
	"github.com/docker/docker/api/types"
	"github.com/docker/docker/client"
	"go.uber.org/zap"
)

const (
	rootOpTimeout    = 30 * time.Second
	archiveOpTimeout = 120 * time.Second
	maxArchives      = 10
)

var allowedPathPrefixes = []string{
	"/DATA/",
	"/data/",
}

var safeShellChars = regexp.MustCompile(`[^a-zA-Z0-9._-]`)

func sanitizeShellArg(s string) string {
	return safeShellChars.ReplaceAllString(s, "_")
}

func validatePath(absPath string) error {
	cleaned := filepath.Clean(absPath)
	for _, prefix := range allowedPathPrefixes {
		if strings.HasPrefix(cleaned, prefix) {
			return nil
		}
	}
	return fmt.Errorf("path %q is outside allowed directories", absPath)
}

// resolveContainerPath translates a path (which may originate from a compose
// volume source or host-perspective path) to the corresponding container-internal
// path by inspecting our own container's mount points. This is the reverse of
// the host-path resolution — needed because docker exec runs inside our container.
func resolveContainerPath(ctx context.Context, cli *client.Client, path string) string {
	// If path already starts with a known mount destination, return as-is
	hostname, err := os.Hostname()
	if err != nil {
		return path
	}

	info, err := cli.ContainerInspect(ctx, hostname)
	if err != nil {
		return path
	}

	// Check if path already matches a mount destination
	for _, mount := range info.Mounts {
		if strings.HasPrefix(path, mount.Destination+"/") || path == mount.Destination {
			return path
		}
	}

	// Path doesn't match any mount destination directly.
	// Find the mount whose Destination appears within the path and remap.
	// e.g., path="/c/DATA/AppData/foo" with mount Destination="/DATA"
	// → found at index 2 → return "/DATA/AppData/foo"
	for _, mount := range info.Mounts {
		idx := strings.Index(path, mount.Destination+"/")
		if idx > 0 {
			return path[idx:]
		}
	}

	return path
}

// execAsRoot runs a command as root inside the CasaOS container itself
// via docker exec. The CasaOS container runs s6-overlay as root,
// so exec with User:"root" works.
func execAsRoot(ctx context.Context, cli *client.Client, cmd []string) error {
	hostname, err := os.Hostname()
	if err != nil {
		return fmt.Errorf("failed to get hostname: %w", err)
	}

	ir, err := cli.ContainerExecCreate(ctx, hostname, types.ExecConfig{
		Cmd:  cmd,
		User: "root",
	})
	if err != nil {
		return fmt.Errorf("failed to create exec: %w", err)
	}

	if err := cli.ContainerExecStart(ctx, ir.ID, types.ExecStartCheck{Detach: true}); err != nil {
		return fmt.Errorf("failed to start exec: %w", err)
	}

	for {
		inspect, err := cli.ContainerExecInspect(ctx, ir.ID)
		if err != nil {
			return fmt.Errorf("failed to inspect exec: %w", err)
		}
		if !inspect.Running {
			if inspect.ExitCode != 0 {
				return fmt.Errorf("command exited with status %d", inspect.ExitCode)
			}
			return nil
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf("timeout waiting for exec to complete")
		case <-time.After(200 * time.Millisecond):
		}
	}
}

// ArchivePathAsRoot creates a zip archive of the given directory by running
// zip as root inside the CasaOS container. The archive is saved to archiveDir
// and ownership is set to PUID:PGID so non-root users can manage it.
func ArchivePathAsRoot(ctx context.Context, sourcePath string, archiveDir string, archiveName string) error {
	if sourcePath == "" {
		return fmt.Errorf("source path cannot be empty")
	}

	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return fmt.Errorf("failed to create docker client: %w", err)
	}
	defer cli.Close()

	opCtx, cancel := context.WithTimeout(ctx, archiveOpTimeout)
	defer cancel()

	absSource := resolveContainerPath(opCtx, cli, sourcePath)
	absArchiveDir := resolveContainerPath(opCtx, cli, archiveDir)

	logger.Info("ArchivePathAsRoot", zap.String("source", absSource), zap.String("archiveDir", absArchiveDir), zap.String("name", archiveName))

	if err := validatePath(absSource); err != nil {
		return fmt.Errorf("root archive refused: %w", err)
	}

	if _, err := os.Stat(absSource); os.IsNotExist(err) {
		return nil
	}

	// Create archive directory as root
	if err := execAsRoot(opCtx, cli, []string{"mkdir", "-p", absArchiveDir}); err != nil {
		return fmt.Errorf("failed to create archive directory: %w", err)
	}

	puid := os.Getenv("PUID")
	if puid == "" {
		puid = "1000"
	}
	pgid := os.Getenv("PGID")
	if pgid == "" {
		pgid = "1000"
	}

	safeName := sanitizeShellArg(archiveName)
	safePuid := sanitizeShellArg(puid)
	safePgid := sanitizeShellArg(pgid)
	archivePath := filepath.Join(absArchiveDir, safeName)

	cmd := fmt.Sprintf(
		"cd %s && zip -r %s . > /dev/null 2>&1 && chown %s:%s %s",
		absSource, archivePath, safePuid, safePgid, archivePath,
	)

	if err := execAsRoot(opCtx, cli, []string{"sh", "-c", cmd}); err != nil {
		return fmt.Errorf("failed to archive path: %w", err)
	}

	pruneOldArchives(absArchiveDir, maxArchives)

	return nil
}

// pruneOldArchives removes the oldest zip files in dir when the count
// exceeds maxKeep. Files are sorted by modification time (oldest first).
func pruneOldArchives(dir string, maxKeep int) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}

	var zips []os.DirEntry
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".zip") {
			zips = append(zips, e)
		}
	}

	if len(zips) <= maxKeep {
		return
	}

	sort.Slice(zips, func(i, j int) bool {
		fi, _ := zips[i].Info()
		fj, _ := zips[j].Info()
		if fi == nil || fj == nil {
			return false
		}
		return fi.ModTime().Before(fj.ModTime())
	})

	for _, z := range zips[:len(zips)-maxKeep] {
		os.Remove(filepath.Join(dir, z.Name()))
	}
}

// RemovePathAsRoot removes a directory and all its contents by running
// rm as root inside the CasaOS container via docker exec. This solves
// the problem where volume folders created by containers running as root
// cannot be deleted by the non-root CasaOS process.
func RemovePathAsRoot(ctx context.Context, path string) error {
	if path == "" {
		return fmt.Errorf("path cannot be empty")
	}

	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return fmt.Errorf("failed to create docker client: %w", err)
	}
	defer cli.Close()

	opCtx, cancel := context.WithTimeout(ctx, rootOpTimeout)
	defer cancel()

	absPath := resolveContainerPath(opCtx, cli, path)

	logger.Info("RemovePathAsRoot", zap.String("path", absPath))

	if err := validatePath(absPath); err != nil {
		return fmt.Errorf("root removal refused: %w", err)
	}

	if _, err := os.Stat(absPath); os.IsNotExist(err) {
		return nil
	}

	if err := execAsRoot(opCtx, cli, []string{"rm", "-rf", absPath}); err != nil {
		return fmt.Errorf("failed to remove path: %w", err)
	}

	return nil
}
