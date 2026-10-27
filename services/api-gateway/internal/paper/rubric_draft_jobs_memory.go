package paper

import (
	"context"
	"encoding/json"
	"sort"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/exam"
	"github.com/google/uuid"
)

func existingRubricDraftJob(records []rubricDraftJobRecord, next rubricDraftJobRecord, regenerate bool) (*RubricDraftJob, error) {
	for _, record := range records {
		if record.Job.RequestID == next.Job.RequestID {
			if record.Fingerprint != next.Fingerprint || record.Job.QuestionID != next.Job.QuestionID || record.Job.ExamID != next.Job.ExamID {
				return nil, ErrConflict
			}
			return &record.Job, nil
		}
	}
	for _, record := range records {
		if record.Job.QuestionID != next.Job.QuestionID {
			continue
		}
		if rubricDraftActive(record.Job.Status) {
			if record.Fingerprint != next.Fingerprint {
				return nil, ErrConflict
			}
			return &record.Job, nil
		}
	}
	// 重复点击复用最近一轮成功结果；重试失败或明确重新生成才创建新一轮。
	for _, record := range records {
		if record.Job.QuestionID != next.Job.QuestionID {
			continue
		}
		if !regenerate && record.Fingerprint == next.Fingerprint && record.Job.Status == "succeeded" {
			return &record.Job, nil
		}
		break
	}
	return nil, nil
}

func rubricDraftActive(status string) bool {
	return status == "queued" || status == "running" || status == "cancelling"
}

func cloneRubricDraftRecord(record rubricDraftJobRecord) rubricDraftJobRecord {
	data, _ := json.Marshal(record)
	var result rubricDraftJobRecord
	_ = json.Unmarshal(data, &result)
	return result
}

func (s *MemoryStore) withRubricDraftExamState(ctx context.Context, tenantID, examID string, action func(bool) error) error {
	s.mu.RLock()
	boundary := s.rubricDraftExamBoundary
	s.mu.RUnlock()
	if boundary == nil {
		return action(false)
	}
	err := boundary.WithManagementExamState(ctx, tenantID, examID, action)
	if err == exam.ErrNotFound {
		return ErrNotFound
	}
	return err
}

func (s *MemoryStore) HasActiveManagementExamWork(tenantID, examID string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, record := range s.rubricDraftJobs {
		if record.TenantID == tenantID && record.Job.ExamID == examID && rubricDraftActive(record.Job.Status) {
			return true
		}
	}
	return false
}

func (s *MemoryStore) createRubricDraftJob(ctx context.Context, record rubricDraftJobRecord, regenerate bool) (RubricDraftJob, error) {
	var job RubricDraftJob
	err := s.withRubricDraftExamState(ctx, record.TenantID, record.Job.ExamID, func(archived bool) error {
		if archived {
			return ErrExamFrozen
		}
		var err error
		job, err = s.createRubricDraftJobMemory(record, regenerate)
		return err
	})
	return job, err
}

func (s *MemoryStore) createRubricDraftJobMemory(record rubricDraftJobRecord, regenerate bool) (RubricDraftJob, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var records []rubricDraftJobRecord
	waiting := 0
	for _, item := range s.rubricDraftJobs {
		if item.TenantID == record.TenantID && item.ActorID == record.ActorID {
			records = append(records, cloneRubricDraftRecord(item))
			if item.Job.Status == "queued" {
				waiting++
			}
		}
	}
	sort.Slice(records, func(i, j int) bool { return records[i].Job.CreatedAt.After(records[j].Job.CreatedAt) })
	if existing, err := existingRubricDraftJob(records, record, regenerate); existing != nil || err != nil {
		if existing != nil {
			return *existing, err
		}
		return RubricDraftJob{}, err
	}
	if waiting >= rubricDraftQueueLimit {
		return RubricDraftJob{}, ErrRubricDraftQueueFull
	}
	s.rubricDraftJobs[record.Job.ID] = cloneRubricDraftRecord(record)
	return record.Job, nil
}

func (s *MemoryStore) listRubricDraftJobs(_ context.Context, tenantID, actorID, examID string) ([]RubricDraftJob, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	items := []RubricDraftJob{}
	for _, record := range s.rubricDraftJobs {
		if record.TenantID == tenantID && record.ActorID == actorID && record.Job.ExamID == examID {
			item := cloneRubricDraftRecord(record).Job
			item.Result = nil
			items = append(items, item)
		}
	}
	sort.Slice(items, func(i, j int) bool {
		if rubricDraftActive(items[i].Status) != rubricDraftActive(items[j].Status) {
			return rubricDraftActive(items[i].Status)
		}
		return items[i].CreatedAt.After(items[j].CreatedAt)
	})
	if len(items) > 100 {
		items = items[:100]
	}
	return items, nil
}

func (s *MemoryStore) getRubricDraftJob(ctx context.Context, tenantID, actorID, examID, id string) (rubricDraftJobRecord, error) {
	var out rubricDraftJobRecord
	err := s.withRubricDraftExamState(ctx, tenantID, examID, func(archived bool) error {
		s.mu.RLock()
		defer s.mu.RUnlock()
		record, ok := s.rubricDraftJobs[id]
		if !ok || record.TenantID != tenantID || record.ActorID != actorID || record.Job.ExamID != examID {
			return ErrNotFound
		}
		out = cloneRubricDraftRecord(record)
		if archived {
			// 归档后的读取保留历史任务，不返回可采用的新建议，也不为读操作改写归档内容。
			out.Job.Status, out.Job.Result, out.Job.Error = "stale", nil, "考试已归档，不能采用该建议"
		}
		return nil
	})
	return out, err
}

func (s *MemoryStore) cancelRubricDraftJob(_ context.Context, tenantID, actorID, examID, id string) (RubricDraftJob, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok := s.rubricDraftJobs[id]
	if !ok || record.TenantID != tenantID || record.ActorID != actorID || record.Job.ExamID != examID {
		return RubricDraftJob{}, ErrNotFound
	}
	if record.Job.Status == "queued" {
		record.Job.Status = "cancelled"
	}
	// 已经发出的模型调用可能计费；保持占位，直到响应或超时，不能立即释放并发名额。
	if record.Job.Status == "running" {
		record.Job.Status = "cancelling"
	}
	record.Job.UpdatedAt = time.Now().UTC()
	s.rubricDraftJobs[id] = record
	return cloneRubricDraftRecord(record).Job, nil
}

func (s *MemoryStore) claimRubricDraftJob(_ context.Context, lease time.Duration, id string) (*rubricDraftJobRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now, running, local := time.Now().UTC(), 0, 0
	for id, record := range s.rubricDraftJobs {
		if record.Job.Status != "running" && record.Job.Status != "cancelling" {
			continue
		}
		if !record.LeaseUntil.After(now) {
			cancelled := record.Job.Status == "cancelling"
			record.Job.Status, record.Job.Error = "failed", "执行中断，请手动重试；上次调用可能已计费"
			if cancelled {
				record.Job.Status = "cancelled"
			}
			record.Job.UpdatedAt = now
			s.rubricDraftJobs[id] = record
			continue
		}
		running++
		if record.Local {
			local++
		}
	}
	if running >= RubricDraftConcurrency {
		return nil, nil
	}
	var next *rubricDraftJobRecord
	for _, record := range s.rubricDraftJobs {
		if id != "" && record.Job.ID != id {
			continue
		}
		if record.Job.Status != "queued" || record.AvailableAt.After(now) || record.Local && local > 0 {
			continue
		}
		if next == nil || record.Job.CreatedAt.Before(next.Job.CreatedAt) {
			copy := record
			next = &copy
		}
	}
	if next == nil {
		return nil, nil
	}
	next.Job.Status, next.Job.UpdatedAt = "running", now
	next.Job.Attempt++
	next.LeaseToken, next.LeaseUntil = uuid.NewString(), now.Add(lease)
	s.rubricDraftJobs[next.Job.ID] = *next
	copy := cloneRubricDraftRecord(*next)
	return &copy, nil
}

func (s *MemoryStore) finishRubricDraftJob(ctx context.Context, record rubricDraftJobRecord, status string, result *MathRubricDraftResponse, message string, retryAt time.Time) error {
	return s.withRubricDraftExamState(ctx, record.TenantID, record.Job.ExamID, func(archived bool) error {
		if archived {
			status, result, message = "stale", nil, "考试已归档，旧任务结果已丢弃"
		}
		return s.finishRubricDraftJobMemory(record, status, result, message, retryAt)
	})
}

func (s *MemoryStore) finishRubricDraftJobMemory(record rubricDraftJobRecord, status string, result *MathRubricDraftResponse, message string, retryAt time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	current, ok := s.rubricDraftJobs[record.Job.ID]
	if !ok || current.TenantID != record.TenantID || current.LeaseToken != record.LeaseToken || !current.LeaseUntil.After(time.Now()) || (current.Job.Status != "running" && current.Job.Status != "cancelling") {
		return ErrConflict
	}
	if current.Job.Status == "cancelling" {
		status, result, message = "cancelled", nil, ""
	}
	current.Job.Status, current.Job.Result, current.Job.Error, current.Job.UpdatedAt = status, result, message, time.Now().UTC()
	current.LeaseToken, current.LeaseUntil, current.AvailableAt = "", time.Time{}, retryAt
	s.rubricDraftJobs[record.Job.ID] = cloneRubricDraftRecord(current)
	return nil
}

func (s *MemoryStore) staleRubricDraftJob(_ context.Context, tenantID, actorID, examID, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok := s.rubricDraftJobs[id]
	if !ok || record.TenantID != tenantID || record.ActorID != actorID || record.Job.ExamID != examID {
		return ErrNotFound
	}
	if record.Job.Status == "succeeded" {
		record.Job.Status, record.Job.Result, record.Job.Error = "stale", nil, "题目、答案、细则或模型已变更，请重新生成"
		record.Job.UpdatedAt = time.Now().UTC()
		s.rubricDraftJobs[id] = record
	}
	return nil
}
