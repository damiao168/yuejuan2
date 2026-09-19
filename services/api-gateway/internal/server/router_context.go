package server

import (
	"net/http"
	"strings"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/config"
	"edugrade-enterprise/services/api-gateway/internal/handlers"
	"edugrade-enterprise/services/api-gateway/internal/observability"
	"edugrade-enterprise/services/api-gateway/internal/onboarding"
)

type routerContext struct {
	cfg                   config.Config
	modules               ApplicationModules
	system                *handlers.Handlers
	onboarding            *onboarding.Handler
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
	onboardingService := onboarding.NewService(onboarding.Dependencies{
		Tenants:      onboardingTenantReader{store: dependencies.Modules.Identity.OrgStore},
		Organization: onboardingOrganizationReader{store: dependencies.Modules.Identity.OrgStore},
		Users:        onboardingUserReader{store: dependencies.Modules.Identity.AuthStore},
		Exams:        onboardingExamReader{store: dependencies.Modules.Exam.ExamStore},
		System:       onboardingSystemReader{system: systemHandler},
		Governance:   onboardingGovernanceReader{store: dependencies.Modules.AIGovernance.modelStore},
	})
	return routerContext{
		cfg: cfg, modules: dependencies.Modules, system: systemHandler, onboarding: onboarding.NewHandler(onboardingService), metrics: metricsRegistry,
		questionBankImportMux: http.NewServeMux(),
		guards:                buildRouterGuards(cfg, dependencies.Modules),
	}
}
