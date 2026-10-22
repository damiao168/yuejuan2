package subjective

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
)

type trialScopeSegment struct {
	MaxScore       float64
	AnswerIdentity any
}

type trialScope struct {
	ExamID   string
	Version  any
	Segments map[string]trialScopeSegment
	Covered  map[string]bool
}

type trialRepository interface {
	Scope(string, string) (trialScope, error)
	List(string) ([]ScoringTrial, error)
	Get(string) (ScoringTrial, error)
	Save(ScoringTrial, bool) error
	CreateBatch(string, string, string, []string) (string, error)
	Results(*ScoringTrial) error
	CancelBatch(string) error
}

func trialHash(value any) (string, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:]), nil
}

func normalizeTrialInput(input CreateTrialInput) (CreateTrialInput, error) {
	if strings.TrimSpace(input.CommandID) == "" || len(input.CommandID) > 128 || input.QuestionID == "" || len(input.Samples) < 5 || len(input.Samples) > 10 {
		return input, ErrInvalidInput
	}
	seen := map[string]bool{}
	roles := map[string]bool{}
	for index := range input.Samples {
		sample := &input.Samples[index]
		if sample.SegmentID == "" || seen[sample.SegmentID] || math.IsNaN(sample.HumanScore) || math.IsInf(sample.HumanScore, 0) || sample.HumanScore < 0 {
			return input, ErrInvalidInput
		}
		seen[sample.SegmentID] = true
		unique := map[string]bool{}
		for _, role := range sample.Roles {
			if role != "representative" && role != "boundary" && role != "incomplete_evidence" {
				return input, ErrInvalidInput
			}
			unique[role], roles[role] = true, true
		}
		sample.Roles = []string{}
		for role := range unique {
			sample.Roles = append(sample.Roles, role)
		}
		sort.Strings(sample.Roles)
	}
	if !roles["representative"] || !roles["boundary"] || !roles["incomplete_evidence"] {
		return input, ErrInvalidInput
	}
	sort.Slice(input.Samples, func(i, j int) bool { return input.Samples[i].SegmentID < input.Samples[j].SegmentID })
	return input, nil
}

func trialFingerprint(scope trialScope, samples []TrialSample, policy ModelPolicy) (string, error) {
	answers := map[string]any{}
	for _, sample := range samples {
		segment, ok := scope.Segments[sample.SegmentID]
		if !ok {
			return "", ErrTrialConflict
		}
		answers[sample.SegmentID] = segment.AnswerIdentity
	}
	return trialHash(struct {
		Version any
		Policy  ModelPolicy
		Answers map[string]any
	}{scope.Version, policy, answers})
}

func trialRemaining(scope trialScope, trial ScoringTrial) []string {
	selected := map[string]bool{}
	for _, sample := range trial.Samples {
		selected[sample.SegmentID] = true
	}
	remaining := []string{}
	for segmentID := range scope.Segments {
		if !selected[segmentID] && !scope.Covered[segmentID] {
			remaining = append(remaining, segmentID)
		}
	}
	sort.Strings(remaining)
	return remaining
}

func createTrial(repo trialRepository, tenantID, actorID, runID string, input CreateTrialInput, policy ModelPolicy) (ScoringTrial, error) {
	input, err := normalizeTrialInput(input)
	if err != nil || tenantID == "" || actorID == "" || runID == "" {
		return ScoringTrial{}, ErrInvalidInput
	}
	requestHash, err := trialHash(input)
	if err != nil {
		return ScoringTrial{}, err
	}
	existing, err := repo.List(runID)
	if err != nil {
		return ScoringTrial{}, err
	}
	for _, saved := range existing {
		if saved.CommandID == input.CommandID {
			if saved.CreatedBy != actorID || saved.RequestHash != requestHash {
				return ScoringTrial{}, ErrIdempotencyConflict
			}
			return refreshTrial(repo, saved, policy)
		}
		if saved.QuestionID == input.QuestionID && saved.Status != "stopped" && saved.Status != "invalidated" {
			saved, err = refreshTrial(repo, saved, policy)
			if err != nil {
				return ScoringTrial{}, err
			}
			if saved.Status != "invalidated" {
				return ScoringTrial{}, ErrTrialConflict
			}
		}
	}
	scope, err := repo.Scope(runID, input.QuestionID)
	if err != nil {
		return ScoringTrial{}, err
	}
	freshCount := 0
	for segmentID := range scope.Segments {
		if !scope.Covered[segmentID] {
			freshCount++
		}
	}
	if freshCount < 5 {
		return ScoringTrial{}, ErrTrialIndependentSamplesRequired
	}
	now := time.Now().UTC()
	trial := ScoringTrial{ID: uuid.NewString(), TenantID: tenantID, RunID: runID, ExamID: scope.ExamID, QuestionID: input.QuestionID, CommandID: input.CommandID, Status: "awaiting_enqueue", Revision: 1, Samples: []TrialSample{}, RemainingSegmentIDs: []string{}, CreatedBy: actorID, CreatedAt: now, UpdatedAt: now, RequestHash: requestHash}
	segments := []string{}
	for _, sample := range input.Samples {
		segment, ok := scope.Segments[sample.SegmentID]
		// 人工基准必须先于 AI；批次取消也不能抹去同版答卷已经启动过模型的事实。
		if !ok || segment.MaxScore <= 0 || sample.HumanScore > segment.MaxScore {
			return ScoringTrial{}, ErrInvalidInput
		}
		if scope.Covered[sample.SegmentID] {
			return ScoringTrial{}, ErrTrialIndependentSamplesRequired
		}
		trial.Samples = append(trial.Samples, TrialSample{SegmentID: sample.SegmentID, HumanScore: sample.HumanScore, Roles: sample.Roles, MaxScore: segment.MaxScore})
		segments = append(segments, sample.SegmentID)
	}
	trial.Fingerprint, err = trialFingerprint(scope, trial.Samples, policy)
	if err != nil {
		return ScoringTrial{}, err
	}
	trial.RemainingSegmentIDs = trialRemaining(scope, trial)
	trial.SampleBatchID, err = repo.CreateBatch(actorID, runID, "scoring-trial-"+trial.ID, segments)
	if err != nil {
		return ScoringTrial{}, err
	}
	if err := repo.Save(trial, true); err != nil {
		return ScoringTrial{}, err
	}
	return trial, nil
}

// 刷新只关联本试改的样本批次；版本或样本答案变化立即作废，不能把旧结果用于扩大建议生成。
func refreshTrial(repo trialRepository, trial ScoringTrial, policy ModelPolicy) (ScoringTrial, error) {
	if err := repo.Results(&trial); err != nil {
		return trial, err
	}
	if trial.Status == "stopped" || trial.Status == "invalidated" {
		return trial, nil
	}
	scope, err := repo.Scope(trial.RunID, trial.QuestionID)
	fingerprint := ""
	if err == nil {
		fingerprint, err = trialFingerprint(scope, trial.Samples, policy)
	}
	if err != nil && err != ErrTrialConflict && err != ErrNotFound {
		return trial, err
	}
	if err != nil || fingerprint != trial.Fingerprint {
		if err := repo.CancelBatch(trial.SampleBatchID); err != nil {
			return trial, err
		}
		if trial.ExpansionBatchID != "" {
			if err := repo.CancelBatch(trial.ExpansionBatchID); err != nil {
				return trial, err
			}
		}
		trial.Status, trial.Revision, trial.UpdatedAt = "invalidated", trial.Revision+1, time.Now().UTC()
		if err := repo.Save(trial, false); err != nil {
			return trial, err
		}
		return trial, nil
	}
	trial.RemainingSegmentIDs = trialRemaining(scope, trial)
	if trial.Status == "running" || trial.Status == "awaiting_enqueue" {
		terminal := true
		for _, sample := range trial.Samples {
			if sample.Result == nil || (sample.Result.Status != RunSucceeded && sample.Result.Status != RunFailed && sample.Result.Status != RunConflict) {
				terminal = false
			}
		}
		if terminal {
			trial.Status, trial.Revision, trial.UpdatedAt = "reviewing", trial.Revision+1, time.Now().UTC()
			if err := repo.Save(trial, false); err != nil {
				return trial, err
			}
		}
	}
	return trial, nil
}

func decideTrial(repo trialRepository, actorID, runID, trialID string, input TrialDecisionInput, policy ModelPolicy) (ScoringTrial, error) {
	trial, err := repo.Get(trialID)
	if err != nil {
		return trial, err
	}
	if trial.RunID != runID {
		return ScoringTrial{}, ErrNotFound
	}
	trial, err = refreshTrial(repo, trial, policy)
	if err != nil {
		return trial, err
	}
	target := map[string]string{"stop": "stopped", "expand": "expanded"}[input.Decision]
	if target == "" || strings.TrimSpace(input.Reason) == "" || len([]rune(input.Reason)) > 500 {
		return trial, ErrInvalidInput
	}
	if trial.Status == target && trial.DecidedBy == actorID && trial.DecisionReason == input.Reason && input.ExpectedRevision+1 == trial.Revision {
		return trial, nil
	}
	if trial.Revision != input.ExpectedRevision || (input.Decision == "expand" && trial.Status != "reviewing") || (input.Decision == "stop" && trial.Status != "awaiting_enqueue" && trial.Status != "running" && trial.Status != "reviewing") {
		return trial, ErrTrialConflict
	}
	if input.Decision == "expand" {
		if len(trial.RemainingSegmentIDs) == 0 {
			return trial, ErrInvalidInput
		}
		// 本期沿用批次的 1000 份上限；超限先拒绝，不能保存部分范围却把整题标成已批准。
		if len(trial.RemainingSegmentIDs) > 1000 {
			return trial, ErrTrialExpansionLimitExceeded
		}
		trial.ExpansionBatchID, err = repo.CreateBatch(actorID, runID, "scoring-trial-expansion-"+trial.ID, trial.RemainingSegmentIDs)
		if err != nil {
			return trial, err
		}
	}
	if input.Decision == "stop" {
		if err := repo.CancelBatch(trial.SampleBatchID); err != nil {
			return trial, err
		}
	}
	trial.Status, trial.DecidedBy, trial.DecisionReason = target, actorID, input.Reason
	trial.Revision++
	trial.UpdatedAt = time.Now().UTC()
	if err := repo.Save(trial, false); err != nil {
		return trial, err
	}
	return trial, nil
}

func prepareTrialEnqueue(repo trialRepository, runID, trialID string, policy ModelPolicy) (ScoringTrial, error) {
	trial, err := repo.Get(trialID)
	if err != nil {
		return trial, err
	}
	if trial.RunID != runID {
		return ScoringTrial{}, ErrNotFound
	}
	trial, err = refreshTrial(repo, trial, policy)
	if err != nil {
		return trial, err
	}
	if trial.Status == "stopped" || trial.Status == "invalidated" {
		return trial, ErrTrialConflict
	}
	if trial.Status == "awaiting_enqueue" {
		trial.Status, trial.Revision, trial.UpdatedAt = "running", trial.Revision+1, time.Now().UTC()
		if err := repo.Save(trial, false); err != nil {
			return trial, err
		}
	}
	return trial, nil
}

func authorizeTrialBatch(repo trialRepository, runID, batchID string, policy ModelPolicy) error {
	trials, err := repo.List(runID)
	if err != nil {
		return err
	}
	if len(trials) == 0 {
		return nil
	} // 老运行尚未建立试改资源时保留既有批次恢复能力。
	for _, trial := range trials {
		if trial.SampleBatchID != batchID && trial.ExpansionBatchID != batchID {
			continue
		}
		trial, err = refreshTrial(repo, trial, policy)
		if err != nil {
			return err
		}
		if trial.Status == "invalidated" || trial.Status == "stopped" {
			return ErrTrialConflict
		}
		if batchID == trial.SampleBatchID || (batchID == trial.ExpansionBatchID && trial.Status == "expanded") {
			return nil
		}
	}
	return ErrTrialConflict
}
