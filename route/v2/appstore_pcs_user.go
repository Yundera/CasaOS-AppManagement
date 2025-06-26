package v2

import (
	"strconv"
	"strings"

	"github.com/IceWhaleTech/CasaOS-Common/utils/logger"
	"github.com/compose-spec/compose-go/types"
	"go.uber.org/zap"
)

// shouldAddUserRights checks if user rights should be added based on PUID/PGID availability
func shouldAddUserRights(puid, pgid string) bool {
	return puid != "" && pgid != "" && isValidUID(puid) && isValidUID(pgid)
}

// shouldAddUserToService checks if user should be added to a specific service
func shouldAddUserToService(service *types.ServiceConfig, puid, pgid string) bool {
	// Rule 1: Check if user is defined
	hasUser := service.User != ""

	if hasUser {
		logger.Info("PCS: service has user, skipping user rights",
			zap.String("service", service.Name),
			zap.String("existing_user", service.User))
		return false
	} else {
		logger.Info("PCS: service has no user defined", zap.String("service", service.Name))
		return true
	}
}

// hasPUIDInEnv checks if PUID is already defined in service environment variables
func hasPUIDInEnv(environment types.MappingWithEquals) bool {
	if environment == nil {
		return false
	}

	for key := range environment {
		if strings.ToUpper(key) == "PUID" {
			return true
		}
	}
	return false
}

// isValidUID checks if a string represents a valid UID/GID (positive integer)
func isValidUID(uid string) bool {
	if uid == "" {
		return false
	}
	uidInt, err := strconv.Atoi(uid)
	return err == nil && uidInt >= 0
}
