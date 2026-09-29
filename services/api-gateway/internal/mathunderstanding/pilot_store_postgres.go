package mathunderstanding

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

type PostgresPilotGateStore struct{ db *sql.DB }

func NewPostgresPilotGateStore(db *sql.DB) *PostgresPilotGateStore {
	return &PostgresPilotGateStore{db: db}
}

// CreatePilotGate 将指标、阈值和本次判断一起保存；以后调整阈值时，历史评估仍按当时的依据解释。
func (s *PostgresPilotGateStore) CreatePilotGate(ctx context.Context, tenantID, subject, benchmarkRef string, metrics PilotMetrics, policy PilotPolicy, actorID string) (PilotGateEvaluation, error) {
	if err := validatePilotGateInput(tenantID, subject, benchmarkRef, metrics, policy, actorID); err != nil {
		return PilotGateEvaluation{}, err
	}
	decision := EvaluatePilotGate(subject, metrics, policy)
	metricsJSON, _ := json.Marshal(metrics)
	policyJSON, _ := json.Marshal(policy)
	decisionJSON, _ := json.Marshal(decision)
	item := PilotGateEvaluation{TenantID: tenantID, SubjectCode: subject, BenchmarkRef: benchmarkRef, Metrics: metrics, Policy: policy, Decision: decision, EvaluatedBy: actorID}
	err := s.db.QueryRowContext(ctx, `INSERT INTO math_pilot_gate_evaluation(tenant_id,subject_code,benchmark_ref,metrics_json,policy_json,decision_json,evaluated_by) VALUES($1::uuid,$2,$3,$4::jsonb,$5::jsonb,$6::jsonb,$7::uuid) RETURNING id::text,created_at`, tenantID, subject, benchmarkRef, metricsJSON, policyJSON, decisionJSON, actorID).Scan(&item.ID, &item.CreatedAt)
	return item, err
}

func (s *PostgresPilotGateStore) ListPilotGates(ctx context.Context, tenantID, subject string, limit int) ([]PilotGateEvaluation, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id::text,tenant_id::text,subject_code,benchmark_ref,metrics_json,policy_json,decision_json,evaluated_by::text,created_at FROM math_pilot_gate_evaluation WHERE tenant_id=$1::uuid AND ($2='' OR subject_code=$2) ORDER BY created_at DESC,id DESC LIMIT $3`, tenantID, subject, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []PilotGateEvaluation{}
	for rows.Next() {
		var item PilotGateEvaluation
		var metricsJSON, policyJSON, decisionJSON []byte
		if err = rows.Scan(&item.ID, &item.TenantID, &item.SubjectCode, &item.BenchmarkRef, &metricsJSON, &policyJSON, &decisionJSON, &item.EvaluatedBy, &item.CreatedAt); err != nil {
			return nil, err
		}
		if json.Unmarshal(metricsJSON, &item.Metrics) != nil || json.Unmarshal(policyJSON, &item.Policy) != nil || json.Unmarshal(decisionJSON, &item.Decision) != nil {
			return nil, errors.New("decode math pilot gate evaluation")
		}
		out = append(out, item)
	}
	return out, rows.Err()
}
