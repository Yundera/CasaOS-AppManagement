package v2

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/IceWhaleTech/CasaOS-AppManagement/codegen"
	"github.com/IceWhaleTech/CasaOS-AppManagement/pkg/install_cmd"
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

	// Get PUID and PGID for user rights management
	puid := getEnvWithDefault("PUID", "1000")
	pgid := getEnvWithDefault("PGID", "1000")

	logger.Info("PCS: updateConectivityAndStorageComposeData",
		zap.String("DATA_ROOT", dataRoot),
		zap.String("REF_NET", refNet),
		zap.String("PUID", puid),
		zap.String("PGID", pgid))

	// Update the x-casaos extensions
	updateCasaOSExtensions(&compose)

	// Modify services if needed
	if dataRoot != "" || refNet != "" || shouldAddUserRights(puid, pgid) {
		if len(compose.Services) == 0 {
			logger.Error("PCS: no services to modify",
				zap.String("name", compose.Name))
			return composeR
		}

		modifyServices(&compose, dataRoot, refNet, puid, pgid)
	}

	return &compose
}

func updateCasaOSExtensions(compose *codegen.ComposeApp) {

	// Read environment variables inside the function
	refScheme := getEnvWithDefault("REF_SCHEME", "http")
	refPort := getValidatedEnv("REF_PORT", "80", isValidPort)
	refDomain := getEnvWithDefault("REF_DOMAIN", "")
	refSeparator := getEnvWithDefault("REF_SEPARATOR", "-")
	refDefaultPort := getEnvWithDefault("REF_DEFAULT_PORT", "80")

	logger.Info("PCS: updateCasaOSExtensions - environment variables",
		zap.String("name", compose.Name),
		zap.String("REF_SCHEME", refScheme),
		zap.String("REF_PORT", refPort),
		zap.String("REF_DOMAIN", refDomain),
		zap.String("REF_SEPARATOR", refSeparator),
		zap.String("REF_DEFAULT_PORT", refDefaultPort))

	casaosExt, ok := compose.Extensions["x-casaos"]
	if !ok {
		return
	}

	casaosExtensions, ok := casaosExt.(map[string]interface{})
	if !ok {
		logger.Error("PCS: invalid x-casaos extension format",
			zap.String("name", compose.Name),
			zap.Any("extensions", casaosExt))
		return
	}

	extCopy := make(map[string]interface{})
	for k, v := range casaosExtensions {
		extCopy[k] = v
	}

	if len(compose.Services) == 0 {
		logger.Error("PCS: no services defined in compose",
			zap.String("name", compose.Name))
		return
	}

	webuiExposePort := determineWebUIPort(extCopy, compose)

	logger.Info("PCS: found webui expose port",
		zap.String("port", webuiExposePort),
		zap.String("name", compose.Name))

	// Only set scheme if not already defined
	if _, exists := extCopy["scheme"]; !exists {
		extCopy["scheme"] = refScheme
	}

	// Only set port_map if not already defined
	if _, exists := extCopy["port_map"]; !exists {
		extCopy["port_map"] = refPort
	}

	// Only set hostname if not already defined and refDomain is provided
	if _, exists := extCopy["hostname"]; !exists && refDomain != "" && isValidDomain(refDomain) {
		// Check if the webui port matches the default port
		if webuiExposePort == refDefaultPort {
			// Use format without port prefix: service-domain
			extCopy["hostname"] = fmt.Sprintf("%s%s%s",
				compose.Name, refSeparator,
				refDomain)
			logger.Info("PCS: using default port, hostname without port prefix",
				zap.String("hostname", extCopy["hostname"].(string)),
				zap.String("port", webuiExposePort),
				zap.String("default_port", refDefaultPort))
		} else {
			// Use original format with port prefix: port-service-domain
			extCopy["hostname"] = fmt.Sprintf("%s%s%s%s%s",
				webuiExposePort, refSeparator,
				compose.Name, refSeparator,
				refDomain)
			logger.Info("PCS: using non-default port, hostname with port prefix",
				zap.String("hostname", extCopy["hostname"].(string)),
				zap.String("port", webuiExposePort),
				zap.String("default_port", refDefaultPort))
		}
	} else if refDomain != "" && !isValidDomain(refDomain) {
		logger.Info("PCS: invalid domain name provided",
			zap.String("domain", refDomain))
	}

	// Expand environment variables in tips.before_install
	expandTipsBeforeInstall(extCopy, compose.Name)

	compose.Extensions["x-casaos"] = extCopy
}

// expandTipsBeforeInstall expands environment variables in the tips.before_install field
func expandTipsBeforeInstall(extCopy map[string]interface{}, appName string) {
	// Check if tips exists
	tipsInterface, hasTips := extCopy["tips"]
	if !hasTips {
		return
	}

	tips, ok := tipsInterface.(map[string]interface{})
	if !ok {
		logger.Error("PCS: invalid tips format in x-casaos extension",
			zap.String("name", appName),
			zap.Any("tips", tipsInterface))
		return
	}

	// Check if before_install exists
	beforeInstallInterface, hasBeforeInstall := tips["before_install"]
	if !hasBeforeInstall {
		return
	}

	beforeInstall, ok := beforeInstallInterface.(map[string]interface{})
	if !ok {
		logger.Error("PCS: invalid before_install format in tips",
			zap.String("name", appName),
			zap.Any("before_install", beforeInstallInterface))
		return
	}

	// Expand environment variables in each before_install entry
	for lang, valueInterface := range beforeInstall {
		if value, ok := valueInterface.(string); ok {
			expandedValue := expandEnvVars(value)
			if expandedValue != value {
				logger.Info("PCS: expanded environment variables in tips.before_install",
					zap.String("name", appName),
					zap.String("language", lang),
					zap.String("original", value),
					zap.String("expanded", expandedValue))
			}
			beforeInstall[lang] = expandedValue
		}
	}
}

// expandEnvVars expands environment variables in the format $VAR or ${VAR}
func expandEnvVars(text string) string {
	// Use os.Expand which handles both $VAR and ${VAR} formats
	// and automatically uses all available OS environment variables
	return os.Expand(text, os.Getenv)
}

func determineWebUIPort(extCopy map[string]interface{}, compose *codegen.ComposeApp) string {
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
	}
	return webuiExposePort
}

func getMainServiceName(compose *codegen.ComposeApp) string {
	casaosExt, ok := compose.Extensions["x-casaos"]
	if !ok {
		return ""
	}

	casaosExtensions, ok := casaosExt.(map[string]interface{})
	if !ok {
		return ""
	}

	mainService, exists := casaosExtensions["main"]
	if !exists {
		return ""
	}

	mainServiceName, ok := mainService.(string)
	if !ok {
		return ""
	}

	return mainServiceName
}

func modifyServices(compose *codegen.ComposeApp, dataRoot, refNet string, puid, pgid string) {
	servicesCopy := make([]types.ServiceConfig, len(compose.Services))
	mainServiceName := getMainServiceName(compose)

	logger.Info("PCS: identified main service for refNet configuration",
		zap.String("mainService", mainServiceName),
		zap.String("refNet", refNet))

	for i, service := range compose.Services {
		servicesCopy[i] = service // Shallow copy of service

		if dataRoot != "" {
			servicesCopy[i].Volumes = filterVolumes(service.Volumes, dataRoot)
		}

		// Add user rights management
		if shouldAddUserToService(&servicesCopy[i], puid, pgid) {
			userString := fmt.Sprintf("%s:%s", puid, pgid)
			servicesCopy[i].User = userString
			logger.Info("PCS: added user rights to service",
				zap.String("service", service.Name),
				zap.String("user", userString))
		}

		// If NetworkMode is set, skip network-related operations
		if service.NetworkMode != "bridge" && service.NetworkMode != "" {
			logger.Info("PCS: NetworkMode is set, skipping network configuration",
				zap.String("service", service.Name),
				zap.String("network_mode", service.NetworkMode))
		} else {
			// Only apply refNet to the main service
			if refNet != "" && mainServiceName != "" && service.Name == mainServiceName {
				// Add refNet to compose networks (preserve existing networks)
				if compose.Networks == nil {
					compose.Networks = make(types.Networks)
				}
				compose.Networks[refNet] = types.NetworkConfig{
					Name:     refNet,
					External: types.External{External: true},
				}

				// Add refNet to service networks (preserve existing networks)
				if servicesCopy[i].Networks == nil {
					servicesCopy[i].Networks = make(map[string]*types.ServiceNetworkConfig)
				}
				servicesCopy[i].Networks[refNet] = &types.ServiceNetworkConfig{}
				
				// Remove network_mode when networks are defined to avoid Docker Compose validation error
				// "service declares mutually exclusive `network_mode` and `networks`: invalid compose project"
				if servicesCopy[i].NetworkMode != "" {
					logger.Info("PCS: removing network_mode from main service to avoid conflict with networks",
						zap.String("service", service.Name),
						zap.String("removed_network_mode", servicesCopy[i].NetworkMode))
					servicesCopy[i].NetworkMode = ""
				}
				
				// Only set hostname if not already set
				if servicesCopy[i].Hostname == "" {
					servicesCopy[i].Hostname = compose.Name
				}
				
				logger.Info("PCS: added refNet to main service (preserving existing networks)",
					zap.String("service", service.Name),
					zap.String("refNet", refNet),
					zap.Any("existingNetworks", servicesCopy[i].Networks))
			} else if refNet != "" && service.Name != mainServiceName {
				logger.Info("PCS: skipping refNet for non-main service",
					zap.String("service", service.Name),
					zap.String("mainService", mainServiceName),
					zap.String("refNet", refNet))
			}
		}
	}

	compose.Services = servicesCopy
}

func executePreInstallScript(composeApp *codegen.ComposeApp) error {
	// First, ensure AppData folders are created with proper ownership
	if composeApp != nil && composeApp.Name != "" {
		dataRoot := getEnvWithDefault("DATA_ROOT", "/DATA")
		puid := getEnvWithDefault("PUID", "1000")
		pgid := getEnvWithDefault("PGID", "1000")

		// Folders to create
		folders := []string{
			filepath.Join(dataRoot, "AppData", composeApp.Name),
			filepath.Join(dataRoot, "AppData", "casaos", "apps", composeApp.Name),
		}

		// Convert PUID/PGID to integers once
		uid, uidErr := strconv.Atoi(puid)
		gid, gidErr := strconv.Atoi(pgid)

		for _, folder := range folders {
			// Create the directory
			if err := os.MkdirAll(folder, 0755); err != nil {
				logger.Error("PCS: failed to create AppData folder",
					zap.String("folder", folder),
					zap.Error(err))
			} else {
				// Set ownership if PUID/PGID are valid
				if uidErr == nil && gidErr == nil {
					if err := os.Chown(folder, uid, gid); err != nil {
						logger.Error("PCS: failed to set ownership for AppData folder",
							zap.String("folder", folder),
							zap.Error(err))
					} else {
						logger.Info("PCS: created AppData folder",
							zap.String("folder", folder),
							zap.String("puid", puid),
							zap.String("pgid", pgid))
					}
				}
			}
		}
	}

	// Then execute any pre-install commands
	return install_cmd.ExecutePreInstallScript(composeApp)
}

func executePostInstallScript(composeApp *codegen.ComposeApp) error {
	return install_cmd.ExecutePostInstallScript(composeApp)
}
