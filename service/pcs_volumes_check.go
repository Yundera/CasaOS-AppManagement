package service

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/IceWhaleTech/CasaOS-AppManagement/common"
	"github.com/IceWhaleTech/CasaOS-Common/utils/logger"
	"go.uber.org/zap"
)

// PrepareVolumeDirectory creates directory and sets ownership for volume mounts in PCS environment
// Requirements:
// 1. Only process directories under DATA_ROOT
// 2. Only process paths ending with "/"  
// 3. Create directory with mkdir -p 
// 4. Set ownership to PUID:PGID if directory didn't already exist
// 5. Never fail the installation process
func PrepareVolumeDirectory(volumePath string) error {
	// Get environment variables with defaults
	dataRoot := getEnvWithDefault("DATA_ROOT", "")
	puid := getEnvWithDefault("PUID", common.DefaultPUID)
	pgid := getEnvWithDefault("PGID", common.DefaultPGID)

	// Skip if DATA_ROOT is not set
	if dataRoot == "" {
		return nil
	}

	// Skip if volume path doesn't start with DATA_ROOT
	if !strings.HasPrefix(volumePath, dataRoot) {
		return nil
	}

	// Skip if path doesn't end with "/" (to avoid creating files by mistake)
	if !strings.HasSuffix(volumePath, "/") {
		return nil
	}

	// Clean the path to remove any trailing slashes and normalize
	cleanPath := filepath.Clean(volumePath)
	
	// Check if directory already exists
	dirExists := false
	if stat, err := os.Stat(cleanPath); err == nil && stat.IsDir() {
		dirExists = true
	}

	// Create directory with mkdir -p behavior (create parent directories as needed)
	if err := os.MkdirAll(cleanPath, 0755); err != nil {
		logger.Error("PCS Volume: failed to create directory", 
			zap.String("path", cleanPath), 
			zap.Error(err))
		return fmt.Errorf("failed to create directory %s: %w", cleanPath, err)
	}

	// Only change ownership if the directory didn't already exist
	if !dirExists {
		// Parse PUID and PGID to integers
		uid, err := strconv.Atoi(puid)
		if err != nil {
			logger.Error("PCS Volume: invalid PUID", 
				zap.String("puid", puid), 
				zap.Error(err))
			return fmt.Errorf("invalid PUID %s: %w", puid, err)
		}

		gid, err := strconv.Atoi(pgid)
		if err != nil {
			logger.Error("PCS Volume: invalid PGID", 
				zap.String("pgid", pgid), 
				zap.Error(err))
			return fmt.Errorf("invalid PGID %s: %w", pgid, err)
		}

		// Change ownership to PUID:PGID
		if err := os.Chown(cleanPath, uid, gid); err != nil {
			logger.Error("PCS Volume: failed to change ownership", 
				zap.String("path", cleanPath), 
				zap.Int("puid", uid), 
				zap.Int("pgid", gid), 
				zap.Error(err))
			return fmt.Errorf("failed to change ownership of %s to %d:%d: %w", cleanPath, uid, gid, err)
		}

		logger.Info("PCS Volume: created directory and set ownership", 
			zap.String("path", cleanPath), 
			zap.String("puid", puid), 
			zap.String("pgid", pgid))
	}

	return nil
}

// getEnvWithDefault gets environment variable with fallback to default value
func getEnvWithDefault(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}