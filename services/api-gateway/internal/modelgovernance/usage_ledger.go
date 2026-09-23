package modelgovernance

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/logger"
)

type ModelUsageEvent struct {
	TenantID              string
	SchoolID              string
	RequestID             string
	Feature               string
	AgentRole             string
	ProviderKey           string
	ModelName             string
	RequestCount          int64
	InputTokens           int64
	OutputTokens          int64
	CachedInputTokens     int64
	ReasoningTokens       int64
	TotalTokens           int64
	EstimatedCostMicroUSD int64
	Status                string
	OccurredAt            time.Time
	Metadata              map[string]any
}

type UsageLedger interface {
	RecordModelUsage(context.Context, ModelUsageEvent) error
}

func (s *PostgresStore) RecordModelUsage(ctx context.Context, event ModelUsageEvent) error {
	if event.RequestCount <= 0 {
		event.RequestCount = 1
	}
	if event.TotalTokens == 0 {
		event.TotalTokens = event.InputTokens + event.OutputTokens
	}
	if event.OccurredAt.IsZero() {
		event.OccurredAt = time.Now().UTC()
	}
	metadata, _ := json.Marshal(event.Metadata)
	_, err := s.db.ExecContext(ctx, `
INSERT INTO model_usage_event(
  tenant_id,school_id,request_id,feature,agent_role,provider_key,model_name,
  request_count,input_tokens,output_tokens,cached_input_tokens,reasoning_tokens,total_tokens,
  estimated_cost_microusd,status,occurred_at,metadata
)
SELECT $1::uuid,COALESCE(NULLIF($2,'')::uuid,(SELECT id FROM school WHERE tenant_id=$1::uuid AND deleted_at IS NULL ORDER BY created_at,id LIMIT 1)),
  $3,$4,NULLIF($5,''),$6,$7,$8,$9,$10,$11,$12,$13,NULLIF($14,0),$15,$16,$17::jsonb
ON CONFLICT (tenant_id,request_id,feature) DO NOTHING`,
		event.TenantID, event.SchoolID, event.RequestID, event.Feature, event.AgentRole, event.ProviderKey, event.ModelName,
		event.RequestCount, event.InputTokens, event.OutputTokens, event.CachedInputTokens, event.ReasoningTokens, event.TotalTokens,
		event.EstimatedCostMicroUSD, event.Status, event.OccurredAt, string(metadata))
	return err
}

func (h *Handler) recordUsage(ctx context.Context, event ModelUsageEvent) {
	ledger, ok := h.store.(UsageLedger)
	if !ok {
		return
	}
	if strings.TrimSpace(event.RequestID) == "" {
		event.RequestID = logger.RequestID(ctx)
	}
	if strings.TrimSpace(event.RequestID) == "" {
		return
	}
	if event.RequestCount <= 0 {
		event.RequestCount = 1
	}
	if event.TotalTokens == 0 {
		event.TotalTokens = event.InputTokens + event.OutputTokens
	}
	_ = ledger.RecordModelUsage(ctx, event)
}

var _ UsageLedger = (*PostgresStore)(nil)
