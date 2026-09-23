package scorerelease

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"sync"
	"time"
)

// MemoryStore is a deterministic release boundary for unit/handler tests. It
// keeps source facts separate from materialised release records so a test can
// emulate a regrade without altering an already published version.
type MemoryStore struct {
	mu                    sync.Mutex
	now                   func() time.Time
	sequence              int
	facts                 map[string][]SubmissionFact
	gates                 map[string][]GateIssue
	releases              map[string]Release
	items                 map[string][]ReleaseItem
	questions             map[string][]ReleaseQuestion
	studentQuestionImages map[string]string
	current               map[string]string
	idempotency           map[string]string
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		now: func() time.Time { return time.Now().UTC() }, facts: map[string][]SubmissionFact{}, gates: map[string][]GateIssue{},
		releases: map[string]Release{}, items: map[string][]ReleaseItem{}, questions: map[string][]ReleaseQuestion{}, studentQuestionImages: map[string]string{},
		current: map[string]string{}, idempotency: map[string]string{},
	}
}

func (s *MemoryStore) SeedStudentQuestionImage(tenantID, examID, studentID, questionID, answerSegmentID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.studentQuestionImages[studentQuestionImageKey(tenantID, examID, studentID, questionID)] = answerSegmentID
}

func (s *MemoryStore) SetNow(now func() time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.now = now
}

func (s *MemoryStore) SeedFacts(examID string, facts []SubmissionFact) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.facts[examID] = cloneFacts(facts)
}

func (s *MemoryStore) SetGateIssues(examID string, issues []GateIssue) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.gates[examID] = append([]GateIssue(nil), issues...)
}

func (s *MemoryStore) Create(_ context.Context, tenantID, examID, actorID string, input CreateInput) (Release, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := tenantID + ":" + examID + ":" + input.IdempotencyKey
	if id := s.idempotency[key]; id != "" {
		return cloneRelease(s.releases[id]), nil
	}
	facts := cloneFacts(s.facts[examID])
	if len(facts) == 0 {
		return Release{}, ErrNotFound
	}
	release := s.newReleaseLocked(tenantID, examID, actorID, input.Source, input.Reason, input.IdempotencyKey, input.VisibilityPolicy, input.AppealWindow)
	s.saveSnapshotLocked(release.ID, facts)
	s.releases[release.ID] = release
	s.idempotency[key] = release.ID
	return cloneRelease(release), nil
}

func (s *MemoryStore) CreateRollback(_ context.Context, tenantID, examID, actorID string, input RollbackInput) (Release, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := tenantID + ":" + examID + ":" + input.IdempotencyKey
	if id := s.idempotency[key]; id != "" {
		return cloneRelease(s.releases[id]), nil
	}
	source, ok := s.releases[input.SourceReleaseID]
	if !ok || source.TenantID != tenantID || source.ExamID != examID || source.Status != StatusPublished {
		return Release{}, ErrNotFound
	}
	source.VisibilityPolicy.ShowHighScorePaper = false
	release := s.newReleaseLocked(tenantID, examID, actorID, SourceRollback, input.Reason, input.IdempotencyKey, source.VisibilityPolicy, source.AppealWindow)
	release.SourceReleaseID = source.ID
	s.cloneSnapshotLocked(source.ID, release.ID)
	s.releases[release.ID] = release
	s.idempotency[key] = release.ID
	return cloneRelease(release), nil
}

func (s *MemoryStore) CreateFromRegrade(_ context.Context, tenantID, examID, actorID string, input CreateRegradeInput) (Release, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := tenantID + ":" + examID + ":" + input.IdempotencyKey
	if id := s.idempotency[key]; id != "" {
		return cloneRelease(s.releases[id]), nil
	}
	source, ok := s.releases[input.SourceReleaseID]
	if !ok || source.TenantID != tenantID || source.ExamID != examID || source.Status != StatusPublished {
		return Release{}, ErrNotFound
	}
	source.VisibilityPolicy.ShowHighScorePaper = false
	facts := s.snapshotFactsLocked(source.ID)
	changes := map[string]RegradeChange{}
	for _, change := range input.Changes {
		changes[change.SubmissionID+"\x00"+change.QuestionID] = change
	}
	matched := 0
	for factIndex := range facts {
		fact := &facts[factIndex]
		for questionIndex := range fact.Questions {
			question := &fact.Questions[questionIndex]
			change, found := changes[fact.SubmissionID+"\x00"+question.QuestionID]
			if !found {
				continue
			}
			if question.QuestionID != input.QuestionID || question.MaxScore != change.MaxScore {
				return Release{}, ErrInvalidInput
			}
			question.Score, question.SourceType, question.SourceID = change.Score, "single_review", change.ReviewedGradeID
			question.Explanation.Feedback = ""
			question.Explanation.RubricSummary = nil
			matched++
		}
		if factIndex < len(facts) {
			fact.TotalScore = sumQuestionScores(fact.Questions)
		}
	}
	if matched != len(changes) {
		return Release{}, ErrInvalidInput
	}
	release := s.newReleaseLocked(tenantID, examID, actorID, SourceRegrade, input.Reason, input.IdempotencyKey, source.VisibilityPolicy, source.AppealWindow)
	release.SourceReleaseID = source.ID
	s.saveSnapshotLocked(release.ID, facts)
	s.releases[release.ID] = release
	s.idempotency[key] = release.ID
	return cloneRelease(release), nil
}

func (s *MemoryStore) List(_ context.Context, tenantID, examID string) ([]Release, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []Release{}
	for _, release := range s.releases {
		if release.TenantID == tenantID && release.ExamID == examID {
			out = append(out, cloneRelease(release))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Version > out[j].Version })
	return out, nil
}

func (s *MemoryStore) Get(_ context.Context, tenantID, id string) (Detail, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.detailLocked(tenantID, id)
}

func (s *MemoryStore) Diff(_ context.Context, tenantID, id, baseID string) (Diff, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.detailLocked(tenantID, id); err != nil {
		return Diff{}, err
	}
	if _, err := s.detailLocked(tenantID, baseID); err != nil {
		return Diff{}, err
	}
	return s.diffLocked(id, baseID), nil
}

func (s *MemoryStore) Gate(_ context.Context, tenantID, examID string) (Gate, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.gateLocked(tenantID, examID, ""), nil
}

func (s *MemoryStore) Publish(_ context.Context, tenantID, id, actorID string) (Release, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	release, ok := s.releases[id]
	if !ok || release.TenantID != tenantID {
		return Release{}, ErrNotFound
	}
	if release.Status == StatusPublished {
		return cloneRelease(release), nil
	}
	if release.Status != StatusDraft {
		return Release{}, ErrInvalidTransition
	}
	gate := s.gateLocked(tenantID, release.ExamID, id)
	if !gate.Passed {
		return Release{}, ErrGateBlocked
	}
	if release.Source == SourceRegrade && release.SourceReleaseID != "" && release.SourceReleaseID != s.current[tenantID+":"+release.ExamID] {
		return Release{}, ErrStaleSource
	}
	now := s.now().UTC()
	release.Status, release.PublishedBy, release.PublishedAt, release.GateSnapshot = StatusPublished, actorID, &now, gate
	release.SupersedesReleaseID = s.current[tenantID+":"+release.ExamID]
	s.releases[id], s.current[tenantID+":"+release.ExamID] = release, id
	return cloneRelease(release), nil
}

func (s *MemoryStore) CurrentPublished(_ context.Context, tenantID, examID string) (Detail, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id := s.current[tenantID+":"+examID]
	if id == "" {
		return Detail{}, ErrNotFound
	}
	return s.detailLocked(tenantID, id)
}

func (s *MemoryStore) StudentResult(_ context.Context, tenantID, examID, studentID string) (StudentResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.studentResultLocked(tenantID, examID, studentID)
}

func (s *MemoryStore) studentResultLocked(tenantID, examID, studentID string) (StudentResult, error) {
	id := s.current[tenantID+":"+examID]
	if id == "" {
		return StudentResult{}, ErrNotFound
	}
	release := s.releases[id]
	for _, item := range s.items[id] {
		if item.StudentID != studentID {
			continue
		}
		result := StudentResult{ExamID: examID, ReleaseID: id, ReleaseVersion: release.Version, TotalScore: item.TotalScore, MaxScore: item.MaxScore, OverallTotalScore: item.TotalScore, OverallMaxScore: item.MaxScore, ScoreRate: scoreRate(item.TotalScore, item.MaxScore),
			Questions: []StudentQuestion{}, AppealWindow: appealView(release.AppealWindow, s.now().UTC())}
		result.Reference = buildStudentReference(s.items[id], item.TotalScore, release.VisibilityPolicy)
		if release.VisibilityPolicy.ShowExactRank && result.Reference != nil && result.Reference.StatisticsAvailable && result.Reference.Rank != nil {
			result.Rankings = &StudentRankings{ClassRank: *result.Reference.Rank, ClassSize: result.Reference.SampleSize, GradeRank: *result.Reference.Rank, GradeSize: result.Reference.SampleSize}
		}
		for _, question := range s.questions[id] {
			if question.SubmissionID != item.SubmissionID {
				continue
			}
			studentQuestion := studentQuestionView(question, s.questions[id], release.VisibilityPolicy)
			if release.VisibilityPolicy.ShowFeedback {
				studentQuestion.Feedback = question.Explanation.Feedback
			}
			if release.VisibilityPolicy.ShowRubricSummary {
				studentQuestion.RubricSummary = append([]string(nil), question.Explanation.RubricSummary...)
			}
			result.Questions = append(result.Questions, studentQuestion)
		}
		if !release.VisibilityPolicy.ShowQuestionScores {
			result.Questions = nil
		}
		sort.Slice(result.Questions, func(i, j int) bool { return result.Questions[i].QuestionNo < result.Questions[j].QuestionNo })
		return result, nil
	}
	return StudentResult{}, ErrNotFound
}

func (s *MemoryStore) StudentQuestion(ctx context.Context, tenantID, examID, studentID, questionID string) (StudentQuestion, error) {
	_ = ctx
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.StudentQuestionLocked(tenantID, examID, studentID, questionID)
}

func (s *MemoryStore) StudentQuestionImage(ctx context.Context, tenantID, examID, studentID, questionID string) (StudentQuestionImageSource, error) {
	_ = ctx
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.StudentQuestionLocked(tenantID, examID, studentID, questionID); err != nil {
		return StudentQuestionImageSource{}, err
	}
	answerSegmentID := s.studentQuestionImages[studentQuestionImageKey(tenantID, examID, studentID, questionID)]
	if answerSegmentID == "" {
		return StudentQuestionImageSource{}, ErrNotFound
	}
	return StudentQuestionImageSource{AnswerSegmentID: answerSegmentID}, nil
}

func (s *MemoryStore) StudentPaperPageImage(ctx context.Context, tenantID, examID, studentID, questionID string, highScore bool) (StudentQuestionImageSource, error) {
	if highScore {
		return StudentQuestionImageSource{}, ErrNotFound
	}
	return s.StudentQuestionImage(ctx, tenantID, examID, studentID, questionID)
}

func (s *MemoryStore) StudentQuestionLocked(tenantID, examID, studentID, questionID string) (StudentQuestion, error) {
	result, err := s.studentResultLocked(tenantID, examID, studentID)
	if err != nil {
		return StudentQuestion{}, err
	}
	if !s.releases[result.ReleaseID].VisibilityPolicy.ShowQuestionScores {
		return StudentQuestion{}, ErrForbidden
	}
	for _, question := range result.Questions {
		if question.QuestionID == questionID {
			return question, nil
		}
	}
	return StudentQuestion{}, ErrNotFound
}

func studentQuestionImageKey(tenantID, examID, studentID, questionID string) string {
	return tenantID + "\x00" + examID + "\x00" + studentID + "\x00" + questionID
}

func (s *MemoryStore) newReleaseLocked(tenantID, examID, actorID, source, reason, idempotencyKey string, visibility VisibilityPolicy, window AppealWindow) Release {
	version := 1
	for _, release := range s.releases {
		if release.TenantID == tenantID && release.ExamID == examID && release.Version >= version {
			version = release.Version + 1
		}
	}
	return Release{ID: s.idLocked("release"), TenantID: tenantID, ExamID: examID, Version: version, Source: source, Reason: reason,
		Status: StatusDraft, IdempotencyKey: idempotencyKey, VisibilityPolicy: cloneVisibility(visibility), AppealWindow: cloneWindow(window),
		CreatedBy: actorID, CreatedAt: s.now().UTC(), GateSnapshot: emptyGate(s.now().UTC())}
}

func (s *MemoryStore) saveSnapshotLocked(releaseID string, facts []SubmissionFact) {
	items, questions := make([]ReleaseItem, 0, len(facts)), []ReleaseQuestion{}
	for _, fact := range facts {
		copy := cloneFact(fact)
		hash := snapshotHash(copy)
		items = append(items, ReleaseItem{ReleaseID: releaseID, StudentID: copy.StudentID, SubmissionID: copy.SubmissionID, TotalScore: copy.TotalScore, MaxScore: copy.MaxScore, Status: copy.Status, SnapshotHash: hash})
		for _, question := range copy.Questions {
			questions = append(questions, ReleaseQuestion{ReleaseID: releaseID, SubmissionID: copy.SubmissionID, QuestionFact: question})
		}
	}
	s.items[releaseID], s.questions[releaseID] = items, questions
}

func (s *MemoryStore) cloneSnapshotLocked(sourceID, targetID string) {
	items := append([]ReleaseItem(nil), s.items[sourceID]...)
	for index := range items {
		items[index].ReleaseID = targetID
	}
	questions := append([]ReleaseQuestion(nil), s.questions[sourceID]...)
	for index := range questions {
		questions[index].ReleaseID = targetID
		questions[index].Explanation.RubricSummary = append([]string(nil), questions[index].Explanation.RubricSummary...)
	}
	s.items[targetID], s.questions[targetID] = items, questions
}

func (s *MemoryStore) gateLocked(_ string, examID, releaseID string) Gate {
	now := s.now().UTC()
	gate := emptyGate(now)
	for _, issue := range s.gates[examID] {
		addIssue(&gate, issue)
	}
	facts := s.facts[examID]
	if releaseID != "" {
		facts = s.snapshotFactsLocked(releaseID)
	}
	if len(facts) == 0 {
		addIssue(&gate, GateIssue{Code: "no_submission_grades", Message: "there are no confirmed grades to release", Blocking: true, Count: 1, ActionRoute: "grades"})
	}
	for _, fact := range facts {
		if fact.Status != "confirmed" && fact.Status != "published" && fact.Status != "locked" {
			addIssue(&gate, GateIssue{Code: "grades_not_confirmed", Message: "a submission grade is not confirmed", Blocking: true, Count: 1, ActionRoute: "grades"})
		}
		if len(fact.Questions) == 0 {
			addIssue(&gate, GateIssue{Code: "release_snapshot_incomplete", Message: "a submission has no question facts", Blocking: true, Count: 1, ActionRoute: "grades"})
			continue
		}
		var total float64
		for _, question := range fact.Questions {
			if question.FinalGradeID == "" || question.QuestionID == "" || question.MaxScore < 0 || question.Score < 0 || question.Score > question.MaxScore {
				addIssue(&gate, GateIssue{Code: "release_snapshot_incomplete", Message: "a question snapshot is incomplete", Blocking: true, Count: 1, ActionRoute: "grades"})
			}
			total += question.Score
		}
		if math.Abs(total-fact.TotalScore) > 0.000001 {
			addIssue(&gate, GateIssue{Code: "score_integrity_mismatch", Message: "submission total does not match released question scores", Blocking: true, Count: 1, ActionRoute: "grades"})
		}
	}
	gate.Passed = len(gate.Blocking) == 0
	return gate
}

func (s *MemoryStore) snapshotFactsLocked(releaseID string) []SubmissionFact {
	bySubmission := map[string]SubmissionFact{}
	for _, item := range s.items[releaseID] {
		bySubmission[item.SubmissionID] = SubmissionFact{StudentID: item.StudentID, SubmissionID: item.SubmissionID, TotalScore: item.TotalScore, MaxScore: item.MaxScore, Status: item.Status, Questions: []QuestionFact{}}
	}
	for _, question := range s.questions[releaseID] {
		fact := bySubmission[question.SubmissionID]
		fact.Questions = append(fact.Questions, question.QuestionFact)
		bySubmission[question.SubmissionID] = fact
	}
	out := make([]SubmissionFact, 0, len(bySubmission))
	for _, item := range bySubmission {
		out = append(out, item)
	}
	return out
}

func (s *MemoryStore) detailLocked(tenantID, id string) (Detail, error) {
	release, ok := s.releases[id]
	if !ok || release.TenantID != tenantID {
		return Detail{}, ErrNotFound
	}
	detail := Detail{Release: cloneRelease(release), Items: append([]ReleaseItem(nil), s.items[id]...), Questions: append([]ReleaseQuestion(nil), s.questions[id]...)}
	for index := range detail.Questions {
		detail.Questions[index].Explanation.RubricSummary = append([]string(nil), detail.Questions[index].Explanation.RubricSummary...)
	}
	sort.Slice(detail.Items, func(i, j int) bool { return detail.Items[i].SubmissionID < detail.Items[j].SubmissionID })
	sort.Slice(detail.Questions, func(i, j int) bool {
		if detail.Questions[i].SubmissionID == detail.Questions[j].SubmissionID {
			return detail.Questions[i].QuestionNo < detail.Questions[j].QuestionNo
		}
		return detail.Questions[i].SubmissionID < detail.Questions[j].SubmissionID
	})
	return detail, nil
}

func (s *MemoryStore) diffLocked(id, baseID string) Diff {
	result := Diff{BaseReleaseID: baseID, ReleaseID: id, Items: []DiffItem{}, Questions: []DiffQuestion{}}
	baseItems, currentItems := indexItems(s.items[baseID]), indexItems(s.items[id])
	for submissionID, current := range currentItems {
		base, ok := baseItems[submissionID]
		if !ok || math.Abs(base.TotalScore-current.TotalScore) > 0.000001 {
			result.Items = append(result.Items, DiffItem{SubmissionID: submissionID, OldTotal: base.TotalScore, NewTotal: current.TotalScore})
		}
	}
	baseQuestions, currentQuestions := indexQuestions(s.questions[baseID]), indexQuestions(s.questions[id])
	for key, current := range currentQuestions {
		base, ok := baseQuestions[key]
		if !ok || math.Abs(base.Score-current.Score) > 0.000001 {
			result.Questions = append(result.Questions, DiffQuestion{SubmissionID: current.SubmissionID, QuestionID: current.QuestionID, OldScore: base.Score, NewScore: current.Score})
		}
	}
	sort.Slice(result.Items, func(i, j int) bool { return result.Items[i].SubmissionID < result.Items[j].SubmissionID })
	sort.Slice(result.Questions, func(i, j int) bool {
		if result.Questions[i].SubmissionID == result.Questions[j].SubmissionID {
			return result.Questions[i].QuestionID < result.Questions[j].QuestionID
		}
		return result.Questions[i].SubmissionID < result.Questions[j].SubmissionID
	})
	result.AffectedCount = len(result.Items)
	return result
}

func emptyGate(now time.Time) Gate {
	return Gate{Version: "A20.v1", Blocking: []GateIssue{}, Warnings: []GateIssue{}, Counts: map[string]int{}, GeneratedAt: now}
}
func addIssue(gate *Gate, issue GateIssue) {
	if issue.Count <= 0 {
		issue.Count = 1
	}
	gate.Counts[issue.Code] += issue.Count
	if issue.Blocking {
		gate.Blocking = append(gate.Blocking, issue)
	} else {
		gate.Warnings = append(gate.Warnings, issue)
	}
}
func (s *MemoryStore) idLocked(prefix string) string {
	s.sequence++
	return fmt.Sprintf("%s-%d", prefix, s.sequence)
}
func cloneFacts(items []SubmissionFact) []SubmissionFact {
	out := make([]SubmissionFact, 0, len(items))
	for _, item := range items {
		out = append(out, cloneFact(item))
	}
	return out
}
func cloneFact(value SubmissionFact) SubmissionFact {
	value.Questions = append([]QuestionFact(nil), value.Questions...)
	for index := range value.Questions {
		value.Questions[index].Explanation.RubricSummary = append([]string(nil), value.Questions[index].Explanation.RubricSummary...)
	}
	return value
}
func cloneRelease(value Release) Release {
	value.VisibilityPolicy = cloneVisibility(value.VisibilityPolicy)
	value.AppealWindow = cloneWindow(value.AppealWindow)
	value.GateSnapshot.Blocking = append([]GateIssue(nil), value.GateSnapshot.Blocking...)
	value.GateSnapshot.Warnings = append([]GateIssue(nil), value.GateSnapshot.Warnings...)
	value.GateSnapshot.Counts = map[string]int{}
	for key, count := range value.GateSnapshot.Counts {
		value.GateSnapshot.Counts[key] = count
	}
	value.PublishedAt = cloneTime(value.PublishedAt)
	return value
}
func cloneVisibility(value VisibilityPolicy) VisibilityPolicy { return value }
func cloneWindow(value AppealWindow) AppealWindow {
	value.OpensAt, value.ClosesAt = cloneTime(value.OpensAt), cloneTime(value.ClosesAt)
	value.AllowedReasonCodes = append([]string(nil), value.AllowedReasonCodes...)
	return value
}
func snapshotHash(value SubmissionFact) string {
	sort.Slice(value.Questions, func(i, j int) bool { return value.Questions[i].QuestionID < value.Questions[j].QuestionID })
	bytes, _ := json.Marshal(value)
	sum := sha256.Sum256(bytes)
	return hex.EncodeToString(sum[:])
}

func sumQuestionScores(questions []QuestionFact) float64 {
	var total float64
	for _, question := range questions {
		total += question.Score
	}
	return total
}
func indexItems(items []ReleaseItem) map[string]ReleaseItem {
	out := map[string]ReleaseItem{}
	for _, item := range items {
		out[item.SubmissionID] = item
	}
	return out
}
func indexQuestions(items []ReleaseQuestion) map[string]ReleaseQuestion {
	out := map[string]ReleaseQuestion{}
	for _, item := range items {
		out[item.SubmissionID+":"+item.QuestionID] = item
	}
	return out
}
