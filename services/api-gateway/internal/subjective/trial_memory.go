package subjective

import (
	"context"
	"encoding/json"
	"sort"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/grading"
)

// BindScoringTrialRun 只给内存夹具绑定真实运行范围；生产实现从 scoring_run 和 review_task 读取。
func (s *MemoryStore) BindScoringTrialRun(tenantID, runID string, segmentIDs []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.trialRunSegments == nil {
		s.trialRunSegments = map[string][]string{}
	}
	s.trialRunSegments[key(tenantID, runID)] = cloneStrings(segmentIDs)
}

type memoryTrialRepository struct {
	store    *MemoryStore
	tenantID string
}

func (r memoryTrialRepository) Scope(runID, questionID string) (trialScope, error) {
	ids, exists := r.store.trialRunSegments[key(r.tenantID, runID)]
	if !exists {
		return trialScope{}, ErrNotFound
	}
	scope := trialScope{Segments: map[string]trialScopeSegment{}, Covered: map[string]bool{}}
	for _, segmentID := range ids {
		value, ok := r.store.contexts[key(r.tenantID, segmentID)]
		if !ok || value.Question.ID != questionID {
			continue
		}
		mode := string(value.AssessmentSnapshot.ScoringPolicySnapshot.Mode)
		if mode != "AI_ASSIST" && mode != "AI_FAST_CONFIRM" {
			continue
		}
		if scope.ExamID == "" {
			scope.ExamID = value.Question.ExamID
		}
		if scope.ExamID != value.Question.ExamID {
			return scope, ErrInvalidInput
		}
		scope.Version = struct {
			Snapshot  any
			Rubric    any
			Reference any
			Question  any
		}{value.AssessmentSnapshot, value.Rubric, value.ReferenceContext, value.Question}
		maxScore := value.Question.Score
		if value.Rubric.MaxScore > 0 {
			maxScore = value.Rubric.MaxScore
		}
		scope.Segments[segmentID] = trialScopeSegment{MaxScore: maxScore, AnswerIdentity: struct {
			Version string
			Text    string
			Image   any
		}{value.AnswerVersion, value.AnswerText, value.AnswerImageRef}}
		// 与生产相同，已创建模型运行或已有 AI 成绩的同版答卷不再作为新的独立基准。
		for _, run := range r.store.runs {
			if run.TenantID == r.tenantID && run.AnswerSegmentID == segmentID && run.AnswerVersion == value.AnswerVersion {
				scope.Covered[segmentID] = true
			}
		}
		for _, grade := range r.store.grades[key(r.tenantID, segmentID)] {
			if grade.AnswerVersion == "" || grade.AnswerVersion == value.AnswerVersion {
				scope.Covered[segmentID] = true
			}
		}
	}
	if len(scope.Segments) == 0 {
		return scope, ErrTrialConflict
	}
	for _, batch := range r.store.batches {
		if batch.TenantID == r.tenantID && batch.ScoringRunID == runID && batch.Status != "cancelled" {
			for _, id := range batch.SegmentIDs {
				scope.Covered[id] = true
			}
		}
	}
	return scope, nil
}

func cloneTrial(trial ScoringTrial) ScoringTrial {
	raw, _ := json.Marshal(trial)
	var out ScoringTrial
	_ = json.Unmarshal(raw, &out)
	out.TenantID, out.RequestHash = trial.TenantID, trial.RequestHash
	return out
}

func (r memoryTrialRepository) List(runID string) ([]ScoringTrial, error) {
	trials := []ScoringTrial{}
	for _, trial := range r.store.trials {
		if trial.TenantID == r.tenantID && trial.RunID == runID {
			trials = append(trials, cloneTrial(trial))
		}
	}
	sort.Slice(trials, func(i, j int) bool { return trials[i].CreatedAt.Before(trials[j].CreatedAt) })
	return trials, nil
}

func (r memoryTrialRepository) Get(id string) (ScoringTrial, error) {
	trial, ok := r.store.trials[key(r.tenantID, id)]
	if !ok {
		return trial, ErrNotFound
	}
	return cloneTrial(trial), nil
}
func (r memoryTrialRepository) Save(trial ScoringTrial, _ bool) error {
	if r.store.trials == nil {
		r.store.trials = map[string]ScoringTrial{}
	}
	r.store.trials[key(r.tenantID, trial.ID)] = cloneTrial(trial)
	return nil
}
func (r memoryTrialRepository) CreateBatch(actorID, runID, commandID string, segments []string) (string, error) {
	segments, err := normalizeBatchSegments(segments)
	if err != nil {
		return "", err
	}
	now := time.Now().UTC()
	id := r.store.id("subjective-batch")
	r.store.batches[key(r.tenantID, commandID)] = GradingBatch{ID: id, TenantID: r.tenantID, CreatedBy: actorID, ScoringRunID: runID, IdempotencyKey: commandID, Status: "planned", SegmentIDs: cloneStrings(segments), TotalCount: len(segments), CreatedAt: now, UpdatedAt: now}
	return id, nil
}

func (r memoryTrialRepository) CancelBatch(batchID string) error {
	for batchKey, batch := range r.store.batches {
		if batch.TenantID == r.tenantID && batch.ID == batchID {
			batch.Status = "cancelled"
			batch.UpdatedAt = time.Now().UTC()
			r.store.batches[batchKey] = batch
		}
	}
	for runKey, run := range r.store.runs {
		if run.TenantID == r.tenantID && run.BatchID == batchID && workerRunActive(run) {
			run.Status = RunFailed
			run.ErrorCode = "scoring_trial_stopped"
			now := time.Now().UTC()
			run.CompletedAt = &now
			run.UpdatedAt = now
			r.store.runs[runKey] = run
		}
	}
	return nil
}
func (r memoryTrialRepository) Results(trial *ScoringTrial) error {
	for index := range trial.Samples {
		sample := &trial.Samples[index]
		sample.Result = nil
		for _, run := range r.store.runs {
			if run.TenantID != r.tenantID || run.BatchID != trial.SampleBatchID || run.AnswerSegmentID != sample.SegmentID {
				continue
			}
			result := TrialSampleResult{Status: run.Status, ErrorCode: run.ErrorCode, Evidence: []grading.Evidence{}, RiskFlags: []string{}}
			for _, grade := range r.store.grades[key(r.tenantID, sample.SegmentID)] {
				if grade.ID == run.GradeID && grade.RunID == run.ID && grade.AdapterRequestID == run.RequestID {
					attachTrialGrade(&result, grade)
				}
			}
			if result.Status == RunSucceeded && result.GradeID == "" {
				result.Status = RunFailed
				result.ErrorCode = "trial_grade_missing"
			}
			sample.Result = &result
		}
	}
	return nil
}

func attachTrialGrade(result *TrialSampleResult, grade Grade) {
	result.GradeID = grade.ID
	if result.Status == RunSucceeded && (grade.Status == "suggested" || grade.Status == RunSucceeded) {
		score := grade.SuggestedScore
		result.SuggestedScore = &score
	}
	result.Evidence = cloneEvidence(grade.Evidence)
	result.RiskFlags = cloneStrings(grade.RiskFlags)
	if result.Evidence == nil {
		result.Evidence = []grading.Evidence{}
	}
	if result.RiskFlags == nil {
		result.RiskFlags = []string{}
	}
	if grade.AdapterLatencyMS > 0 {
		latency := grade.AdapterLatencyMS
		result.LatencyMS = &latency
	}
	// 老适配器不回用量时保持未知，不能把默认零值当成实际零成本。
	if grade.TotalTokens > 0 {
		tokens := grade.TotalTokens
		result.TotalTokens = &tokens
	}
}

func (s *MemoryStore) CreateScoringTrial(_ context.Context, tenantID, actorID, runID string, input CreateTrialInput, policy ModelPolicy) (ScoringTrial, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return createTrial(memoryTrialRepository{s, tenantID}, tenantID, actorID, runID, input, policy)
}
func (s *MemoryStore) ListScoringTrials(_ context.Context, tenantID, runID string, policy ModelPolicy) ([]ScoringTrial, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	repo := memoryTrialRepository{s, tenantID}
	trials, err := repo.List(runID)
	if err != nil {
		return nil, err
	}
	for index := range trials {
		trials[index], err = refreshTrial(repo, trials[index], policy)
		if err != nil {
			return nil, err
		}
	}
	return trials, nil
}
func (s *MemoryStore) DecideScoringTrial(_ context.Context, tenantID, actorID, runID, trialID string, input TrialDecisionInput, policy ModelPolicy) (ScoringTrial, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return decideTrial(memoryTrialRepository{s, tenantID}, actorID, runID, trialID, input, policy)
}
func (s *MemoryStore) PrepareScoringTrialEnqueue(_ context.Context, tenantID, runID, trialID string, policy ModelPolicy) (ScoringTrial, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return prepareTrialEnqueue(memoryTrialRepository{s, tenantID}, runID, trialID, policy)
}
func (s *MemoryStore) AuthorizeTrialBatch(_ context.Context, tenantID, batchID string, policy ModelPolicy) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, batch := range s.batches {
		if batch.TenantID == tenantID && batch.ID == batchID {
			if batch.ScoringRunID == "" {
				return nil
			}
			if _, required := s.trialRunSegments[key(tenantID, batch.ScoringRunID)]; required {
				trials, err := (memoryTrialRepository{s, tenantID}).List(batch.ScoringRunID)
				if err != nil {
					return err
				}
				if len(trials) == 0 {
					return ErrTrialConflict
				}
			}
			return authorizeTrialBatch(memoryTrialRepository{s, tenantID}, batch.ScoringRunID, batchID, policy)
		}
	}
	return ErrNotFound
}

func (s *MemoryStore) markTrialSampleLocked(tenantID string, grade Grade) Grade {
	for _, trial := range s.trials {
		if trial.TenantID != tenantID {
			continue
		}
		for _, sample := range trial.Samples {
			if sample.SegmentID == grade.AnswerSegmentID {
				return markTrialGrade(grade)
			}
		}
	}
	return grade
}

func markTrialGrade(grade Grade) Grade {
	for _, flag := range grade.RiskFlags {
		if flag == "trial_sample" {
			grade.NeedsHumanReview = true
			return grade
		}
	}
	grade.RiskFlags = append(cloneStrings(grade.RiskFlags), "trial_sample")
	grade.NeedsHumanReview = true
	return grade
}
