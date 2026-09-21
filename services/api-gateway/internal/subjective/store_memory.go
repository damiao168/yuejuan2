package subjective

import (
	"context"
	"fmt"
	"sync"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/grading"
	"edugrade-enterprise/services/api-gateway/internal/paper"
)

type MemoryStore struct {
	mu            sync.RWMutex
	next          int
	contexts      map[string]Context
	grades        map[string][]Grade
	runs          map[string]GradingRun
	panels        map[string]GradingPanel
	panelPolicies map[string]PanelPolicy
	panelReviews  map[string]string
	batches       map[string]GradingBatch
	enqueuePlans  map[string]memoryEnqueuePlan
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{next: 1, contexts: map[string]Context{}, grades: map[string][]Grade{}, runs: map[string]GradingRun{}, panels: map[string]GradingPanel{}, panelPolicies: map[string]PanelPolicy{}, panelReviews: map[string]string{}, batches: map[string]GradingBatch{}, enqueuePlans: map[string]memoryEnqueuePlan{}}
}

func (s *MemoryStore) AddContext(tenantID string, segmentID string, ctx Context) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ctx.SegmentID = segmentID
	s.contexts[key(tenantID, segmentID)] = ctx
}

func (s *MemoryStore) LoadContext(_ context.Context, tenantID string, segmentID string) (Context, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	ctx, ok := s.contexts[key(tenantID, segmentID)]
	if !ok {
		return Context{}, ErrNotFound
	}
	if ctx.AnswerText == "" {
		return Context{}, ErrAnswerMissing
	}
	if ctx.Rubric.ID == "" {
		return Context{}, ErrRubricMissing
	}
	return ctx, nil
}

func (s *MemoryStore) LoadContexts(_ context.Context, tenantID string, segmentIDs []string) ([]Context, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	contexts := make([]Context, 0, len(segmentIDs))
	for _, segmentID := range segmentIDs {
		ctx, ok := s.contexts[key(tenantID, segmentID)]
		if !ok {
			return nil, ErrNotFound
		}
		if ctx.AnswerText == "" {
			return nil, ErrAnswerMissing
		}
		if ctx.Rubric.ID == "" {
			return nil, ErrRubricMissing
		}
		contexts = append(contexts, ctx)
	}
	return contexts, nil
}

func (s *MemoryStore) CreateGrade(_ context.Context, tenantID string, actorID string, grade Grade) (Grade, error) {
	if grade.AnswerSegmentID == "" || grade.QuestionID == "" {
		return Grade{}, ErrInvalidInput
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if grade.AdapterRequestID != "" {
		for _, existing := range s.grades[key(tenantID, grade.AnswerSegmentID)] {
			if existing.AdapterRequestID == grade.AdapterRequestID {
				if sameGradeIdentity(existing, grade) {
					return existing, nil
				}
				return Grade{}, ErrIdempotencyConflict
			}
		}
	}
	grade.ID = s.id("ai-grade")
	grade.TenantID = tenantID
	grade.CreatedBy = actorID
	grade.CreatedAt = time.Now().UTC()
	grade.RawOutput = cloneMap(grade.RawOutput)
	grade.MatchedPoints = clonePoints(grade.MatchedPoints)
	grade.MissingPoints = clonePoints(grade.MissingPoints)
	grade.Evidence = cloneEvidence(grade.Evidence)
	grade.RiskFlags = cloneStrings(grade.RiskFlags)
	s.grades[key(tenantID, grade.AnswerSegmentID)] = append(s.grades[key(tenantID, grade.AnswerSegmentID)], grade)
	return grade, nil
}

func (s *MemoryStore) GetGradeByAdapterRequestID(_ context.Context, tenantID string, requestID string) (Grade, error) {
	if requestID == "" {
		return Grade{}, ErrNotFound
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, grades := range s.grades {
		for _, grade := range grades {
			if grade.TenantID == tenantID && grade.AdapterRequestID == requestID {
				return grade, nil
			}
		}
	}
	return Grade{}, ErrNotFound
}

func (s *MemoryStore) GetOrCreateRun(_ context.Context, tenantID string, _ string, input CreateRunInput) (GradingRun, error) {
	if tenantID == "" || input.AnswerSegmentID == "" || input.RequestID == "" {
		return GradingRun{}, ErrInvalidInput
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if input.AgentRole == "" {
		input.AgentRole = AgentRoleSingle
	}
	if !validRunPanelRole(input.PanelID, input.AgentRole) {
		return GradingRun{}, ErrInvalidInput
	}
	if input.PanelID != "" {
		panel, ok := s.panels[key(tenantID, input.PanelID)]
		if !ok || !panelRunSnapshotMatches(panel, input) {
			return GradingRun{}, ErrIdempotencyConflict
		}
		for _, existing := range s.runs {
			if existing.TenantID == tenantID && existing.PanelID == input.PanelID &&
				existing.AgentRole == input.AgentRole && existing.RequestID != input.RequestID {
				return GradingRun{}, ErrIdempotencyConflict
			}
		}
	}
	runKey := key(tenantID, input.RequestID)
	if existing, ok := s.runs[runKey]; ok {
		if input.PanelID != "" && !runPanelIdentityMatches(existing, input) {
			return GradingRun{}, ErrIdempotencyConflict
		}
		return existing, nil
	}
	now := time.Now().UTC()
	status := RunProcessing
	attemptCount := 1
	var startedAt *time.Time
	if input.BatchID == "" && input.PanelID == "" {
		startedAt = &now
	} else {
		status = RunQueued
		attemptCount = 0
	}
	run := GradingRun{ID: s.id("subjective-run"), TenantID: tenantID, AnswerSegmentID: input.AnswerSegmentID, BatchID: input.BatchID, AnswerVersion: input.AnswerVersion, QuestionID: input.QuestionID, RubricVersion: input.RubricVersion, ModelVersion: input.ModelVersion, PromptVersion: input.PromptVersion, MinConfidence: input.MinConfidence, RequestID: input.RequestID, PanelID: input.PanelID, AgentRole: input.AgentRole,
		MathArtifactID: input.MathArtifactID, MathArtifactVersion: input.MathArtifactVersion, MathCorrectionRevision: input.MathCorrectionRevision, MathScoringVersion: input.MathScoringVersion,
		Status: status, AttemptCount: attemptCount, StartedAt: startedAt, CreatedAt: now, UpdatedAt: now}
	s.runs[runKey] = run
	return run, nil
}

func (s *MemoryStore) ClaimPanelRun(_ context.Context, tenantID, runID string) (GradingRun, bool, error) {
	if tenantID == "" || runID == "" {
		return GradingRun{}, false, ErrInvalidInput
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for runKey, run := range s.runs {
		if run.TenantID != tenantID || run.ID != runID {
			continue
		}
		if run.PanelID == "" || run.AgentRole == AgentRoleSingle {
			return GradingRun{}, false, ErrInvalidInput
		}
		if run.Status != RunQueued {
			return run, false, nil
		}
		now := time.Now().UTC()
		run.Status, run.AttemptCount, run.StartedAt, run.UpdatedAt = RunProcessing, run.AttemptCount+1, &now, now
		s.runs[runKey] = run
		return run, true, nil
	}
	return GradingRun{}, false, ErrNotFound
}

func validRunPanelRole(panelID, role string) bool {
	if panelID == "" {
		return role == AgentRoleSingle
	}
	return role == AgentRolePrimaryA || role == AgentRolePrimaryB || role == AgentRoleArbiter
}

func (s *MemoryStore) GetRun(_ context.Context, tenantID string, runID string) (GradingRun, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, run := range s.runs {
		if run.TenantID == tenantID && run.ID == runID {
			return run, nil
		}
	}
	return GradingRun{}, ErrNotFound
}

func (s *MemoryStore) UpdateRun(_ context.Context, tenantID string, runID string, input UpdateRunInput) (GradingRun, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for runKey, run := range s.runs {
		if run.TenantID != tenantID || run.ID != runID {
			continue
		}
		if input.Status != RunQueued && input.Status != RunProcessing && input.Status != RunSucceeded && input.Status != RunFailed && input.Status != RunConflict {
			return GradingRun{}, ErrInvalidInput
		}
		run.Status = input.Status
		if input.GradeID != "" {
			run.GradeID = input.GradeID
		}
		if input.ErrorCode != "" {
			run.ErrorCode = input.ErrorCode
		}
		if input.AttemptCount > 0 {
			run.AttemptCount = input.AttemptCount
		}
		now := time.Now().UTC()
		run.UpdatedAt = now
		if input.Status == RunProcessing && run.StartedAt == nil {
			run.StartedAt = &now
		}
		if input.Status == RunSucceeded || input.Status == RunFailed || input.Status == RunConflict {
			run.CompletedAt = &now
		} else {
			run.CompletedAt = nil
		}
		s.runs[runKey] = run
		return run, nil
	}
	return GradingRun{}, ErrNotFound
}

func (s *MemoryStore) CreateBatch(_ context.Context, tenantID string, actorID string, input CreateBatchInput) (GradingBatch, error) {
	segments, err := normalizeBatchSegments(input.SegmentIDs)
	if err != nil || tenantID == "" || input.IdempotencyKey == "" {
		return GradingBatch{}, ErrInvalidInput
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	batchKey := key(tenantID, input.IdempotencyKey)
	if existing, ok := s.batches[batchKey]; ok {
		if existing.CreatedBy != actorID || !sameStringSlice(existing.SegmentIDs, segments) {
			return GradingBatch{}, ErrIdempotencyConflict
		}
		return existing, nil
	}
	now := time.Now().UTC()
	batch := GradingBatch{ID: s.id("subjective-batch"), TenantID: tenantID, IdempotencyKey: input.IdempotencyKey, Status: "planned", SegmentIDs: cloneStrings(segments), TotalCount: len(segments), CreatedBy: actorID, CreatedAt: now, UpdatedAt: now}
	s.batches[batchKey] = batch
	return batch, nil
}

func (s *MemoryStore) GetBatch(_ context.Context, tenantID string, batchID string) (GradingBatch, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, batch := range s.batches {
		if batch.TenantID == tenantID && batch.ID == batchID {
			batch.SegmentIDs = cloneStrings(batch.SegmentIDs)
			return batch, nil
		}
	}
	return GradingBatch{}, ErrNotFound
}

func (s *MemoryStore) RefreshBatch(_ context.Context, tenantID string, batchID string) (GradingBatch, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for batchKey, batch := range s.batches {
		if batch.TenantID != tenantID || batch.ID != batchID {
			continue
		}
		if batch.Status == "planned" || batch.Status == "cancelled" {
			batch.SegmentIDs = cloneStrings(batch.SegmentIDs)
			return batch, nil
		}
		queued, processing, succeeded, failed := 0, 0, 0, 0
		for _, run := range s.runs {
			if run.TenantID != tenantID || run.BatchID != batchID {
				continue
			}
			switch run.Status {
			case RunQueued:
				queued++
			case RunProcessing:
				processing++
			case RunSucceeded:
				succeeded++
			case RunFailed, RunConflict:
				failed++
			}
		}
		status := "processing"
		if succeeded+failed == batch.TotalCount {
			if failed > 0 {
				status = "failed"
			} else {
				status = "completed"
			}
		}
		batch.Status, batch.QueuedCount, batch.ProcessingCount, batch.SucceededCount, batch.FailedCount = status, queued, processing, succeeded, failed
		batch.UpdatedAt = time.Now().UTC()
		s.batches[batchKey] = batch
		batch.SegmentIDs = cloneStrings(batch.SegmentIDs)
		return batch, nil
	}
	return GradingBatch{}, ErrNotFound
}

func (s *MemoryStore) UpdateBatch(_ context.Context, tenantID string, batchID string, input UpdateBatchInput) (GradingBatch, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for batchKey, batch := range s.batches {
		if batch.TenantID != tenantID || batch.ID != batchID {
			continue
		}
		if input.Status != "planned" && input.Status != "processing" && input.Status != "completed" && input.Status != "failed" && input.Status != "cancelled" {
			return GradingBatch{}, ErrInvalidInput
		}
		if input.QueuedCount < 0 || input.ProcessingCount < 0 || input.SucceededCount < 0 || input.FailedCount < 0 || input.QueuedCount+input.ProcessingCount+input.SucceededCount+input.FailedCount > batch.TotalCount {
			return GradingBatch{}, ErrInvalidInput
		}
		batch.Status, batch.QueuedCount, batch.ProcessingCount, batch.SucceededCount, batch.FailedCount = input.Status, input.QueuedCount, input.ProcessingCount, input.SucceededCount, input.FailedCount
		batch.UpdatedAt = time.Now().UTC()
		s.batches[batchKey] = batch
		return batch, nil
	}
	return GradingBatch{}, ErrNotFound
}

func normalizeBatchSegments(input []string) ([]string, error) {
	if len(input) == 0 || len(input) > 1000 {
		return nil, ErrInvalidInput
	}
	seen := map[string]struct{}{}
	segments := make([]string, 0, len(input))
	for _, segmentID := range input {
		if segmentID == "" {
			return nil, ErrInvalidInput
		}
		if _, ok := seen[segmentID]; ok {
			return nil, ErrInvalidInput
		}
		seen[segmentID] = struct{}{}
		segments = append(segments, segmentID)
	}
	return segments, nil
}

func sameStringSlice(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func ContextForTest(kind string, score float64, answerText string, ocrConfidence *float64) Context {
	return Context{
		AnswerVersion: "answer-v1",
		Subject:       "chinese",
		GradeLevel:    "junior",
		Question: paper.Question{
			ID:           "question-" + kind,
			TenantID:     "00000000-0000-0000-0000-000000000002",
			ExamID:       "exam-1",
			QuestionNo:   "Q1",
			QuestionType: kind,
			Score:        score,
			Stem:         "synthetic question",
		},
		Rubric: paper.Rubric{
			ID:         "rubric-" + kind,
			QuestionID: "question-" + kind,
			Version:    "v1",
			Status:     "approved",
			MaxScore:   score,
			Points: []paper.RubricPoint{
				{ID: "p1", Description: "synthetic rubric point", Score: score, Required: true},
			},
		},
		AnswerText:     answerText,
		AnswerImageRef: map[string]any{"answer_segment_id": "segment-1"},
		OCRConfidence:  ocrConfidence,
	}
}

func key(tenantID string, segmentID string) string {
	return tenantID + "|" + segmentID
}

func (s *MemoryStore) id(prefix string) string {
	id := fmt.Sprintf("%s-%d", prefix, s.next)
	s.next++
	return id
}

func cloneMap(in map[string]any) map[string]any {
	if in == nil {
		return map[string]any{}
	}
	out := map[string]any{}
	for key, value := range in {
		out[key] = value
	}
	return out
}

func cloneStrings(in []string) []string {
	out := make([]string, len(in))
	copy(out, in)
	return out
}

func clonePoints(in []grading.PointResult) []grading.PointResult {
	out := make([]grading.PointResult, len(in))
	copy(out, in)
	return out
}

func cloneEvidence(in []grading.Evidence) []grading.Evidence {
	out := make([]grading.Evidence, len(in))
	copy(out, in)
	return out
}
