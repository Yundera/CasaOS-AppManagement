package v2

import (
	"os"
	"strconv"
	"strings"

	"github.com/IceWhaleTech/CasaOS-Common/utils/logger"
	"github.com/compose-spec/compose-go/types"
	"go.uber.org/zap"
)

func needsModification() bool {
	envVars := []string{"DATA_ROOT", "REF_NET", "REF_PORT", "REF_DOMAIN", "REF_SCHEME", "PUID", "PGID"}
	for _, env := range envVars {
		if os.Getenv(env) != "" {
			return true
		}
	}
	return false
}

func getEnvWithDefault(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

// getValidatedEnv retrieves an environment variable with validation
func getValidatedEnv(key, defaultValue string, validator func(string) bool) string {
	value := os.Getenv(key)
	if value != "" {
		if validator(value) {
			return value
		}
		logger.Info("Invalid environment variable value",
			zap.String("key", key),
			zap.String("value", value),
			zap.String("using_default", defaultValue))
	}
	return defaultValue
}

// isValidPort checks if a string represents a valid port number
func isValidPort(s string) bool {
	port, err := strconv.Atoi(s)
	return err == nil && port > 0 && port < 65536
}

// isValidDomain performs basic validation on domain names
func isValidDomain(domain string) bool {
	return len(domain) > 0 && !strings.ContainsAny(domain, " \t\n\r")
}

func filterVolumes(volumes []types.ServiceVolumeConfig, dataRoot string) []types.ServiceVolumeConfig {
	if len(volumes) == 0 {
		return []types.ServiceVolumeConfig{}
	}

	// Count matching volumes first to allocate correct capacity
	matchCount := 0
	for _, volume := range volumes {
		if strings.HasPrefix(volume.Source, "/DATA") {
			matchCount++
		}
	}

	filtered := make([]types.ServiceVolumeConfig, 0, matchCount)
	for _, volume := range volumes {
		if strings.HasPrefix(volume.Source, "/DATA") {
			volumeCopy := volume
			volumeCopy.Source = strings.Replace(volume.Source, "/DATA", dataRoot, -1)
			filtered = append(filtered, volumeCopy)
		}
	}
	return filtered
}

func convertPortsToExpose(ports []types.ServicePortConfig) []string {
	if len(ports) == 0 {
		return []string{}
	}

	expose := make([]string, 0, len(ports))
	for _, port := range ports {
		if port.Target > 0 && port.Target < 65536 {
			expose = append(expose, strconv.Itoa(int(port.Target)))
		} else {
			logger.Info("Skipping invalid port in convertPortsToExpose",
				zap.Uint32("port", port.Target))
		}
	}
	return expose
}
