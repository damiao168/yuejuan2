package server

import (
	"edugrade-enterprise/services/api-gateway/internal/aidisagreement"
	"edugrade-enterprise/services/api-gateway/internal/aieligibility"
	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/config"
	"edugrade-enterprise/services/api-gateway/internal/gradingevaluation"
	"edugrade-enterprise/services/api-gateway/internal/mathunderstanding"
	"edugrade-enterprise/services/api-gateway/internal/modelcalibration"
	"edugrade-enterprise/services/api-gateway/internal/modelgovernance"
	"edugrade-enterprise/services/api-gateway/internal/review"
	"edugrade-enterprise/services/api-gateway/internal/workerruntime"
)

type AIGovernanceStores struct {
	ModelGovernance   modelgovernance.Store
	MathUnderstanding mathunderstanding.Store
	MathCorrections   mathunderstanding.CorrectionStore
	MathPilotGates    mathunderstanding.PilotGateStore
}

type AIGovernanceModule struct {
	modelStore               modelgovernance.Store
	ModelGovernanceHandler   *modelgovernance.Handler
	MathUnderstandingHandler *mathunderstanding.Handler
	EligibilityHandler       *aieligibility.Handler
	GradingEvaluationHandler *gradingevaluation.Handler
	ModelCalibrationHandler  *modelcalibration.Handler
	DisagreementHandler      *aidisagreement.Handler
}

type AIGovernanceDependencies struct {
	Auth                     auth.AuditRecorder
	WorkerRuntime            workerruntime.Store
	Reviews                  review.Store
	EligibilityHandler       *aieligibility.Handler
	GradingEvaluationHandler *gradingevaluation.Handler
	ModelCalibrationHandler  *modelcalibration.Handler
	DisagreementHandler      *aidisagreement.Handler
}

func NewAIGovernanceModule(cfg config.Config, stores AIGovernanceStores, dependencies AIGovernanceDependencies) *AIGovernanceModule {
	return &AIGovernanceModule{
		modelStore:               stores.ModelGovernance,
		EligibilityHandler:       dependencies.EligibilityHandler,
		GradingEvaluationHandler: dependencies.GradingEvaluationHandler,
		ModelCalibrationHandler:  dependencies.ModelCalibrationHandler,
		DisagreementHandler:      dependencies.DisagreementHandler,
		ModelGovernanceHandler: modelgovernance.NewHandler(
			stores.ModelGovernance,
			dependencies.Auth,
			modelgovernance.NewEnvironmentSecretResolver(""),
			localModelBaseline(cfg),
		).WithRuntimePromptSource(modelgovernance.NewHTTPRuntimePromptSource(
			cfg.AIService.URL,
			cfg.AIService.Token,
			cfg.AIService.Timeout,
		)),
		MathUnderstandingHandler: mathunderstanding.NewHandler(
			stores.MathUnderstanding, stores.MathCorrections, stores.MathPilotGates, dependencies.Reviews, dependencies.Auth,
		).WithRuntime(dependencies.WorkerRuntime),
	}
}
