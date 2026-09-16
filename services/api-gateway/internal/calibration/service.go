package calibration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/assessment"
	"edugrade-enterprise/services/api-gateway/internal/goldpaper"

	"github.com/google/uuid"
)

type Service struct {
	store Store
	gold  goldpaper.ActiveApprovedReader
	now   func() time.Time
}

func NewService(store Store, gold goldpaper.ActiveApprovedReader) *Service {
	return &Service{store: store, gold: gold, now: func() time.Time { return time.Now().UTC() }}
}

func (s *Service) PutPolicy(ctx context.Context, tenantID, examID, questionID string, input PutPolicyInput) (Policy, error) {
	if tenantID == "" || examID == "" || questionID == "" || !validPolicyInput(input) {
		return Policy{}, ErrInvalidInput
	}
	return s.store.PutPolicy(ctx, tenantID, examID, questionID, input)
}

func (s *Service) GetPolicy(ctx context.Context, tenantID, examID, questionID string) (Policy, error) {
	return s.store.GetPolicy(ctx, tenantID, examID, questionID)
}

func (s *Service) CreateSession(ctx context.Context, tenantID, examID, questionID, graderID string) (Session, error) {
	if tenantID == "" || examID == "" || questionID == "" || graderID == "" || s.gold == nil {
		return Session{}, ErrInvalidInput
	}
	policy, err := s.store.GetPolicy(ctx, tenantID, examID, questionID)
	if err != nil {
		if err == ErrNotFound {
			return Session{}, ErrPolicyMissing
		}
		return Session{}, err
	}
	references, goldHash, err := s.currentReferences(ctx, tenantID, examID, questionID, policy)
	if err != nil {
		return Session{}, err
	}
	now := s.now()
	session := Session{ID: uuid.NewString(), ExamID: examID, QuestionID: questionID, GraderID: graderID,
		SnapshotID: references[0].SnapshotID, ArchetypeCode: policy.ArchetypeCode, RiskTier: references[0].RiskTier,
		GoldSetHash: goldHash, Status: SessionInProgress,
		Samples: publicSamples(references), StartedAt: now}
	return s.store.CreateSession(ctx, tenantID, session, policy, references)
}

func (s *Service) GetSession(ctx context.Context, tenantID, sessionID string) (Session, error) {
	session, _, _, _, err := s.store.GetSession(ctx, tenantID, sessionID)
	return session, err
}

func (s *Service) SubmitAttempt(ctx context.Context, tenantID, sessionID string, input SubmitAttemptInput) (Attempt, Session, *Qualification, error) {
	if tenantID == "" || sessionID == "" || strings.TrimSpace(input.GoldPaperID) == "" ||
		math.IsNaN(input.SubmittedScore) || math.IsInf(input.SubmittedScore, 0) {
		return Attempt{}, Session{}, nil, ErrInvalidInput
	}
	session, policy, references, attempts, err := s.store.GetSession(ctx, tenantID, sessionID)
	if err != nil {
		return Attempt{}, Session{}, nil, err
	}
	if session.Status != SessionInProgress {
		return Attempt{}, Session{}, nil, ErrConflict
	}
	var reference *goldReference
	for i := range references {
		if references[i].GoldPaperID == input.GoldPaperID {
			reference = &references[i]
			break
		}
	}
	if reference == nil {
		return Attempt{}, Session{}, nil, ErrSampleNotInSession
	}
	for _, previous := range attempts {
		if previous.GoldPaperID == input.GoldPaperID {
			if len(attempts) == len(references) && previous.SubmittedScore == input.SubmittedScore &&
				reflect.DeepEqual(cloneObject(previous.RubricSelections), cloneObject(input.RubricSelections)) {
				return s.finishSession(ctx, tenantID, session, policy, attempts, previous)
			}
			return Attempt{}, Session{}, nil, ErrConflict
		}
	}
	if input.SubmittedScore < 0 || input.SubmittedScore > reference.MaxScore {
		return Attempt{}, Session{}, nil, ErrInvalidInput
	}
	absError := math.Abs(input.SubmittedScore - reference.ReferenceScore)
	correct, criterionCount, differences := compareCriteria(reference.ExpectedCriteria, input.RubricSelections)
	now := s.now()
	attempt := Attempt{ID: uuid.NewString(), SessionID: session.ID, GoldPaperID: reference.GoldPaperID,
		GoldVersion: reference.GoldVersion, SubmittedScore: input.SubmittedScore, ReferenceScore: reference.ReferenceScore,
		RubricSelections: cloneObject(input.RubricSelections),
		AbsoluteError:    absError, ExactMatch: absError < 1e-9, WithinOne: absError <= 1,
		SevereDisagreement: absError >= policy.SevereErrorThreshold, CriterionCorrect: correct,
		CriterionCount: criterionCount, CriterionDifferences: differences, CreatedAt: now}
	attempt, err = s.store.CreateAttempt(ctx, tenantID, sessionID, attempt)
	if err != nil {
		return Attempt{}, Session{}, nil, err
	}
	// Reload after insert so concurrent submissions cannot leave a fully
	// answered session in_progress because each request observed stale counts.
	session, policy, references, attempts, err = s.store.GetSession(ctx, tenantID, sessionID)
	if err != nil {
		return Attempt{}, Session{}, nil, err
	}
	if len(attempts) < len(references) {
		return attempt, session, nil, nil
	}
	return s.finishSession(ctx, tenantID, session, policy, attempts, attempt)
}

func (s *Service) finishSession(ctx context.Context, tenantID string, session Session, policy Policy, attempts []Attempt, attempt Attempt) (Attempt, Session, *Qualification, error) {
	now := s.now()
	metrics := calculateMetrics(attempts)
	passed := passes(policy, metrics)
	session.Status, session.Metrics, session.CompletedAt = SessionFailed, &metrics, &now
	if passed {
		session.Status = SessionPassed
	}
	qualification := Qualification{ID: uuid.NewString(), ExamID: session.ExamID, QuestionID: session.QuestionID,
		GraderID: session.GraderID, Status: QualificationQualified, GoldSetHash: session.GoldSetHash,
		CalibrationSessionID: session.ID, Metrics: metrics, CreatedAt: now, UpdatedAt: now,
		ValidUntil: now.AddDate(0, 0, policy.QualificationValidityDays)}
	if !passed {
		qualification.Status = QualificationRevoked
		qualification.ValidUntil = now
	}
	session, qualification, err := s.store.CompleteSession(ctx, tenantID, session, qualification)
	if err != nil {
		return Attempt{}, Session{}, nil, err
	}
	return attempt, session, &qualification, nil
}

func (s *Service) GetQualification(ctx context.Context, tenantID, examID, questionID, graderID string) (Qualification, error) {
	qualification, err := s.store.GetQualification(ctx, tenantID, examID, questionID, graderID)
	if err != nil {
		return Qualification{}, err
	}
	if qualification.Status != QualificationQualified {
		return qualification, nil
	}
	now := s.now()
	if !qualification.ValidUntil.After(now) {
		return s.store.InvalidateQualification(ctx, tenantID, qualification.ID, "expired")
	}
	policy, err := s.store.GetPolicy(ctx, tenantID, examID, questionID)
	if err != nil {
		return Qualification{}, err
	}
	_, currentHash, err := s.currentReferences(ctx, tenantID, examID, questionID, policy)
	if err != nil || currentHash != qualification.GoldSetHash {
		return s.store.InvalidateQualification(ctx, tenantID, qualification.ID, "gold_set_changed")
	}
	return qualification, nil
}

func (s *Service) RequireQualification(ctx context.Context, tenantID, examID, questionID, graderID string) error {
	qualification, err := s.GetQualification(ctx, tenantID, examID, questionID, graderID)
	if err != nil || qualification.Status != QualificationQualified || !qualification.ValidUntil.After(s.now()) {
		return ErrQualificationRequired
	}
	return nil
}

// SuspendForQualityIncident is the narrow A11 bridge. It invalidates only the
// one grader/question qualification identified by a critical quality incident;
// it never changes the grader's permissions or qualifications on other items.
// A missing qualification is already unable to claim work, so it is harmless.
func (s *Service) SuspendForQualityIncident(ctx context.Context, tenantID, examID, questionID, graderID, _ string) error {
	qualification, err := s.GetQualification(ctx, tenantID, examID, questionID, graderID)
	if err == ErrNotFound {
		return nil
	}
	if err != nil || qualification.Status != QualificationQualified {
		return err
	}
	_, err = s.store.InvalidateQualification(ctx, tenantID, qualification.ID, "quality_incident")
	return err
}

func (s *Service) currentReferences(ctx context.Context, tenantID, examID, questionID string, policy Policy) ([]goldReference, string, error) {
	items, err := s.gold.ListActiveApproved(ctx, tenantID, examID, questionID)
	if err != nil {
		return nil, "", err
	}
	references := make([]goldReference, 0, len(items))
	for _, item := range items {
		if item.ActiveVersion <= 0 || item.ArchetypeCode != policy.ArchetypeCode || !assessment.RiskTier(item.RiskTier).Valid() {
			continue
		}
		for _, version := range item.Versions {
			if version.Version != item.ActiveVersion || version.ApprovedAt == nil || math.Abs(version.MaxScore-policy.MaxScore) > 1e-9 {
				continue
			}
			references = append(references, goldReference{GoldSample: GoldSample{GoldPaperID: item.ID,
				GoldVersion: version.Version, SubmissionID: item.SubmissionID, AnswerImageURL: item.AnswerImageURL,
				MaxScore: version.MaxScore, RubricSnapshot: cloneObject(version.RubricSnapshot)},
				SnapshotID: version.ExamQuestionSnapshotID, RiskTier: item.RiskTier, ReferenceScore: version.ReferenceScore,
				ExpectedCriteria: cloneObject(version.TraitScores)})
		}
	}
	if len(references) < policy.MinimumSamples {
		return nil, "", ErrGoldSetIncomplete
	}
	sort.Slice(references, func(i, j int) bool { return references[i].GoldPaperID < references[j].GoldPaperID })
	digest := sha256.New()
	for _, ref := range references {
		digest.Write([]byte(ref.GoldPaperID))
		digest.Write([]byte{0})
		digest.Write([]byte(strconv.Itoa(ref.GoldVersion)))
		digest.Write([]byte{0})
		digest.Write([]byte(ref.SnapshotID))
		digest.Write([]byte{0})
	}
	return references, hex.EncodeToString(digest.Sum(nil)), nil
}

func validPolicyInput(input PutPolicyInput) bool {
	validRate := func(value float64) bool {
		return !math.IsNaN(value) && !math.IsInf(value, 0) && value >= 0 && value <= 1
	}
	return assessment.IsQuestionArchetype(strings.TrimSpace(input.ArchetypeCode)) && input.MaxScore > 0 && input.MinimumSamples > 0 &&
		input.MaximumMAE >= 0 && input.SevereErrorThreshold > 0 && input.SevereErrorThreshold <= input.MaxScore &&
		validRate(input.MinimumExactAgreement) && validRate(input.MinimumWithinOneAgreement) &&
		(input.MinimumCriterionAgreement == nil || validRate(*input.MinimumCriterionAgreement)) &&
		validRate(input.MaximumSevereRate) && input.QualificationValidityDays > 0 && input.QualificationValidityDays <= 3650 &&
		input.ExpectedRevision >= 0
}

func publicSamples(refs []goldReference) []GoldSample {
	out := make([]GoldSample, len(refs))
	for i := range refs {
		out[i] = refs[i].GoldSample
		out[i].RubricSnapshot = cloneObject(refs[i].RubricSnapshot)
	}
	return out
}

func calculateMetrics(attempts []Attempt) Metrics {
	metrics := Metrics{SampleCount: len(attempts)}
	var exact, withinOne, severe, criterionCorrect, criterionCount int
	for _, attempt := range attempts {
		metrics.MAE += attempt.AbsoluteError
		if attempt.ExactMatch {
			exact++
		}
		if attempt.WithinOne {
			withinOne++
		}
		if attempt.SevereDisagreement {
			severe++
		}
		criterionCorrect += attempt.CriterionCorrect
		criterionCount += attempt.CriterionCount
	}
	if len(attempts) > 0 {
		count := float64(len(attempts))
		metrics.MAE /= count
		metrics.ExactAgreement = float64(exact) / count
		metrics.WithinOneAgreement = float64(withinOne) / count
		metrics.SevereDisagreementRate = float64(severe) / count
	}
	metrics.CriterionSampleCount = criterionCount
	if criterionCount > 0 {
		value := float64(criterionCorrect) / float64(criterionCount)
		metrics.CriterionAgreement = &value
	}
	return metrics
}

func passes(policy Policy, metrics Metrics) bool {
	if metrics.SampleCount < policy.MinimumSamples || metrics.MAE > policy.MaximumMAE ||
		metrics.ExactAgreement < policy.MinimumExactAgreement || metrics.WithinOneAgreement < policy.MinimumWithinOneAgreement ||
		metrics.SevereDisagreementRate > policy.MaximumSevereRate {
		return false
	}
	if policy.MinimumCriterionAgreement != nil {
		return metrics.CriterionAgreement != nil && *metrics.CriterionAgreement >= *policy.MinimumCriterionAgreement
	}
	return true
}

func compareCriteria(expected, submitted map[string]any) (int, int, []CriterionDifference) {
	expectedFlat, submittedFlat := map[string]string{}, map[string]string{}
	flatten("", expected, expectedFlat)
	flatten("", submitted, submittedFlat)
	keys := make([]string, 0, len(expectedFlat))
	for key := range expectedFlat {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	correct, differences := 0, []CriterionDifference{}
	for _, key := range keys {
		actual, ok := submittedFlat[key]
		if ok && actual == expectedFlat[key] {
			correct++
			continue
		}
		differences = append(differences, CriterionDifference{Criterion: key, Expected: decodeScalar(expectedFlat[key]), Submitted: decodeScalar(actual)})
	}
	return correct, len(keys), differences
}

func flatten(prefix string, value any, out map[string]string) {
	switch current := value.(type) {
	case map[string]any:
		keys := make([]string, 0, len(current))
		for key := range current {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			next := key
			if prefix != "" {
				next = prefix + "." + key
			}
			flatten(next, current[key], out)
		}
	case []any:
		for index, item := range current {
			flatten(prefix+"["+strconv.Itoa(index)+"]", item, out)
		}
	default:
		encoded, _ := json.Marshal(current)
		out[prefix] = string(encoded)
	}
}

func decodeScalar(encoded string) any {
	if encoded == "" {
		return nil
	}
	var value any
	if json.Unmarshal([]byte(encoded), &value) == nil {
		return value
	}
	return encoded
}

func cloneObject(value map[string]any) map[string]any {
	if value == nil {
		return map[string]any{}
	}
	encoded, _ := json.Marshal(value)
	var out map[string]any
	_ = json.Unmarshal(encoded, &out)
	return out
}
