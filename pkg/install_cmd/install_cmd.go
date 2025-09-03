package install_cmd

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/IceWhaleTech/CasaOS-AppManagement/codegen"
	"github.com/IceWhaleTech/CasaOS-Common/utils/logger"
	"go.uber.org/zap"
)

func ExecuteInstallCmd(composeApp *codegen.ComposeApp, cmdType string) error {
	if composeApp == nil {
		logger.Error("PCS: cannot execute install command - nil compose app", zap.String("cmdType", cmdType))
		return fmt.Errorf("nil compose app")
	}

	// Check if x-casaos extension exists
	casaosExt, ok := composeApp.Extensions["x-casaos"]
	if !ok {
		logger.Info("PCS: no x-casaos extension found, skipping install command check",
			zap.String("name", composeApp.Name),
			zap.String("cmdType", cmdType))
		return nil
	}

	// Check if it's a map
	casaosExtensions, ok := casaosExt.(map[string]interface{})
	if !ok {
		logger.Error("PCS: invalid x-casaos extension format",
			zap.String("name", composeApp.Name),
			zap.String("cmdType", cmdType),
			zap.Any("extensions", casaosExt))
		return fmt.Errorf("invalid x-casaos extension format")
	}

	// Check for the specific command type
	installCmd, exists := casaosExtensions[cmdType]
	if !exists || installCmd == nil {
		logger.Info("PCS: no install command found in x-casaos extension",
			zap.String("name", composeApp.Name),
			zap.String("cmdType", cmdType))
		return nil
	}

	// Get the command value as string
	cmdString, ok := installCmd.(string)
	if !ok || cmdString == "" {
		logger.Error("PCS: invalid install command value",
			zap.String("name", composeApp.Name),
			zap.String("cmdType", cmdType),
			zap.Any("command", installCmd))
		return fmt.Errorf("invalid %s value", cmdType)
	}

	logger.Info("PCS: executing install command",
		zap.String("name", composeApp.Name),
		zap.String("cmdType", cmdType),
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
		zap.String("cmdType", cmdType),
		zap.String("command", cmdString),
		zap.Strings("env", execCmd.Env))

	// Run the command interactively
	err := execCmd.Run()
	if err != nil {
		logger.Error("PCS: failed to execute install command",
			zap.String("name", composeApp.Name),
			zap.String("cmdType", cmdType),
			zap.String("command", cmdString),
			zap.Error(err))
		return fmt.Errorf("%s execution failed: %w", cmdType, err)
	}

	logger.Info("PCS: install command executed successfully",
		zap.String("name", composeApp.Name),
		zap.String("cmdType", cmdType),
		zap.String("command", cmdString))

	return nil
}

func ExecutePreInstallScript(composeApp *codegen.ComposeApp) error {
	return ExecuteInstallCmd(composeApp, "pre-install-cmd")
}

func ExecutePostInstallScript(composeApp *codegen.ComposeApp) error {
	return ExecuteInstallCmd(composeApp, "post-install-cmd")
}