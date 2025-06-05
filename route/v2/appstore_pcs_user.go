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
// 1. No user already defined
// 2. No PUID already defined in env
// 3. PUID and PGID are valid
func shouldAddUserToService(service *types.ServiceConfig, puid, pgid string) bool {
	// Rule 1: Check if user is already defined
	if service.User != "" {
		logger.Info("PCS: service already has user defined, skipping user rights",
			zap.String("service", service.Name),
			zap.String("existing_user", service.User))
		return false
	}

	// Rule 2: Check if PUID is already defined in environment variables
	if hasPUIDInEnv(service.Environment) {
		logger.Info("PCS: service already has PUID in environment, skipping user rights",
			zap.String("service", service.Name))
		return false
	}

	// Rule 3: Check if PUID and PGID are valid
	if !isValidUID(puid) || !isValidUID(pgid) {
		logger.Info("PCS: invalid PUID or PGID, skipping user rights",
			zap.String("service", service.Name),
			zap.String("puid", puid),
			zap.String("pgid", pgid))
		return false
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
