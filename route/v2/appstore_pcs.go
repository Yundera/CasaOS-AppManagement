package v2

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/IceWhaleTech/CasaOS-AppManagement/codegen"
	"github.com/IceWhaleTech/CasaOS-Common/utils/logger"
	"github.com/compose-spec/compose-go/types"
	"go.uber.org/zap"
)

func modifyComposeData(composeR *codegen.ComposeApp) *codegen.ComposeApp {
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
	refPort := getEnvWithDefault("REF_PORT", "80")
	refDomain := getEnvWithDefault("REF_DOMAIN", "")
	refIp := getEnvWithDefault("REF_IP", "")
	refpwd := getEnvWithDefault("REF_DEFAULT_PWD", "")
	refScheme := getEnvWithDefault("REF_SCHEME", "http")
	refSeparator := getEnvWithDefault("REF_SEPARATOR", "-")
	logger.Info("PCS: update compose with",
		zap.String("DATA_ROOT", dataRoot),
		zap.String("REF_NET", refNet),
		zap.String("REF_PORT", refPort),
		zap.String("REF_DOMAIN", refDomain),
		zap.String("REF_IP", refIp),
		zap.String("REF_DEFAULT_PWD", refpwd),
		zap.String("REF_SCHEME", refScheme),
		zap.String("REF_SEPARATOR", refSeparator))

	// Define variable replacements
	replacements := map[string]string{
		"$public_ip":   refIp,
		"$default_pwd": refpwd,
	}

	// Apply all replacements to services
	for i := range compose.Services {
		applyReplacementsToService(&compose.Services[i], replacements)
	}

	// Update the x-casaos extensions setup scheme, port and hostname for webui link
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
		if extCopy["hostname"] != "" && extCopy["scheme"] != "" {
			if len(compose.Services) == 0 {
				logger.Error("PCS: no services defined in compose",
					zap.String("name", compose.Name))
				return composeR
			}

			webuiExposePort := ""
			if extCopy["webui_port"] != nil {
				webuiExposePort = strconv.Itoa(int(extCopy["webui_port"].(float64)))
			} else {
				useDynamicWebUIPort = true
				if len(compose.Services[0].Ports) == 0 {
					logger.Error("PCS: no ports defined for first service",
						zap.String("name", compose.Name),
						zap.String("service", compose.Services[0].Name))
					return composeR
				}
				webuiExposePort = strconv.Itoa(int(compose.Services[0].Ports[0].Target))
			}

			logger.Info("PCS: found webui expose port",
				zap.String("port", webuiExposePort),
				zap.String("name", compose.Name))

			extCopy["scheme"] = refScheme
			extCopy["port_map"] = refPort

			if refDomain != "" {
				extCopy["hostname"] = fmt.Sprintf("%s%s%s%s%s",
					webuiExposePort, refSeparator,
					compose.Name, refSeparator,
					refDomain)
			}

			compose.Extensions["x-casaos"] = extCopy
		}
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

			if(useDynamicWebUIPort){
				//if the expose port has been set dynamicaly, we need to update the port to expose
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

// applyReplacementsToService applies string replacements to a service configuration
func applyReplacementsToService(service *types.ServiceConfig, replacements map[string]string) {
	// Skip empty replacements
	filteredReplacements := make(map[string]string)
	for placeholder, value := range replacements {
		if value != "" {
			filteredReplacements[placeholder] = value
		}
	}

	if len(filteredReplacements) == 0 {
		return
	}

	// Replace in environment variables
	if len(service.Environment) > 0 {
		for k, v := range service.Environment {
			if v != nil {
				strValue := *v
				modified := false

				for placeholder, replacement := range filteredReplacements {
					if strings.Contains(strValue, placeholder) {
						strValue = strings.ReplaceAll(strValue, placeholder, replacement)
						modified = true
					}
				}

				if modified {
					service.Environment[k] = &strValue
				}
			}
		}
	}

	// Replace in command if it exists
	if service.Command != nil {
		for j, cmd := range service.Command {
			modified := false
			newCmd := cmd

			for placeholder, replacement := range filteredReplacements {
				if strings.Contains(cmd, placeholder) {
					newCmd = strings.ReplaceAll(newCmd, placeholder, replacement)
					modified = true
				}
			}

			if modified {
				service.Command[j] = newCmd
			}
		}
	}

	// Replace in entrypoint if it exists
	if service.Entrypoint != nil {
		for j, entry := range service.Entrypoint {
			modified := false
			newEntry := entry

			for placeholder, replacement := range filteredReplacements {
				if strings.Contains(entry, placeholder) {
					newEntry = strings.ReplaceAll(newEntry, placeholder, replacement)
					modified = true
				}
			}

			if modified {
				service.Entrypoint[j] = newEntry
			}
		}
	}

	// Replace in labels
	for label, value := range service.Labels {
		modified := false
		newValue := value

		for placeholder, replacement := range filteredReplacements {
			if strings.Contains(value, placeholder) {
				newValue = strings.ReplaceAll(newValue, placeholder, replacement)
				modified = true
			}
		}

		if modified {
			service.Labels[label] = newValue
		}
	}
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

func filterVolumes(volumes []types.ServiceVolumeConfig, dataRoot string) []types.ServiceVolumeConfig {
	if len(volumes) == 0 {
		return []types.ServiceVolumeConfig{}
	}

	filtered := make([]types.ServiceVolumeConfig, 0, len(volumes))
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

	expose := make([]string, len(ports))
	for i, port := range ports {
		expose[i] = strconv.Itoa(int(port.Target))
	}
	return expose
}

//
