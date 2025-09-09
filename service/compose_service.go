package service

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/IceWhaleTech/CasaOS-AppManagement/codegen"
	"github.com/IceWhaleTech/CasaOS-AppManagement/common"
	"github.com/IceWhaleTech/CasaOS-AppManagement/pkg/auth"
	"github.com/IceWhaleTech/CasaOS-AppManagement/pkg/config"
	"github.com/IceWhaleTech/CasaOS-AppManagement/pkg/install_cmd"
	"github.com/IceWhaleTech/CasaOS-Common/utils/file"
	"github.com/IceWhaleTech/CasaOS-Common/utils/logger"
	timeutils "github.com/IceWhaleTech/CasaOS-Common/utils/time"
	"gopkg.in/yaml.v3"

	"github.com/docker/cli/cli/command"
	"github.com/docker/cli/cli/flags"
	"github.com/docker/compose/v2/pkg/api"
	"github.com/docker/compose/v2/pkg/compose"
	"github.com/docker/docker/client"

	"go.uber.org/zap"
)

type ComposeService struct {
	installationInProgress sync.Map
}

func (s *ComposeService) PrepareWorkingDirectory(name string) (string, error) {
	workingDirectory := filepath.Join(config.AppInfo.AppsPath, name)

	if err := file.IsNotExistMkDir(workingDirectory); err != nil {
		logger.Error("failed to create working dir", zap.Error(err), zap.String("path", workingDirectory))
		return "", err
	}

	return workingDirectory, nil
}

func (s *ComposeService) IsInstalling(appName string) bool {
	_, ok := s.installationInProgress.Load(appName)
	return ok
}

func (s *ComposeService) Install(ctx context.Context, composeApp *ComposeApp) error {
	// set store_app_id (by convention is the same as app name at install time if it does not exist)
	_, isStoreApp := composeApp.SetStoreAppID(composeApp.Name)
	if !isStoreApp {
		logger.Info("the compose app getting installed is not a store app, skipping store app id setting.")
	}

	logger.Info("installing compose app", zap.String("name", composeApp.Name))

	// Marshal to YAML first
	composeYAML, err := yaml.Marshal(composeApp)
	if err != nil {
		return err
	}

	// Interpolate AUTH_HASH and other variables in the YAML string
	composeYAMLInterpolated := s.interpolateInstallTimeVariables(string(composeYAML), composeApp.Name)

	workingDirectory, err := s.PrepareWorkingDirectory(composeApp.Name)
	if err != nil {
		return err
	}

	yamlFilePath := filepath.Join(workingDirectory, common.ComposeYAMLFileName)

	if err := os.WriteFile(yamlFilePath, []byte(composeYAMLInterpolated), 0o600); err != nil {
		logger.Error("failed to save compose file", zap.Error(err), zap.String("path", yamlFilePath))

		if err := file.RMDir(workingDirectory); err != nil {
			logger.Error("failed to cleanup working dir after failing to save compose file", zap.Error(err), zap.String("path", workingDirectory))
		}
		return err
	}

	// load project
	composeApp, err = LoadComposeAppFromConfigFile(composeApp.Name, yamlFilePath)

	if err != nil {
		logger.Error("failed to install compose app", zap.Error(err), zap.String("name", composeApp.Name))
		cleanup(workingDirectory)
		return err
	}

	// prepare for message bus events
	eventProperties := common.PropertiesFromContext(ctx)
	eventProperties[common.PropertyTypeAppName.Name] = composeApp.Name

	if err := composeApp.UpdateEventPropertiesFromStoreInfo(eventProperties); err != nil {
		logger.Info("failed to update event properties from store info", zap.Error(err), zap.String("name", composeApp.Name))
	}

	go func(ctx context.Context) {
		s.installationInProgress.Store(composeApp.Name, true)
		defer func() {
			s.installationInProgress.Delete(composeApp.Name)
		}()

		go PublishEventWrapper(ctx, common.EventTypeAppInstallBegin, nil)

		defer PublishEventWrapper(ctx, common.EventTypeAppInstallEnd, nil)

		if err := composeApp.PullAndInstall(ctx); err != nil {
			go PublishEventWrapper(ctx, common.EventTypeAppInstallError, map[string]string{
				common.PropertyTypeMessage.Name: err.Error(),
			})

			logger.Error("failed to install compose app", zap.Error(err), zap.String("name", composeApp.Name))
		} else {
			// Execute post-install command after successful installation
			if err := install_cmd.ExecutePostInstallScript((*codegen.ComposeApp)(composeApp)); err != nil {
				logger.Error("failed to execute post-install command, but installation was successful", zap.Error(err), zap.String("name", composeApp.Name))
				// Don't fail the installation if post-install command fails
			}
		}
	}(ctx)

	return nil
}

func (s *ComposeService) Uninstall(ctx context.Context, composeApp *ComposeApp, deleteConfigFolder bool) error {
	// prepare for message bus events
	eventProperties := common.PropertiesFromContext(ctx)
	eventProperties[common.PropertyTypeAppName.Name] = composeApp.Name

	if err := composeApp.UpdateEventPropertiesFromStoreInfo(eventProperties); err != nil {
		logger.Info("failed to update event properties from store info", zap.Error(err), zap.String("name", composeApp.Name))
	}

	go func(ctx context.Context) {
		go PublishEventWrapper(ctx, common.EventTypeAppUninstallBegin, nil)

		defer PublishEventWrapper(ctx, common.EventTypeAppUninstallEnd, nil)

		if err := composeApp.Uninstall(ctx, deleteConfigFolder); err != nil {
			go PublishEventWrapper(ctx, common.EventTypeAppUninstallError, map[string]string{
				common.PropertyTypeMessage.Name: err.Error(),
			})

			logger.Error("failed to uninstall compose app", zap.Error(err), zap.String("name", composeApp.Name))
		}
	}(ctx)

	return nil
}

func (s *ComposeService) Status(ctx context.Context, appID string) (string, error) {
	service, dockerClient, err := apiService()
	if err != nil {
		return "", err
	}
	defer dockerClient.Close()

	stackList, err := service.List(ctx, api.ListOptions{
		All: true,
	})
	if err != nil {
		return "", err
	}

	for _, stack := range stackList {
		if stack.ID == appID {
			return stack.Status, nil
		}
	}

	return "", ErrComposeAppNotFound
}

func (s *ComposeService) List(ctx context.Context) (map[string]*ComposeApp, error) {
	service, dockerClient, err := apiService()
	if err != nil {
		return nil, err
	}
	defer dockerClient.Close()

	stackList, err := service.List(ctx, api.ListOptions{
		All: true,
	})
	if err != nil {
		return nil, err
	}

	result := map[string]*ComposeApp{}

	for _, stack := range stackList {

		composeApp, err := LoadComposeAppFromConfigFile(stack.ID, stack.ConfigFiles)
		// load project
		if err != nil {
			logger.Error("failed to load compose file", zap.Error(err), zap.String("path", stack.ConfigFiles))
			continue
		}

		result[stack.ID] = composeApp
	}

	return result, nil
}

func NewComposeService() *ComposeService {
	return &ComposeService{
		installationInProgress: sync.Map{},
	}
}


// interpolateInstallTimeVariables replaces installation-time variables with their actual values
// This ensures that AUTH_HASH is persisted with its generated value
// This works on the YAML string to ensure ALL occurrences are replaced, not just in environment variables
func (s *ComposeService) interpolateInstallTimeVariables(yamlContent string, appName string) string {
	// Generate a single AUTH_HASH for this installation
	authHash := auth.GenerateHash()
	logger.Info("Generated AUTH_HASH for installation", zap.String("app", appName), zap.Int("hash_length", len(authHash)))
	
	// Only replace AUTH_HASH - other variables continue to work through baseInterpolationMap
	result := yamlContent
	
	// Count replacements for logging
	count := strings.Count(result, "$AUTH_HASH")
	if count > 0 {
		logger.Info("Replacing AUTH_HASH throughout compose file", 
			zap.String("app", appName),
			zap.Int("occurrences", count))
	}
	
	// Replace all occurrences of AUTH_HASH
	result = strings.ReplaceAll(result, "$AUTH_HASH", authHash)
	
	logger.Info("Completed AUTH_HASH interpolation", zap.String("app", appName), zap.Int("replacements", count))
	
	return result
}

func baseInterpolationMap() map[string]string {
	return map[string]string{
		"DefaultUserName": common.DefaultUserName,
		"DefaultPassword": common.DefaultPassword,
		"PUID":            common.DefaultPUID,
		"PGID":            common.DefaultPGID,
		"TZ":              timeutils.GetSystemTimeZoneName(),
	}
}

func apiService() (api.Service, client.APIClient, error) {
	dockerCli, err := command.NewDockerCli()
	if err != nil {
		return nil, nil, err
	}

	if err := dockerCli.Initialize(&flags.ClientOptions{}); err != nil {
		return nil, nil, err
	}

	return compose.NewComposeService(dockerCli), dockerCli.Client(), nil
}

func ApiService() (api.Service, client.APIClient, error) {
	return apiService()
}

func cleanup(workDir string) {
	logger.Info("cleaning up working dir", zap.String("path", workDir))
	if err := file.RMDir(workDir); err != nil {
		logger.Error("failed to cleanup working dir", zap.Error(err), zap.String("path", workDir))
	}
}
