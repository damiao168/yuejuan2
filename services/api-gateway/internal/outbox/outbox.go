package outbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

var ErrLeaseLost = errors.New("outbox lease lost")

type Event struct {
	ID            string          `json:"id"`
	TenantID      string          `json:"tenant_id"`
	AggregateType string          `json:"aggregate_type"`
	AggregateID   string          `json:"aggregate_id"`
	EventType     string          `json:"event_type"`
	Payload       json.RawMessage `json:"payload"`
	OccurredAt    time.Time       `json:"occurred_at"`
	AttemptCount  int             `json:"attempt_count"`
	MaxAttempts   int             `json:"max_attempts"`
}

type Store interface {
	Claim(context.Context, string, int, time.Duration) ([]Event, error)
	MarkPublished(context.Context, string, string) error
	MarkFailed(context.Context, string, string, string, time.Duration) error
}

// Publisher 可能收到重复事件：外部发布成功后，记录发布状态仍可能失败。
// 有副作用的实现应使用 Event.ID 去重，不能假设每条事件只投递一次。
type Publisher interface {
	Publish(context.Context, Event) error
}

type Options struct {
	Owner        string
	BatchSize    int
	LeaseTTL     time.Duration
	PollInterval time.Duration
	BaseBackoff  time.Duration
	MaxBackoff   time.Duration
}

type Dispatcher struct {
	store     Store
	publisher Publisher
	options   Options
}

func NewDispatcher(store Store, publisher Publisher, options Options) *Dispatcher {
	if options.Owner == "" {
		options.Owner = fmt.Sprintf("api-%d", time.Now().UnixNano())
	}
	if options.BatchSize <= 0 || options.BatchSize > 500 {
		options.BatchSize = 100
	}
	if options.LeaseTTL <= 0 {
		options.LeaseTTL = 30 * time.Second
	}
	if options.PollInterval <= 0 {
		options.PollInterval = 2 * time.Second
	}
	if options.BaseBackoff <= 0 {
		options.BaseBackoff = time.Second
	}
	if options.MaxBackoff <= 0 {
		options.MaxBackoff = 15 * time.Minute
	}
	return &Dispatcher{store: store, publisher: publisher, options: options}
}

func (d *Dispatcher) RunOnce(ctx context.Context) (int, error) {
	events, err := d.store.Claim(ctx, d.options.Owner, d.options.BatchSize, d.options.LeaseTTL)
	if err != nil {
		return 0, err
	}
	published := 0
	for _, event := range events {
		if err := d.publisher.Publish(ctx, event); err != nil {
			delay := d.backoff(event.AttemptCount)
			if markErr := d.store.MarkFailed(ctx, event.ID, d.options.Owner, err.Error(), delay); markErr != nil {
				return published, errors.Join(err, markErr)
			}
			continue
		}
		if err := d.store.MarkPublished(ctx, event.ID, d.options.Owner); err != nil {
			return published, err
		}
		published++
	}
	return published, nil
}

func (d *Dispatcher) Run(ctx context.Context, onError func(error)) {
	ticker := time.NewTicker(d.options.PollInterval)
	defer ticker.Stop()
	for {
		if _, err := d.RunOnce(ctx); err != nil && onError != nil && !errors.Is(err, context.Canceled) {
			onError(err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (d *Dispatcher) backoff(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	delay := d.options.BaseBackoff
	for i := 1; i < attempt && delay < d.options.MaxBackoff; i++ {
		delay *= 2
		if delay > d.options.MaxBackoff {
			return d.options.MaxBackoff
		}
	}
	return delay
}
