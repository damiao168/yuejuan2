package workerruntime

import (
	"context"
	"sort"
	"time"
)

func (s *MemoryStore) Metrics(_ context.Context, tenantID string) (Metrics, error) {
	return s.metrics(tenantID, false), nil
}

func (s *MemoryStore) MetricsAcrossTenants(_ context.Context, platformTenantID string) (Metrics, error) {
	return s.metrics(platformTenantID, true), nil
}

func (s *MemoryStore) metrics(tenantID string, acrossTenants bool) Metrics {
	// 任务和 worker 心跳在同一读锁内统计，期间写入方不能改动这些存储记录。
	s.mu.RLock()
	defer s.mu.RUnlock()
	byQueue := map[string]*QueueMetrics{}
	durations := map[string][]int{}
	cutoff := time.Now().UTC().Add(-time.Hour)
	for _, task := range s.tasks {
		if !acrossTenants && task.TenantID != tenantID {
			continue
		}
		metric := byQueue[task.QueueName]
		if metric == nil {
			metric = &QueueMetrics{QueueName: task.QueueName}
			byQueue[task.QueueName] = metric
		}
		updateTaskQueueMetrics(metric, task, cutoff)
		// 时长来自任务上的正值，包含非成功状态；不能改用尝试记录的时长。
		if task.DurationMS > 0 {
			durations[task.QueueName] = append(durations[task.QueueName], task.DurationMS)
		}
		metric.RetryLastHour += taskRetriesAfter(task, cutoff)
	}
	out := Metrics{Queues: []QueueMetrics{}, Workers: []WorkerHeartbeat{}}
	for queue, metric := range byQueue {
		metric.P95DurationMS = percentile95(durations[queue])
		out.Queues = append(out.Queues, *metric)
	}
	sort.Slice(out.Queues, func(i, j int) bool { return out.Queues[i].QueueName < out.Queues[j].QueueName })
	for _, worker := range s.heartbeats {
		if acrossTenants || worker.TenantID == tenantID {
			out.Workers = append(out.Workers, worker)
		}
	}
	sort.Slice(out.Workers, func(i, j int) bool { return out.Workers[i].LastSeenAt.After(out.Workers[j].LastSeenAt) })
	return out
}

func updateTaskQueueMetrics(metric *QueueMetrics, task Task, cutoff time.Time) {
	switch task.Status {
	case StatusQueued:
		metric.Queued++
	case StatusLeased:
		metric.Leased++
	case StatusRunning:
		metric.Running++
	case StatusSucceeded:
		metric.Succeeded++
	case StatusFailed:
		metric.Failed++
	case StatusDeadLetter:
		metric.DeadLetter++
	case StatusCancelled:
		metric.Cancelled++
	}
	if (task.Status == StatusFailed || task.Status == StatusDeadLetter) && task.UpdatedAt.After(cutoff) {
		metric.FailedLastHour++
	}
}

func taskRetriesAfter(task Task, cutoff time.Time) int {
	retries := 0
	// 统计失败的尝试记录，仅在任务当前尝试数小于上限时计数；不能改用尝试自身序号。
	for _, attempt := range task.Attempts {
		if attempt.Status == StatusFailed && attempt.CompletedAt != nil && attempt.CompletedAt.After(cutoff) && task.AttemptCount < task.MaxAttempts {
			retries++
		}
	}
	return retries
}
