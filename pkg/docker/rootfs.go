package docker

import (
	"archive/zip"
	"context"
	"fmt"
	"io"
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

// ArchivePath creates a zip archive of the given directory using Go's
// archive/zip package. The archive is created as the current process user
// (non-root), so the resulting file is owned by the normal user. Files that
// cannot be read (e.g. root-owned) are skipped with a warning.
func ArchivePath(ctx context.Context, sourcePath string, archiveDir string, appName string, archiveName string) error {
	if sourcePath == "" {
		return fmt.Errorf("source path cannot be empty")
	}

	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return fmt.Errorf("failed to create docker client: %w", err)
	}
	defer cli.Close()

	absSource := resolveContainerPath(ctx, cli, sourcePath)
	absArchiveDir := resolveContainerPath(ctx, cli, archiveDir)

	if err := validatePath(absSource); err != nil {
		return fmt.Errorf("archive refused: %w", err)
	}

	if _, err := os.Stat(absSource); os.IsNotExist(err) {
		return nil
	}

	safeName := sanitizeShellArg(archiveName)
	archivePath := filepath.Join(absArchiveDir, safeName)

	zipFile, err := os.Create(archivePath)
	if err != nil {
		return fmt.Errorf("failed to create archive file: %w", err)
	}
	defer zipFile.Close()

	w := zip.NewWriter(zipFile)
	defer w.Close()

	err = filepath.Walk(absSource, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			logger.Info("skipping unreadable file", zap.String("path", path), zap.Error(err))
			return nil
		}

		relPath, err := filepath.Rel(absSource, path)
		if err != nil {
			return err
		}

		if relPath == "." {
			return nil
		}

		if info.IsDir() {
			_, err := w.Create(relPath + "/")
			return err
		}

		f, err := os.Open(path)
		if err != nil {
			logger.Info("skipping unreadable file", zap.String("path", path), zap.Error(err))
			return nil
		}
		defer f.Close()

		header, err := zip.FileInfoHeader(info)
		if err != nil {
			return err
		}
		header.Name = relPath
		header.Method = zip.Deflate

		writer, err := w.CreateHeader(header)
		if err != nil {
			return err
		}

		_, err = io.Copy(writer, f)
		return err
	})

	if err != nil {
		os.Remove(archivePath)
		return fmt.Errorf("failed to archive path: %w", err)
	}

	pruneOldArchives(absArchiveDir, appName, maxArchives)

	return nil
}

// pruneOldArchives removes the oldest archive zip files for a specific app
// when the count exceeds maxKeep. Only matches files with the exact pattern
// {appName}_{YYYYMMDD}_{HHMMSS}.zip to avoid touching user files.
func pruneOldArchives(dir string, appName string, maxKeep int) {
	pattern := regexp.MustCompile(`^` + regexp.QuoteMeta(appName) + `_\d{8}_\d{6}\.zip$`)

	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}

	var zips []os.DirEntry
	for _, e := range entries {
		if !e.IsDir() && pattern.MatchString(e.Name()) {
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
