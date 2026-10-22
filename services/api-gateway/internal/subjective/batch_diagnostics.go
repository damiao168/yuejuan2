package subjective

import (
	"context"
	"encoding/json"
	"sort"
)

// BatchFailureSummary 仅聚合固定诊断，不含来源、模型正文或个人信息。
// 旧运行没有调用计数，省略汇总次数，不能把缺失事实显示成零调用。
type BatchFailureSummary struct {
	FailureKind          string `json:"failure_kind"`
	Count                int    `json:"count"`
	Attempts             *int   `json:"attempts,omitempty"`
	ProviderRequestCount *int   `json:"provider_request_count,omitempty"`
	UsageStatus          string `json:"usage_status"`
}

type batchFailureSummaryReader interface {
	LoadBatchFailureSummaries(context.Context, string, []GradingBatch) (map[string][]BatchFailureSummary, error)
}

func attachBatchFailureSummaries(ctx context.Context, store Store, tenantID string, batches []GradingBatch) error {
	reader, ok := store.(batchFailureSummaryReader)
	if !ok || len(batches) == 0 {
		return nil
	}
	summaries, err := reader.LoadBatchFailureSummaries(ctx, tenantID, batches)
	if err != nil {
		return err
	}
	for index := range batches {
		batches[index].FailureSummary = summaries[batches[index].ID]
	}
	return nil
}

func summarizeBatchFailures(runs []GradingRun) []BatchFailureSummary {
	type group struct {
		value                                  BatchFailureSummary
		known                                  bool
		attempts, calls, reported, unavailable int
		invalid                                bool
	}
	groups := map[string]*group{}
	for _, run := range runs {
		if run.Status != RunFailed && run.Status != RunConflict {
			continue
		}
		d := run.ExecutionDiagnostics
		kind := "output_incomplete"
		if d != nil && diagnosticFailureKinds[d.FailureKind] {
			kind = d.FailureKind
		}
		g := groups[kind]
		if g == nil {
			g = &group{value: BatchFailureSummary{FailureKind: kind}, known: true}
			groups[kind] = g
		}
		g.value.Count++
		if d == nil {
			g.known = false
			g.unavailable++
			continue
		}
		g.attempts += d.Attempts
		g.calls += d.ProviderRequestCount
		switch d.UsageStatus {
		case "reported":
			g.reported++
		case "unavailable":
			g.unavailable++
		case "invalid":
			g.invalid = true
		}
	}
	result := []BatchFailureSummary{}
	for _, g := range groups {
		g.value.UsageStatus = "partial"
		if g.invalid {
			g.value.UsageStatus = "invalid"
		} else if g.reported == g.value.Count {
			g.value.UsageStatus = "reported"
		} else if g.unavailable == g.value.Count {
			g.value.UsageStatus = "unavailable"
		}
		if g.known {
			attempts, calls := g.attempts, g.calls
			g.value.Attempts = &attempts
			g.value.ProviderRequestCount = &calls
		}
		result = append(result, g.value)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].FailureKind < result[j].FailureKind })
	return result
}

func (s *MemoryStore) LoadBatchFailureSummaries(_ context.Context, tenantID string, batches []GradingBatch) (map[string][]BatchFailureSummary, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	groups := map[string][]GradingRun{}
	for _, batch := range batches {
		if batch.TenantID == tenantID {
			groups[batch.ID] = nil
		}
	}
	for _, run := range s.runs {
		if _, ok := groups[run.BatchID]; ok && run.TenantID == tenantID {
			groups[run.BatchID] = append(groups[run.BatchID], run)
		}
	}
	result := map[string][]BatchFailureSummary{}
	for id, runs := range groups {
		result[id] = summarizeBatchFailures(runs)
	}
	return result, nil
}

func (s *PostgresStore) LoadBatchFailureSummaries(ctx context.Context, tenantID string, batches []GradingBatch) (map[string][]BatchFailureSummary, error) {
	ids := []string{}
	for _, batch := range batches {
		if batch.TenantID == tenantID {
			ids = append(ids, batch.ID)
		}
	}
	raw, err := json.Marshal(ids)
	if err != nil {
		return nil, err
	}
	// 一次查询全部授权批次，避免概览随批次数增长产生逐批诊断查询。
	rows, err := s.db.QueryContext(ctx, `SELECT batch_id::text,status,execution_diagnostics FROM subjective_grading_run
WHERE tenant_id=$1::uuid AND batch_id::text IN (SELECT jsonb_array_elements_text($2::jsonb))
AND status IN ('failed','conflict') AND deleted_at IS NULL`, tenantID, raw)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	groups := map[string][]GradingRun{}
	for rows.Next() {
		var run GradingRun
		var diagnostics []byte
		if err := rows.Scan(&run.BatchID, &run.Status, &diagnostics); err != nil {
			return nil, err
		}
		if len(diagnostics) > 0 {
			if err := json.Unmarshal(diagnostics, &run.ExecutionDiagnostics); err != nil {
				return nil, err
			}
		}
		groups[run.BatchID] = append(groups[run.BatchID], run)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	result := map[string][]BatchFailureSummary{}
	for id, runs := range groups {
		result[id] = summarizeBatchFailures(runs)
	}
	return result, nil
}
