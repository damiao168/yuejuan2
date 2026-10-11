package modelcalibration

import (
	"context"

	"edugrade-enterprise/services/api-gateway/internal/gradingevaluation"
)

// NewEvaluationReader 适配 A16 服务，不暴露持久化内部细节；校准只能使用 A16 已接受、已完成且在租户范围内的对齐观察。
func NewEvaluationReader(service *gradingevaluation.Service) EvaluationReader {
	return evaluationReader{service: service}
}

type evaluationReader struct{ service *gradingevaluation.Service }

func (r evaluationReader) GetRun(ctx context.Context, tenantID, runID string) (EvaluationRun, error) {
	item, err := r.service.GetRun(ctx, tenantID, runID)
	if err != nil {
		return EvaluationRun{}, err
	}
	return EvaluationRun{ID: item.ID, ModelReference: item.ModelReference, PromptVersion: item.PromptVersion, RubricVersion: item.RubricVersion, Status: string(item.Status)}, nil
}

func (r evaluationReader) ListObservations(ctx context.Context, tenantID, runID string) ([]EvaluationObservation, error) {
	items, err := r.service.ListObservations(ctx, tenantID, runID)
	if err != nil {
		return nil, err
	}
	result := make([]EvaluationObservation, 0, len(items))
	for _, item := range items {
		result = append(result, EvaluationObservation{ResponseKey: item.ResponseKey, Subject: item.Subject, Archetype: item.Archetype,
			OCRQuality: item.OCRQuality, ScoreBand: item.ReferenceScoreBand, ReferenceScore: item.ReferenceScore,
			ModelScore: item.ModelScore, MaxScore: item.MaxScore})
	}
	return result, nil
}
