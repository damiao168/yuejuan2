package imagequality

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"sync"
	"time"
)

type MemoryStore struct {
	mu   sync.RWMutex
	next int
	runs map[string]Run
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		next: 1,
		runs: map[string]Run{},
	}
}

func (s *MemoryStore) CreateRuns(_ context.Context, tenantID string, input CreateRunsInput) ([]Run, error) {
	input.Profile = normalizeProfile(input.Profile)
	if err := validateCreateRunsInput(input); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Run, 0, len(input.Pages))
	now := time.Now().UTC()
	for _, page := range input.Pages {
		run := Run{
			ID:                     s.id("quality-run"),
			TenantID:               tenantID,
			SubmissionID:           input.SubmissionID,
			SubmissionPageID:       page.SubmissionPageID,
			PageNo:                 page.PageNo,
			SourceFileAssetID:      page.SourceFileAssetID,
			SourceSHA256:           page.SourceSHA256,
			DownloadURL:            page.DownloadURL,
			ProcessingStatus:       ProcessingPending,
			ProfileName:            input.Profile.Name,
			ProfileVersion:         input.Profile.Version,
			ProfileConfigHash:      input.Profile.ConfigHash,
			MetricSchemaVersion:    input.Profile.MetricSchemaVersion,
			ReportSchemaVersion:    input.Profile.ReportSchemaVersion,
			QualityReport:          map[string]any{},
			QualityIssues:          []Issue{},
			NormalizationTransform: map[string]any{},
			ErrorDetail:            map[string]any{},
			CreatedAt:              now,
		}
		s.runs[run.ID] = run
		out = append(out, run)
	}
	return out, nil
}

func (s *MemoryStore) ListRunsForPage(_ context.Context, tenantID string, submissionPageID string) ([]Run, error) {
	if tenantID == "" || submissionPageID == "" {
		return nil, ErrInvalidInput
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	runs := make([]Run, 0)
	for _, run := range s.runs {
		if run.TenantID == tenantID && run.SubmissionPageID == submissionPageID {
			runs = append(runs, cloneRun(run))
		}
	}
	sort.Slice(runs, func(i, j int) bool {
		if runs[i].CreatedAt.Equal(runs[j].CreatedAt) {
			return runs[i].ID > runs[j].ID
		}
		return runs[i].CreatedAt.After(runs[j].CreatedAt)
	})
	return runs, nil
}

// 内存领取实现模拟租约过期可回收和尝试次数递增，保证测试覆盖生产重试语义。
func (s *MemoryStore) Claim(_ context.Context, tenantID string, input ClaimInput) ([]ClaimedJob, error) {
	input = normalizeClaimInput(input)
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	candidates := make([]Run, 0, len(s.runs))
	for _, run := range s.runs {
		if run.TenantID != tenantID {
			continue
		}
		if run.ProcessingStatus == ProcessingPending || (run.ProcessingStatus == ProcessingProcessing && run.LeaseExpiresAt != nil && run.LeaseExpiresAt.Before(now)) {
			candidates = append(candidates, run)
		}
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].CreatedAt.Before(candidates[j].CreatedAt) })
	if len(candidates) > input.Limit {
		candidates = candidates[:input.Limit]
	}
	jobs := make([]ClaimedJob, 0, len(candidates))
	for _, run := range candidates {
		token := newLeaseToken()
		expiresAt := now.Add(time.Duration(input.LeaseSeconds) * time.Second)
		startedAt := now
		run.ProcessingStatus = ProcessingProcessing
		run.WorkerService = "image-quality-worker"
		run.WorkerInstanceID = input.WorkerInstanceID
		run.AttemptNo++
		run.LeaseToken = token
		run.LeaseExpiresAt = &expiresAt
		run.StartedAt = &startedAt
		s.runs[run.ID] = run
		jobs = append(jobs, ClaimedJob{
			RunID:             run.ID,
			SubmissionID:      run.SubmissionID,
			SubmissionPageID:  run.SubmissionPageID,
			PageNo:            run.PageNo,
			SourceFileAssetID: run.SourceFileAssetID,
			SourceSHA256:      run.SourceSHA256,
			DownloadURL:       run.DownloadURL,
			LeaseToken:        token,
			LeaseExpiresAt:    expiresAt,
			AttemptNo:         run.AttemptNo,
			Profile: Profile{
				Name:                run.ProfileName,
				Version:             run.ProfileVersion,
				ConfigHash:          run.ProfileConfigHash,
				MetricSchemaVersion: run.MetricSchemaVersion,
				ReportSchemaVersion: run.ReportSchemaVersion,
			},
		})
	}
	return jobs, nil
}

func (s *MemoryStore) LeaseRun(_ context.Context, tenantID string, runID string, workerInstanceID string, leaseToken string, leaseExpiresAt time.Time, attemptNo int) (Run, error) {
	if leaseToken == "" || attemptNo <= 0 || !leaseExpiresAt.After(time.Now().UTC()) {
		return Run{}, ErrInvalidInput
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	run, ok := s.runs[runID]
	if !ok || run.TenantID != tenantID {
		return Run{}, ErrNotFound
	}
	if run.ProcessingStatus != ProcessingPending && run.ProcessingStatus != ProcessingProcessing && run.ProcessingStatus != ProcessingRetryableError {
		return Run{}, ErrInvalidTransition
	}
	now := time.Now().UTC()
	run.ProcessingStatus = ProcessingProcessing
	run.WorkerService = "image-quality-worker"
	run.WorkerInstanceID = workerInstanceID
	run.LeaseToken = leaseToken
	run.LeaseExpiresAt = &leaseExpiresAt
	run.AttemptNo = attemptNo
	run.StartedAt = &now
	s.runs[runID] = run
	return cloneRun(run), nil
}

func (s *MemoryStore) RenewLease(_ context.Context, tenantID string, runID string, workerInstanceID string, leaseToken string, leaseExpiresAt time.Time, attemptNo int) (Run, error) {
	if workerInstanceID == "" || leaseToken == "" || attemptNo <= 0 || !leaseExpiresAt.After(time.Now().UTC()) {
		return Run{}, ErrInvalidInput
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	run, ok := s.runs[runID]
	if !ok || run.TenantID != tenantID {
		return Run{}, ErrNotFound
	}
	if run.ProcessingStatus != ProcessingProcessing {
		return Run{}, ErrInvalidTransition
	}
	if run.WorkerInstanceID != workerInstanceID || run.LeaseToken != leaseToken || run.AttemptNo != attemptNo {
		return Run{}, ErrLeaseMismatch
	}
	run.LeaseExpiresAt = &leaseExpiresAt
	s.runs[runID] = run
	return cloneRun(run), nil
}

func (s *MemoryStore) CompleteRun(_ context.Context, tenantID string, runID string, input ResultInput) (Run, error) {
	if err := validateResultInput(input); err != nil {
		return Run{}, err
	}
	payloadHash := resultPayloadHash(input)
	s.mu.Lock()
	defer s.mu.Unlock()
	run, ok := s.runs[runID]
	if !ok || run.TenantID != tenantID {
		return Run{}, ErrNotFound
	}
	if run.ProcessingStatus == ProcessingCompleted && run.AttemptNo == input.AttemptNo && run.ResultVersion == input.ResultVersion {
		if run.LeaseToken != input.LeaseToken {
			return Run{}, ErrLeaseMismatch
		}
		if run.ResultPayloadHash == payloadHash {
			return cloneRun(run), nil
		}
		return Run{}, ErrConflict
	}
	if run.ProcessingStatus != ProcessingProcessing {
		return Run{}, ErrInvalidTransition
	}
	if run.LeaseToken != input.LeaseToken || run.AttemptNo != input.AttemptNo {
		return Run{}, ErrLeaseMismatch
	}
	if run.LeaseExpiresAt == nil || run.LeaseExpiresAt.Before(time.Now().UTC()) {
		return Run{}, ErrLeaseExpired
	}
	completedAt := time.Now().UTC()
	run.ProcessingStatus = input.ProcessingStatus
	run.QualityStatus = input.QualityStatus
	run.NormalizedFileAssetID = input.NormalizedFileAssetID
	run.ResultVersion = input.ResultVersion
	run.ResultPayloadHash = payloadHash
	run.DurationMS = input.DurationMS
	run.QualityReport = cloneMap(input.QualityReport)
	run.QualityIssues = append([]Issue{}, input.QualityIssues...)
	run.NormalizationTransform = cloneMap(input.NormalizationTransform)
	run.ErrorCode = input.ErrorCode
	run.ErrorDetail = cloneMap(input.ErrorDetail)
	run.CompletedAt = &completedAt
	s.runs[runID] = run
	return cloneRun(run), nil
}

func (s *MemoryStore) GetRun(_ context.Context, tenantID string, runID string) (Run, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	run, ok := s.runs[runID]
	if !ok || run.TenantID != tenantID {
		return Run{}, ErrNotFound
	}
	return cloneRun(run), nil
}

func (s *MemoryStore) ForceExpireLeaseForTest(runID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	run, ok := s.runs[runID]
	if !ok {
		return
	}
	expired := time.Now().UTC().Add(-time.Second)
	run.LeaseExpiresAt = &expired
	s.runs[runID] = run
}

func (s *MemoryStore) id(prefix string) string {
	id := fmt.Sprintf("%s-%d", prefix, s.next)
	s.next++
	return id
}

func newLeaseToken() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("lease-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
}

func resultPayloadHash(input ResultInput) string {
	type comparableInput ResultInput
	raw, _ := json.Marshal(comparableInput(input))
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func cloneRun(run Run) Run {
	run.QualityReport = cloneMap(run.QualityReport)
	run.QualityIssues = append([]Issue{}, run.QualityIssues...)
	run.NormalizationTransform = cloneMap(run.NormalizationTransform)
	run.ErrorDetail = cloneMap(run.ErrorDetail)
	return run
}

func cloneMap(in map[string]any) map[string]any {
	if len(in) == 0 {
		return map[string]any{}
	}
	raw, err := json.Marshal(in)
	if err != nil {
		out := make(map[string]any, len(in))
		for k, v := range in {
			out[k] = v
		}
		return out
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return map[string]any{}
	}
	return out
}
