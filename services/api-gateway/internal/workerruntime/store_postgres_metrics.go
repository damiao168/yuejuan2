package workerruntime

import (
	"context"
	"encoding/json"
)

func (s *PostgresStore) Metrics(ctx context.Context, tenantID string) (Metrics, error) {
	return s.metrics(ctx, tenantID, false)
}

func (s *PostgresStore) MetricsAcrossTenants(ctx context.Context, platformTenantID string) (Metrics, error) {
	return s.metrics(ctx, platformTenantID, true)
}

func (s *PostgresStore) metrics(ctx context.Context, tenantID string, acrossTenants bool) (Metrics, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT queue_name,
  count(*) FILTER (WHERE status = 'queued'),
  count(*) FILTER (WHERE status = 'leased'),
  count(*) FILTER (WHERE status = 'running'),
  count(*) FILTER (WHERE status = 'succeeded'),
  count(*) FILTER (WHERE status = 'failed'),
  count(*) FILTER (WHERE status = 'dead_letter'),
  count(*) FILTER (WHERE status = 'cancelled'),
  count(*) FILTER (WHERE status IN ('failed', 'dead_letter') AND updated_at >= now() - interval '1 hour'),
  COALESCE(percentile_cont(0.95) WITHIN GROUP (ORDER BY duration_ms) FILTER (WHERE duration_ms IS NOT NULL), 0)::int
FROM agent_worker_task
WHERE ($2 OR tenant_id = $1)
  AND (NOT $2 OR tenant_id IN (
    SELECT id FROM tenant WHERE status = 'active' AND deleted_at IS NULL
  ))
GROUP BY queue_name
ORDER BY queue_name
`, tenantID, acrossTenants)
	if err != nil {
		return Metrics{}, err
	}
	out := Metrics{Queues: []QueueMetrics{}, Workers: []WorkerHeartbeat{}}
	for rows.Next() {
		var metric QueueMetrics
		if err := rows.Scan(&metric.QueueName, &metric.Queued, &metric.Leased, &metric.Running, &metric.Succeeded, &metric.Failed, &metric.DeadLetter, &metric.Cancelled, &metric.FailedLastHour, &metric.P95DurationMS); err != nil {
			rows.Close()
			return Metrics{}, err
		}
		out.Queues = append(out.Queues, metric)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return Metrics{}, err
	}
	if err := rows.Close(); err != nil {
		return Metrics{}, err
	}
	for index := range out.Queues {
		if err := s.db.QueryRowContext(ctx, `
SELECT count(*) FROM agent_worker_task_attempt a
JOIN agent_worker_task t ON t.tenant_id = a.tenant_id AND t.id = a.task_id
WHERE ($3 OR a.tenant_id = $1) AND t.queue_name = $2 AND a.status = 'failed'
  AND (NOT $3 OR a.tenant_id IN (
    SELECT id FROM tenant WHERE status = 'active' AND deleted_at IS NULL
  ))
  AND a.completed_at >= now() - interval '1 hour' AND a.attempt_no < t.max_attempts
`, tenantID, out.Queues[index].QueueName, acrossTenants).Scan(&out.Queues[index].RetryLastHour); err != nil {
			return Metrics{}, err
		}
	}
	workerRows, err := s.db.QueryContext(ctx, `
SELECT tenant_id::text, worker_service, worker_instance_id, queue_name, last_seen_at, metadata
FROM agent_worker_heartbeat
WHERE ($2 OR tenant_id = $1)
  AND (NOT $2 OR tenant_id IN (
    SELECT id FROM tenant WHERE status = 'active' AND deleted_at IS NULL
  ))
ORDER BY last_seen_at DESC
`, tenantID, acrossTenants)
	if err != nil {
		return Metrics{}, err
	}
	defer workerRows.Close()
	for workerRows.Next() {
		var worker WorkerHeartbeat
		var metadata []byte
		if err := workerRows.Scan(&worker.TenantID, &worker.WorkerService, &worker.WorkerInstanceID, &worker.QueueName, &worker.LastSeenAt, &metadata); err != nil {
			return Metrics{}, err
		}
		_ = json.Unmarshal(metadata, &worker.Metadata)
		out.Workers = append(out.Workers, worker)
	}
	return out, workerRows.Err()
}
