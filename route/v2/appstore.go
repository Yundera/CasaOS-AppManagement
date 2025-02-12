package v2

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/IceWhaleTech/CasaOS-AppManagement/codegen"
	"github.com/IceWhaleTech/CasaOS-AppManagement/common"
	"github.com/IceWhaleTech/CasaOS-AppManagement/pkg/config"
	"github.com/IceWhaleTech/CasaOS-AppManagement/pkg/docker"
	"github.com/IceWhaleTech/CasaOS-AppManagement/service"
	"github.com/IceWhaleTech/CasaOS-Common/utils"
	"github.com/IceWhaleTech/CasaOS-Common/utils/logger"
	"github.com/compose-spec/compose-go/types"
	"github.com/labstack/echo/v4"
	"github.com/samber/lo"
	"go.uber.org/zap"
	"gopkg.in/yaml.v3"

	pkg_utils "github.com/IceWhaleTech/CasaOS-AppManagement/pkg/utils"
)

func (a *AppManagement) AppStoreList(ctx echo.Context) error {
	appStoreList := service.MyService.AppStoreManagement().AppStoreList()

	return ctx.JSON(http.StatusOK, codegen.AppStoreListOK{
		Data: &appStoreList,
	})
}

// the method should be deprecated
// but it be used by CasaOS
func (a *AppManagement) RegisterAppStore(ctx echo.Context, params codegen.RegisterAppStoreParams) error {
	if params.Url == nil || *params.Url == "" {
		message := "appstore url is required"
		return ctx.JSON(http.StatusBadRequest, codegen.ResponseBadRequest{Message: &message})
	}

	backgroundCtx := common.WithProperties(context.Background(), PropertiesFromQueryParams(ctx))

	if err := service.MyService.AppStoreManagement().RegisterAppStore(backgroundCtx, *params.Url); err != nil {
		message := err.Error()

		if err != nil {
			switch err {
			case service.ErrAppStoreSourceExists:
				return ctx.JSON(http.StatusConflict, codegen.ResponseConflict{Message: &message})
			case service.ErrNotAppStore:
				return ctx.JSON(http.StatusBadRequest, codegen.ResponseBadRequest{Message: &message})
			default:
				return ctx.JSON(http.StatusInternalServerError, codegen.ResponseInternalServerError{Message: &message})
			}
		}
	}

	logFilepath := filepath.Join(config.AppInfo.LogPath, fmt.Sprintf("%s.%s", config.AppInfo.LogSaveName, config.AppInfo.LogFileExt))
	message := fmt.Sprintf("trying to register app store asynchronously - see %s for any errors.", logFilepath)
	return ctx.JSON(http.StatusOK, codegen.AppStoreRegisterOK{
		Message: &message,
	})
}

func (a *AppManagement) RegisterAppStoreSync(ctx echo.Context, params codegen.RegisterAppStoreSyncParams) error {
	if params.Url == nil || *params.Url == "" {
		message := "appstore url is required"
		return ctx.JSON(http.StatusBadRequest, codegen.ResponseBadRequest{Message: &message})
	}

	backgroundCtx := common.WithProperties(context.Background(), PropertiesFromQueryParams(ctx))

	err := service.MyService.AppStoreManagement().RegisterAppStoreSync(backgroundCtx, *params.Url)
	if err != nil {
		message := err.Error()

		switch err {
		case service.ErrAppStoreSourceExists:
			return ctx.JSON(http.StatusConflict, codegen.ResponseConflict{Message: &message})
		case service.ErrNotAppStore:
			return ctx.JSON(http.StatusBadRequest, codegen.ResponseBadRequest{Message: &message})
		default:
			return ctx.JSON(http.StatusInternalServerError, codegen.ResponseInternalServerError{Message: &message})
		}
	}

	return ctx.JSON(http.StatusOK, codegen.AppStoreRegisterOK{
		Message: utils.Ptr("app store is registered."),
	})
}

func (a *AppManagement) UnregisterAppStore(ctx echo.Context, id codegen.AppStoreID) error {
	appStoreList := service.MyService.AppStoreManagement().AppStoreList()

	if id < 0 || id >= len(appStoreList) {
		message := fmt.Sprintf("app store id %d is not found", id)
		return ctx.JSON(http.StatusNotFound, codegen.ResponseNotFound{Message: &message})
	}

	if len(appStoreList) == 1 {
		message := "cannot unregister the last app store - need at least one app store"
		return ctx.JSON(http.StatusBadRequest, codegen.ResponseBadRequest{Message: &message})
	}

	if err := service.MyService.AppStoreManagement().UnregisterAppStore(uint(id)); err != nil {
		message := err.Error()
		return ctx.JSON(http.StatusInternalServerError, codegen.ResponseInternalServerError{Message: &message})
	}

	return ctx.JSON(http.StatusOK, codegen.AppStoreUnregisterOK{
		Message: utils.Ptr("app store is unregistered."),
	})
}

func (a *AppManagement) ComposeAppStoreInfoList(ctx echo.Context, params codegen.ComposeAppStoreInfoListParams) error {

	catalog, err := service.MyService.AppStoreManagement().Catalog()
	if err != nil {
		message := err.Error()
		logger.Error("failed to get catalog", zap.Error(err))
		return ctx.JSON(http.StatusInternalServerError, codegen.ResponseInternalServerError{Message: &message})
	}

	if params.Category != nil {
		catalog = FilterCatalogByCategory(catalog, *params.Category)
	}

	if params.AuthorType != nil {
		authorType := strings.ToLower(string(*params.AuthorType))
		catalog = FilterCatalogByAuthorType(catalog, codegen.StoreAppAuthorType(authorType))
	}

	if params.Recommend != nil && *params.Recommend {
		// recommend
		recommendedList, err := service.MyService.AppStoreManagement().Recommend()
		if err != nil {
			message := err.Error()
			logger.Error("failed to get recommend list", zap.Error(err))
			return ctx.JSON(http.StatusInternalServerError, codegen.ResponseInternalServerError{Message: &message})
		}

		catalog = FilterCatalogByAppStoreID(catalog, recommendedList)
	}

	cpuArch := pkg_utils.GetCPUArch()

	// Filter applications based on CPU architecture
	catalog = lo.PickBy(catalog, func(appStoreID string, composeApp *service.ComposeApp) bool {
		storeInfo, err := composeApp.StoreInfo(true)
		if err != nil {
			logger.Error("Failed to get app store information", zap.Error(err), zap.String("appStoreID", appStoreID))
			return false
		}

		// If architecture information is empty, assume it supports all architectures
		if storeInfo.Architectures == nil {
			return true
		}
		// Check if the application supports the current CPU architecture
		return lo.Contains(*storeInfo.Architectures, cpuArch)
	})

	// list
	list := lo.MapValues(catalog, func(composeApp *service.ComposeApp, appStoreID string) codegen.ComposeAppStoreInfo {
		storeInfo, err := composeApp.StoreInfo(true)
		if err != nil {
			logger.Error("failed to get store info", zap.Error(err), zap.String("appStoreID", appStoreID))
			return codegen.ComposeAppStoreInfo{}
		}

		return *storeInfo
	})

	data := &codegen.ComposeAppStoreInfoLists{
		List: &list,
	}

	// installed
	installedComposeApps, err := service.MyService.Compose().List(ctx.Request().Context())
	if err != nil {
		message := err.Error()
		logger.Error("failed to list installed compose apps", zap.Error(err))
		return ctx.JSON(http.StatusOK, codegen.ComposeAppStoreInfoListsOK{
			Message: &message,
			Data:    data,
		})
	}

	installed := lo.FilterMap(lo.Values(installedComposeApps), func(composeApp *service.ComposeApp, i int) (string, bool) {
		storeInfo, err := composeApp.StoreInfo(false)
		if err != nil {
			logger.Error("failed to get store info", zap.Error(err), zap.String("name", composeApp.Name))
			return "", false
		}

		if storeInfo == nil || storeInfo.StoreAppID == nil {
			logger.Error("failed to get store info - nil value", zap.String("name", composeApp.Name))
			return "", false
		}
		return *storeInfo.StoreAppID, true
	})

	data.Installed = &installed

	return ctx.JSON(http.StatusOK, codegen.ComposeAppStoreInfoListsOK{Data: data})
}

func (a *AppManagement) ComposeAppStoreInfo(ctx echo.Context, id codegen.StoreAppIDString) error {
	composeApp, err := service.MyService.AppStoreManagement().ComposeApp(id)
	if err != nil {
		message := err.Error()
		return ctx.JSON(http.StatusInternalServerError, codegen.ResponseInternalServerError{Message: &message})
	}

	if composeApp == nil {
		return ctx.JSON(http.StatusNotFound, codegen.ResponseNotFound{
			Message: utils.Ptr("app not found"),
		})
	}

	storeInfo, err := composeApp.StoreInfo(true)
	if err != nil {
		return ctx.JSON(http.StatusInternalServerError, codegen.ResponseInternalServerError{
			Message: utils.Ptr(err.Error()),
		})
	}

	return ctx.JSON(http.StatusOK, codegen.ComposeAppStoreInfoOK{
		Data: storeInfo,
	})
}

func (a *AppManagement) ComposeAppMainStableTag(ctx echo.Context, id codegen.StoreAppIDString) error {
	composeApp, err := service.MyService.AppStoreManagement().ComposeApp(id)
	if err != nil {
		message := err.Error()
		return ctx.JSON(http.StatusInternalServerError, codegen.ResponseInternalServerError{Message: &message})
	}

	if composeApp == nil {
		return ctx.JSON(http.StatusNotFound, codegen.ResponseNotFound{
			Message: utils.Ptr("app not found"),
		})
	}

	mainService, err := composeApp.MainService()
	if err != nil {
		return ctx.JSON(http.StatusInternalServerError, codegen.ResponseInternalServerError{
			Message: utils.Ptr(err.Error()),
		})
	}

	_, tag := docker.ExtractImageAndTag(mainService.Image)

	return ctx.JSON(http.StatusOK, codegen.ComposeAppStoreTagOK{
		Data: &codegen.ComposeAppStoreTag{
			Tag: tag,
		},
	})
}

func (a *AppManagement) ComposeAppServiceStableTag(ctx echo.Context, id codegen.StoreAppIDString, serviceName string) error {
	composeApp, err := service.MyService.AppStoreManagement().ComposeApp(id)
	if err != nil {
		message := err.Error()
		return ctx.JSON(http.StatusInternalServerError, codegen.ResponseInternalServerError{Message: &message})
	}

	if composeApp == nil {
		return ctx.JSON(http.StatusNotFound, codegen.ResponseNotFound{
			Message: utils.Ptr("app not found"),
		})
	}

	service := composeApp.App(serviceName)
	if service == nil {
		return ctx.JSON(http.StatusInternalServerError, codegen.ResponseInternalServerError{
			Message: utils.Ptr("service not found"),
		})
	}

	_, tag := docker.ExtractImageAndTag(service.Image)

	return ctx.JSON(http.StatusOK, codegen.ComposeAppStoreTagOK{
		Data: &codegen.ComposeAppStoreTag{
			Tag: tag,
		},
	})
}

//

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
	refScheme := getEnvWithDefault("REF_SCHEME", "http")
	refSeparator := getEnvWithDefault("REF_SEPARATOR", "-")
	logger.Info("update compose with",
		zap.String("DATA_ROOT", dataRoot),
		zap.String("REF_NET", refNet),
		zap.String("REF_PORT", refPort),
		zap.String("REF_DOMAIN", refDomain),
		zap.String("REF_SCHEME", refScheme),
		zap.String("REF_SEPARATOR", refSeparator))

	// Handle x-casaos extensions
	if casaosExt, ok := compose.Extensions["x-casaos"]; ok {
		casaosExtensions, ok := casaosExt.(map[string]interface{})
		if !ok {
			logger.Error("invalid x-casaos extension format",
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
				logger.Error("no services defined in compose",
					zap.String("name", compose.Name))
				return composeR
			}

			if len(compose.Services[0].Ports) == 0 {
				logger.Error("no ports defined for first service",
					zap.String("name", compose.Name),
					zap.String("service", compose.Services[0].Name))
				return composeR
			}

			webuiExposePort := strconv.Itoa(int(compose.Services[0].Ports[0].Target))
			logger.Info("found webui expose port",
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
			logger.Error("no services to modify",
				zap.String("name", compose.Name))
			return composeR
		}

		servicesCopy := make([]types.ServiceConfig, len(compose.Services))
		for i, service := range compose.Services {
			servicesCopy[i] = service // Shallow copy of service

			if dataRoot != "" {
				servicesCopy[i].Volumes = filterVolumes(service.Volumes, dataRoot)
			}

			servicesCopy[i].Expose = convertPortsToExpose(service.Ports)
			servicesCopy[i].Ports = nil

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

func (a *AppManagement) ComposeApp(ctx echo.Context, id codegen.StoreAppIDString) error {
	composeApp, err := service.MyService.AppStoreManagement().ComposeApp(id)
	if err != nil {
		message := err.Error()
		return ctx.JSON(http.StatusInternalServerError, codegen.ResponseInternalServerError{Message: &message})
	}

	if composeApp == nil {
		return ctx.JSON(http.StatusNotFound, codegen.ResponseNotFound{
			Message: utils.Ptr("app not found"),
		})
	}

	accept := ctx.Request().Header.Get(echo.HeaderAccept)
	if accept == common.MIMEApplicationYAML {
		yaml, err := yaml.Marshal(modifyComposeData((*codegen.ComposeApp)(composeApp)))
		if err != nil {
			message := err.Error()
			return ctx.JSON(http.StatusInternalServerError, codegen.ResponseInternalServerError{
				Message: &message,
			})
		}

		return ctx.String(http.StatusOK, string(yaml))
	}

	storeInfo, err := composeApp.StoreInfo(false)
	if err != nil {
		message := err.Error()
		return ctx.JSON(http.StatusInternalServerError, codegen.ResponseInternalServerError{
			Message: &message,
		})
	}

	message := fmt.Sprintf("!! JSON format is for debugging purpose only - use `Accept: %s` HTTP header to get YAML instead !!", common.MIMEApplicationYAML)
	data := codegen.ComposeAppWithStoreInfo{
		StoreInfo: storeInfo,
		Compose:   modifyComposeData((*codegen.ComposeApp)(composeApp)),
	}

	return ctx.JSON(http.StatusOK, codegen.ComposeAppOK{
		// extension properties aren't marshalled - https://github.com/golang/go/issues/6213
		Message: &message,
		Data:    &data,
	})
}

func (a *AppManagement) CategoryList(ctx echo.Context) error {
	categoryMap, err := service.MyService.AppStoreManagement().CategoryMap()
	if err != nil {
		message := err.Error()
		return ctx.JSON(http.StatusInternalServerError, codegen.ResponseInternalServerError{Message: &message})
	}

	categoryList := lo.Values(categoryMap)

	sort.Slice(categoryList, func(i, j int) bool { return strings.Compare(*categoryList[i].Name, *categoryList[j].Name) < 0 })

	totalCount := 0
	for _, category := range categoryList {
		if category.Count == nil {
			continue
		}

		totalCount += *category.Count
	}

	categoryList = append([]codegen.CategoryInfo{
		{
			Name:        utils.Ptr("All"),
			Font:        utils.Ptr("apps"),
			Description: utils.Ptr("All apps"),
			Count:       &totalCount,
		},
	}, categoryList...)

	categoryList = lo.Map(categoryList, func(category codegen.CategoryInfo, i int) codegen.CategoryInfo {
		category.ID = &i
		return category
	})

	return ctx.JSON(http.StatusOK, codegen.CategoryListOK{
		Data: &categoryList,
	})
}

func FilterCatalogByCategory(catalog map[string]*service.ComposeApp, category string) map[string]*service.ComposeApp {
	if category == "" {
		return catalog
	}

	return lo.PickBy(catalog, func(storeAppID string, composeApp *service.ComposeApp) bool {
		storeInfo, err := composeApp.StoreInfo(true)
		if err != nil {
			return false
		}

		return strings.EqualFold(storeInfo.Category, category)
	})
}

func FilterCatalogByAuthorType(catalog map[string]*service.ComposeApp, authorType codegen.StoreAppAuthorType) map[string]*service.ComposeApp {
	if !lo.Contains([]codegen.StoreAppAuthorType{
		codegen.Official,
		codegen.ByCasaos,
		codegen.Community,
	}, authorType) {
		logger.Info("warning: unknown author type - returning empty catalog", zap.String("authorType", string(authorType)))
		return map[string]*service.ComposeApp{}
	}

	return lo.PickBy(catalog, func(_ string, composeApp *service.ComposeApp) bool {
		return composeApp.AuthorType() == authorType
	})
}

func FilterCatalogByAppStoreID(catalog map[string]*service.ComposeApp, appStoreIDs []string) map[string]*service.ComposeApp {
	return lo.PickBy(catalog, func(storeAppID string, _ *service.ComposeApp) bool {
		return lo.Contains(appStoreIDs, storeAppID)
	})
}

func (a *AppManagement) UpgradableAppList(ctx echo.Context) error {
	composeApps, err := service.MyService.Compose().List(ctx.Request().Context())

	var upgradableAppList []codegen.UpgradableAppInfo = []codegen.UpgradableAppInfo{}
	if err != nil {
		message := err.Error()
		return ctx.JSON(http.StatusInternalServerError, codegen.ResponseInternalServerError{Message: &message})
	}
	for id, composeApp := range composeApps {
		if composeApp == nil {
			continue
		}

		storeInfo, err := composeApp.StoreInfo(true)
		if err != nil {
			logger.Error("failed to get store info", zap.Error(err), zap.String("appStoreID", id))
			continue
		}

		title, err := json.Marshal(storeInfo.Title)
		if err != nil {
			title = []byte("unknown")
		}

		storeComposeApp, err := service.MyService.AppStoreManagement().ComposeApp(id)
		if err != nil || storeComposeApp == nil {
			logger.Error("failed to get compose app", zap.Error(err), zap.String("appStoreID", id))
			continue
		}
		tag, err := storeComposeApp.MainTag()
		if err != nil {
			// TODO
			logger.Error("failed to get compose app main tag", zap.Error(err), zap.String("appStoreID", id))
			continue
		}

		status := codegen.Idle
		if service.MyService.AppStoreManagement().IsUpdating(composeApp.Name) {
			status = codegen.Updating
		}

		// not change the main tag
		mainTag, err := composeApp.MainTag()
		if err != nil {
			logger.Error("failed to get main tag", zap.Error(err), zap.String("name", composeApp.Name))
			continue
		}

		targetTag := tag
		if lo.Contains(common.NeedCheckDigestTags, mainTag) {
			targetTag = mainTag
		}

		if service.MyService.AppStoreManagement().IsUpdateAvailable(composeApp) {
			upgradableAppList = append(upgradableAppList, codegen.UpgradableAppInfo{
				Title:      string(title),
				Version:    targetTag,
				StoreAppID: lo.ToPtr(id),
				Status:     status,
				Icon:       storeInfo.Icon,
			})
		}
	}

	//  sort upgradableAppList by title
	sort.Slice(upgradableAppList, func(i, j int) bool {
		return strings.Compare(upgradableAppList[i].Title, upgradableAppList[j].Title) < 0
	})

	return ctx.JSON(http.StatusOK, codegen.UpgradableAppListOK{
		Data: &upgradableAppList,
	})
}
