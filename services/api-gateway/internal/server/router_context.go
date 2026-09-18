package server

import (
	"net/http"
	"strings"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/config"
	"edugrade-enterprise/services/api-gateway/internal/handlers"
	"edugrade-enterprise/services/api-gateway/internal/observability"
)

type routerContext struct {
	cfg                   config.Config
	modules               ApplicationModules
	system                *handlers.Handlers
	metrics               *observability.Registry
	questionBankImportMux *http.ServeMux
	guards                routerGuards
}

func buildRouterContext(dependencies RouterDependencies) routerContext {
	cfg := dependencies.Config
	if strings.TrimSpace(cfg.Auth.SessionCookieName) == "" {
		cfg.Auth.SessionCookieName = auth.DefaultSessionCookieName
	}
	metricsRegistry := dependencies.Metrics
	if metricsRegistry == nil {
		metricsRegistry = observability.NewRegistry()
	}
	systemHandler := handlers.New(cfg, dependencies.Checkers)
	systemHandler.WithWorkerRuntimeStore(dependencies.Modules.Capture.WorkerRuntimeStore)
	return routerContext{
		cfg: cfg, modules: dependencies.Modules, system: systemHandler, metrics: metricsRegistry,
		questionBankImportMux: http.NewServeMux(),
		guards:                buildRouterGuards(cfg, dependencies.Modules),
	}
}
