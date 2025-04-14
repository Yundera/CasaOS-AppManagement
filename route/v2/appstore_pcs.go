package v2

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/IceWhaleTech/CasaOS-AppManagement/codegen"
	"github.com/IceWhaleTech/CasaOS-Common/utils/logger"
	"github.com/compose-spec/compose-go/types"
	"go.uber.org/zap"
)

func updateConectivityAndStorageComposeData(composeR *codegen.ComposeApp) *codegen.ComposeApp {
	if composeR == nil {
		logger.Error("failed to modify compose data - nil value")
		return composeR
	}

	if !needsModification() {
		return composeR
	}

	compose := *composeR
	dataRoot := getEnvWithDefault("DATA_ROOT", "")
	refNet := getEnvWithDefault("REF_NET", "")
	refPort := getValidatedEnv("REF_PORT", "80", isValidPort)
	refDomain := getEnvWithDefault("REF_DOMAIN", "")
	refIp := getEnvWithDefault("REF_IP", "")
	refScheme := getEnvWithDefault("REF_SCHEME", "http")
	refSeparator := getEnvWithDefault("REF_SEPARATOR", "-")
	logger.Info("PCS: updateConectivityAndStorageComposeData",
		zap.String("DATA_ROOT", dataRoot),
		zap.String("REF_NET", refNet),
		zap.String("REF_PORT", refPort),
		zap.String("REF_DOMAIN", refDomain),
		zap.String("REF_IP", refIp),
		zap.String("REF_SCHEME", refScheme),
		zap.String("REF_SEPARATOR", refSeparator))

	// Update the x-casaos extensions setup scheme, port and hostname and build the webui link
	useDynamicWebUIPort := false
	if casaosExt, ok := compose.Extensions["x-casaos"]; ok {
		casaosExtensions, ok := casaosExt.(map[string]interface{})
		if !ok {
			logger.Error("PCS: invalid x-casaos extension format",
				zap.String("name", compose.Name),
				zap.Any("extensions", casaosExt))
			return composeR
		}

		extCopy := make(map[string]interface{})
		for k, v := range casaosExtensions {
			extCopy[k] = v
		}

		if len(compose.Services) == 0 {
			logger.Error("PCS: no services defined in compose",
				zap.String("name", compose.Name))
			return composeR
		}

		webuiExposePort := "80" // Default port
		if portVal, exists := extCopy["webui_port"]; exists && portVal != nil {
			// Safely convert the webui_port to a string
			switch v := portVal.(type) {
			case float64:
				if v > 0 && v < 65536 {
					webuiExposePort = strconv.Itoa(int(v))
				} else {
					logger.Info("PCS: invalid webui_port value",
						zap.String("name", compose.Name),
						zap.Float64("port", v))
				}
			case int:
				if v > 0 && v < 65536 {
					webuiExposePort = strconv.Itoa(v)
				} else {
					logger.Info("PCS: invalid webui_port value",
						zap.String("name", compose.Name),
						zap.Int("port", v))
				}
			case string:
				if port, err := strconv.Atoi(v); err == nil && port > 0 && port < 65536 {
					webuiExposePort = v
				} else {
					logger.Info("PCS: invalid webui_port string value",
						zap.String("name", compose.Name),
						zap.String("port", v))
				}
			default:
				logger.Info("PCS: unexpected webui_port type",
					zap.String("name", compose.Name),
					zap.Any("webui_port", portVal))
			}
		} else {
			useDynamicWebUIPort = true
			// Check if we have services and ports available
			if len(compose.Services) > 0 && len(compose.Services[0].Ports) > 0 {
				port := compose.Services[0].Ports[0].Target
				if port > 0 && port < 65536 {
					webuiExposePort = strconv.Itoa(int(port))
				} else {
					logger.Info("PCS: invalid port in service config, using default",
						zap.String("name", compose.Name),
						zap.Uint32("port", port))
				}
			} else {
				logger.Info("PCS: no ports defined for first service, using default",
					zap.String("name", compose.Name))
				if len(compose.Services) > 0 {
					logger.Info("Service without ports",
						zap.String("service", compose.Services[0].Name))
				}
			}
		}

		logger.Info("PCS: found webui expose port",
			zap.String("port", webuiExposePort),
			zap.String("name", compose.Name))

		extCopy["scheme"] = refScheme
		extCopy["port_map"] = refPort

		if refDomain != "" && isValidDomain(refDomain) {
			extCopy["hostname"] = fmt.Sprintf("%s%s%s%s%s",
				webuiExposePort, refSeparator,
				compose.Name, refSeparator,
				refDomain)
		} else if refDomain != "" {
			logger.Info("PCS: invalid domain name provided",
				zap.String("domain", refDomain))
		}

		compose.Extensions["x-casaos"] = extCopy
	}

	// Modify services if needed
	if dataRoot != "" || refNet != "" {
		if len(compose.Services) == 0 {
			logger.Error("PCS: no services to modify",
				zap.String("name", compose.Name))
			return composeR
		}

		servicesCopy := make([]types.ServiceConfig, len(compose.Services))
		for i, service := range compose.Services {
			servicesCopy[i] = service // Shallow copy of service

			if dataRoot != "" {
				servicesCopy[i].Volumes = filterVolumes(service.Volumes, dataRoot)
			}

			if useDynamicWebUIPort {
				// If the expose port has been set dynamically, we need to update the port to expose
				servicesCopy[i].Expose = convertPortsToExpose(service.Ports)
				servicesCopy[i].Ports = nil
			}

			if refNet != "" {
				networksCopy := make(types.Networks)
				networksCopy[refNet] = types.NetworkConfig{
					Name:     refNet,
					External: types.External{External: true},
				}
				compose.Networks = networksCopy

				servicesCopy[i].Hostname = compose.Name
				servicesCopy[i].NetworkMode = ""
				servicesCopy[i].Networks = map[string]*types.ServiceNetworkConfig{
					refNet: {},
				}
			}
		}
		compose.Services = servicesCopy
	}

	return &compose
}

func needsModification() bool {
	envVars := []string{"DATA_ROOT", "REF_NET", "REF_PORT", "REF_DOMAIN", "REF_SCHEME"}
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

func executePreInstallScript(composeApp *codegen.ComposeApp) error {
	if composeApp == nil {
		logger.Error("PCS: cannot execute pre-install script - nil compose app")
		return fmt.Errorf("nil compose app")
	}

	// Check if x-casaos extension exists
	casaosExt, ok := composeApp.Extensions["x-casaos"]
	if !ok {
		logger.Info("PCS: no x-casaos extension found, skipping pre-install script check")
		return nil
	}

	// Check if it's a map
	casaosExtensions, ok := casaosExt.(map[string]interface{})
	if !ok {
		logger.Error("PCS: invalid x-casaos extension format",
			zap.String("name", composeApp.Name),
			zap.Any("extensions", casaosExt))
		return fmt.Errorf("invalid x-casaos extension format")
	}

	// Check for pre-install-cmd
	preInstallCmd, exists := casaosExtensions["pre-install-cmd"]
	if !exists || preInstallCmd == nil {
		logger.Info("PCS: no pre-install-cmd found in x-casaos extension",
			zap.String("name", composeApp.Name))
		return nil
	}

	// Get the command value as string
	cmdString, ok := preInstallCmd.(string)
	if !ok || cmdString == "" {
		logger.Error("PCS: invalid pre-install-cmd value",
			zap.String("name", composeApp.Name),
			zap.Any("command", preInstallCmd))
		return fmt.Errorf("invalid pre-install-cmd value")
	}

	logger.Info("PCS: executing pre-install command",
		zap.String("name", composeApp.Name),
		zap.String("command", cmdString))

	// Create a more robust command execution
	execCmd := exec.Command("/bin/bash", "-c", cmdString)

	// Set environment variables that might be needed for Docker
	execCmd.Env = append(os.Environ(),
		"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
		"DOCKER_HOST=unix:///var/run/docker.sock")

	// Ensure the command has access to standard streams
	execCmd.Stdin = os.Stdin
	execCmd.Stdout = os.Stdout
	execCmd.Stderr = os.Stderr

	// Log command for debugging
	logger.Info("PCS: running command",
		zap.String("command", cmdString),
		zap.Strings("env", execCmd.Env))

	// Run the command interactively
	err := execCmd.Run()
	if err != nil {
		logger.Error("PCS: failed to execute pre-install command",
			zap.String("name", composeApp.Name),
			zap.String("command", cmdString),
			zap.Error(err))
		return fmt.Errorf("pre-install command execution failed: %w", err)
	}

	logger.Info("PCS: pre-install command executed successfully",
		zap.String("name", composeApp.Name),
		zap.String("command", cmdString))

	return nil
}
