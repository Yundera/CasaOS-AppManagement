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
// Rules:
// 1. Skip only if BOTH user is defined AND PUID is defined in env
// 2. PUID and PGID must be valid
func shouldAddUserToService(service *types.ServiceConfig, puid, pgid string) bool {
	// Rule 1: Check if BOTH user is defined AND PUID is in environment
	hasUser := service.User != ""
	hasPUID := hasPUIDInEnv(service.Environment)
	
	if hasUser && hasPUID {
		logger.Info("PCS: service has both user defined and PUID in environment, skipping user rights",
			zap.String("service", service.Name),
			zap.String("existing_user", service.User))
		return false
	}

	// Rule 2: Check if PUID and PGID are valid
	if !isValidUID(puid) || !isValidUID(pgid) {
		logger.Info("PCS: invalid PUID or PGID, skipping user rights",
			zap.String("service", service.Name),
			zap.String("puid", puid),
			zap.String("pgid", pgid))
		return false
	}

	// Log what we're adding if we proceed
	if hasUser {
		logger.Info("PCS: service has user defined but no PUID in env, will add environment variables",
			zap.String("service", service.Name),
			zap.String("existing_user", service.User))
	} else if hasPUID {
		logger.Info("PCS: service has PUID in env but no user defined, will add user field",
			zap.String("service", service.Name))
	} else {
		logger.Info("PCS: service has neither user nor PUID defined, will add both",
			zap.String("service", service.Name))
	}

	return true
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