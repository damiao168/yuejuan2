package server

import (
	"edugrade-enterprise/services/api-gateway/internal/aidisagreement"
	"edugrade-enterprise/services/api-gateway/internal/aieligibility"
	"edugrade-enterprise/services/api-gateway/internal/gradingevaluation"
	"edugrade-enterprise/services/api-gateway/internal/modelcalibration"
)

type AIFoundationStores struct {
	Eligibility       aieligibility.Store
	GradingEvaluation gradingevaluation.Store
	ModelCalibration  modelcalibration.Store
	Disagreement      aidisagreement.Store
}

type AIFoundation struct {
	EligibilityService       *aieligibility.Service
	GradingEvaluationService *gradingevaluation.Service
	ModelCalibrationService  *modelcalibration.Service
	DisagreementService      *aidisagreement.Service

	EligibilityHandler       *aieligibility.Handler
	GradingEvaluationHandler *gradingevaluation.Handler
	ModelCalibrationHandler  *modelcalibration.Handler
	DisagreementHandler      *aidisagreement.Handler
}

func NewAIFoundation(stores AIFoundationStores) *AIFoundation {
	gradingEvaluationService := gradingevaluation.NewService(stores.GradingEvaluation)
	modelCalibrationService := modelcalibration.NewService(stores.ModelCalibration, modelcalibration.NewEvaluationReader(gradingEvaluationService))
	disagreementService := aidisagreement.NewService(stores.Disagreement)
	module := &AIFoundation{
		GradingEvaluationService: gradingEvaluationService,
		ModelCalibrationService:  modelCalibrationService,
		DisagreementService:      disagreementService,
		GradingEvaluationHandler: gradingevaluation.NewHandler(gradingEvaluationService),
		ModelCalibrationHandler:  modelcalibration.NewHandler(modelCalibrationService),
		DisagreementHandler:      aidisagreement.NewHandler(disagreementService),
	}
	if stores.Eligibility != nil {
		module.EligibilityService = aieligibility.NewService(stores.Eligibility)
		module.EligibilityHandler = aieligibility.NewHandler(module.EligibilityService)
	}
	return module
}
