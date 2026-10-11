package outbox

import (
	"context"

	"edugrade-enterprise/services/api-gateway/internal/logger"
)

// LogPublisher 是内置的持久化输出端。外部日志或告警流水线可消费这些结构化记录，完整载荷仍保存在 Postgres 中。
type LogPublisher struct{ log *logger.Logger }

func NewLogPublisher(log *logger.Logger) *LogPublisher { return &LogPublisher{log: log} }

func (p *LogPublisher) Publish(ctx context.Context, event Event) error {
	p.log.Info(ctx, "transactional outbox event", map[string]any{
		"event":           "outbox_event",
		"outbox_event_id": event.ID,
		"tenant_id":       event.TenantID,
		"aggregate_type":  event.AggregateType,
		"aggregate_id":    event.AggregateID,
		"event_type":      event.EventType,
		"occurred_at":     event.OccurredAt,
		"attempt_count":   event.AttemptCount,
	})
	return nil
}
