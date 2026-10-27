package outbox

import (
	"context"

	"edugrade-enterprise/services/api-gateway/internal/logger"
)

// LogPublisher 输出不受诊断级别过滤的结构化事件；确认仅表示 writer 接收成功，完整载荷仍保存在 Postgres 中。
type LogPublisher struct{ log *logger.Logger }

func NewLogPublisher(log *logger.Logger) *LogPublisher { return &LogPublisher{log: log} }

func (p *LogPublisher) Publish(ctx context.Context, event Event) error {
	return p.log.WriteEvent(ctx, "transactional outbox event", map[string]any{
		"event":           "outbox_event",
		"outbox_event_id": event.ID,
		"tenant_id":       event.TenantID,
		"aggregate_type":  event.AggregateType,
		"aggregate_id":    event.AggregateID,
		"event_type":      event.EventType,
		"occurred_at":     event.OccurredAt,
		"attempt_count":   event.AttemptCount,
	})
}
