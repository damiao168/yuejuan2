package subjective

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"strings"

	"edugrade-enterprise/services/api-gateway/internal/assessment"
	"edugrade-enterprise/services/api-gateway/internal/gradingevaluation"
)

var (
	ErrPanelShadowSampleIncomplete = errors.New("panel shadow sample has no complete A/B scores")
	shadowResponseKeyPattern       = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)
	shadowFingerprintPattern       = regexp.MustCompile(`^[a-f0-9]{64}$`)
)

// PanelShadowSample keeps the answer and human reference in separate fields.
// Only the anonymous identity, human-adjudicated score and server-calculated
// panel scores are written to the evaluation store; the reference is never
// passed to an A/B/C adapter.
type PanelShadowSample struct {
	Context              Context
	ResponseKey          string
	ResponseFingerprint  string
	ReferenceScore       float64
	ReferenceReviewers   int
	ReferenceAdjudicated bool
	PrimaryACostMicros   int64
	PrimaryBCostMicros   int64
	ArbiterCostMicros    int64
}

type PanelShadowEvaluationStore interface {
	GetRun(context.Context, string, string) (gradingevaluation.Run, error)
	ListPanelObservations(context.Context, string, string) ([]gradingevaluation.PanelObservation, error)
	AddPanelObservation(context.Context, string, string, gradingevaluation.PanelObservation) (gradingevaluation.PanelObservation, error)
}

// PanelModelSetReference is the immutable, role-ordered model/prompt identity
// to put in the evaluation run's model_reference. A changed role model or
// prompt must start a new run; a strength rank alone is not quality evidence.
func PanelModelSetReference(agents PanelAgents) string {
	type identity struct {
		Policy        ModelPolicy `json:"policy"`
		ModelConfigID string      `json:"model_config_id"`
		ProviderKey   string      `json:"provider_key"`
		AdapterType   string      `json:"adapter_type"`
		BaseURL       string      `json:"base_url"`
	}
	roles := make([]identity, 0, 3)
	for _, binding := range []PanelAgentBinding{agents.PrimaryA, agents.PrimaryB, agents.Arbiter} {
		roles = append(roles, identity{
			Policy: binding.Policy, ModelConfigID: binding.ModelConfigID,
			ProviderKey: binding.ProviderKey, AdapterType: binding.AdapterType, BaseURL: binding.BaseURL,
		})
	}
	encoded, _ := json.Marshal(roles)
	digest := sha256.Sum256(encoded)
	return "panel:" + hex.EncodeToString(digest[:])
}

type PanelShadowRunner struct {
	panel *PanelOrchestrator
	store PanelShadowEvaluationStore
}

func NewPanelShadowRunner(panel *PanelOrchestrator, store PanelShadowEvaluationStore) (*PanelShadowRunner, error) {
	if panel == nil || panel.approvedOnly || store == nil {
		return nil, ErrPanelConfiguration
	}
	return &PanelShadowRunner{panel: panel, store: store}, nil
}

// RunBatch is an internal, resumable Shadow-only path. It never completes an
// evaluation run, approves a policy, or publishes a student score. Every
// sample is checked before the first model call. On a later operational error,
// previously written observations remain available for an idempotent retry.
func (r *PanelShadowRunner) RunBatch(ctx context.Context, tenantID, actorID, evaluationRunID string, config PanelDecisionConfig, samples []PanelShadowSample) ([]gradingevaluation.PanelObservation, error) {
	if r == nil || r.panel == nil || r.store == nil || strings.TrimSpace(tenantID) == "" || strings.TrimSpace(actorID) == "" ||
		strings.TrimSpace(evaluationRunID) == "" || len(samples) == 0 || strings.TrimSpace(config.PolicyVersion) == "" ||
		ValidatePanelDecisionConfig(config) != nil {
		return nil, ErrInvalidInput
	}
	run, err := r.store.GetRun(ctx, tenantID, evaluationRunID)
	if err != nil {
		return nil, err
	}
	if run.Status != gradingevaluation.RunDraft || run.ModelReference != PanelModelSetReference(r.panel.agents) || run.RubricVersion == "" {
		return nil, ErrPanelConfiguration
	}
	existing, err := r.store.ListPanelObservations(ctx, tenantID, evaluationRunID)
	if err != nil {
		return nil, err
	}
	byKey := make(map[string]gradingevaluation.PanelObservation, len(existing))
	fingerprints := make(map[string]string, len(existing)+len(samples))
	for _, item := range existing {
		byKey[item.ResponseKey] = item
		if prior := fingerprints[item.ResponseFingerprint]; prior != "" && prior != item.ResponseKey {
			return nil, gradingevaluation.ErrConflict
		}
		fingerprints[item.ResponseFingerprint] = item.ResponseKey
	}
	seenKeys := map[string]bool{}
	for _, sample := range samples {
		if !validShadowPanelSample(sample, tenantID, run.RubricVersion) || seenKeys[sample.ResponseKey] ||
			(fingerprints[sample.ResponseFingerprint] != "" && fingerprints[sample.ResponseFingerprint] != sample.ResponseKey) {
			return nil, ErrInvalidInput
		}
		if saved, exists := byKey[sample.ResponseKey]; exists && !shadowReferenceMatches(saved, sample) {
			return nil, gradingevaluation.ErrConflict
		}
		seenKeys[sample.ResponseKey] = true
		fingerprints[sample.ResponseFingerprint] = sample.ResponseKey
	}
	observations := make([]gradingevaluation.PanelObservation, 0, len(samples))
	for _, sample := range samples {
		if saved, exists := byKey[sample.ResponseKey]; exists {
			observations = append(observations, saved)
			continue
		}
		result, gradeErr := r.panel.Grade(ctx, tenantID, actorID, sample.Context, config)
		if gradeErr != nil {
			return observations, fmt.Errorf("shadow sample %s: %w", sample.ResponseKey, gradeErr)
		}
		observation, buildErr := shadowObservation(sample, result)
		if buildErr != nil {
			return observations, fmt.Errorf("shadow sample %s: %w", sample.ResponseKey, buildErr)
		}
		saved, saveErr := r.store.AddPanelObservation(ctx, tenantID, evaluationRunID, observation)
		if saveErr != nil {
			return observations, fmt.Errorf("shadow sample %s: %w", sample.ResponseKey, saveErr)
		}
		observations = append(observations, saved)
	}
	return observations, nil
}

func validShadowPanelSample(sample PanelShadowSample, tenantID, rubricVersion string) bool {
	value := sample.Context
	stage := value.AssessmentSnapshot.EducationStage
	subject := value.AssessmentSnapshot.SubjectCode
	canonical, subjectOK := assessment.NormalizeSubjectCode(value.Subject)
	return shadowResponseKeyPattern.MatchString(sample.ResponseKey) && shadowFingerprintPattern.MatchString(sample.ResponseFingerprint) &&
		stage == assessment.StageSenior && subject.Valid() && subjectOK && canonical == subject && value.GradeLevel == string(stage) &&
		value.AssessmentSnapshot.TenantID == tenantID && value.AssessmentSnapshot.QuestionID == value.Question.ID &&
		strings.TrimSpace(value.AssessmentSnapshot.ArchetypeCode) != "" && strings.TrimSpace(value.SegmentID) != "" &&
		strings.TrimSpace(value.AnswerVersion) != "" && value.Rubric.Version == rubricVersion &&
		value.Question.Score > 0 && value.Rubric.MaxScore == value.Question.Score &&
		!math.IsNaN(sample.ReferenceScore) && !math.IsInf(sample.ReferenceScore, 0) &&
		sample.ReferenceScore >= 0 && sample.ReferenceScore <= value.Question.Score &&
		sample.ReferenceReviewers >= 2 && sample.ReferenceAdjudicated &&
		sample.PrimaryACostMicros > 0 && sample.PrimaryBCostMicros > 0 && sample.ArbiterCostMicros >= 0
}

func shadowReferenceMatches(saved gradingevaluation.PanelObservation, sample PanelShadowSample) bool {
	return saved.ResponseFingerprint == sample.ResponseFingerprint && saved.EducationStage == string(sample.Context.AssessmentSnapshot.EducationStage) &&
		saved.Subject == string(sample.Context.AssessmentSnapshot.SubjectCode) && saved.Archetype == sample.Context.AssessmentSnapshot.ArchetypeCode &&
		saved.ReferenceScore == sample.ReferenceScore && saved.MaxScore == sample.Context.Question.Score &&
		saved.ReferenceReviewers == sample.ReferenceReviewers && saved.ReferenceAdjudicated == sample.ReferenceAdjudicated &&
		saved.PrimaryACostMicros == sample.PrimaryACostMicros && saved.PrimaryBCostMicros == sample.PrimaryBCostMicros &&
		saved.ArbiterCostMicros == sample.ArbiterCostMicros
}

func shadowObservation(sample PanelShadowSample, result PanelResult) (gradingevaluation.PanelObservation, error) {
	if result.PrimaryA.ID == "" || result.PrimaryB.ID == "" || result.PrimaryA.Status != "succeeded" || result.PrimaryB.Status != "succeeded" ||
		result.Panel.Status != PanelResolved && result.Panel.Status != PanelHumanReview {
		return gradingevaluation.PanelObservation{}, ErrPanelShadowSampleIncomplete
	}
	triggered := result.Panel.ArbiterRunID != "" || result.Panel.Status == PanelArbitrationPending || result.Panel.ResolutionSource == ResolutionArbiter
	if result.Panel.Status == PanelHumanReview {
		triggered = true
	}
	if result.Arbiter != nil && sample.ArbiterCostMicros <= 0 {
		return gradingevaluation.PanelObservation{}, ErrInvalidInput
	}
	item := gradingevaluation.PanelObservation{
		ResponseKey: sample.ResponseKey, ResponseFingerprint: sample.ResponseFingerprint,
		EducationStage: string(sample.Context.AssessmentSnapshot.EducationStage),
		Subject:        string(sample.Context.AssessmentSnapshot.SubjectCode), Archetype: sample.Context.AssessmentSnapshot.ArchetypeCode,
		ReferenceKind: gradingevaluation.ReferenceHumanAdjudicated, ReferenceScore: sample.ReferenceScore,
		MaxScore: sample.Context.Question.Score, ScoreA: result.PrimaryA.SuggestedScore, ScoreB: result.PrimaryB.SuggestedScore,
		ResolvedScore: result.Panel.ResolvedScore, ArbitrationTriggered: triggered,
		ResolutionSource: result.Panel.ResolutionSource, HumanEscalated: result.Panel.Status == PanelHumanReview,
		ReferenceReviewers: sample.ReferenceReviewers, ReferenceAdjudicated: sample.ReferenceAdjudicated,
		PrimaryACostMicros: sample.PrimaryACostMicros, PrimaryBCostMicros: sample.PrimaryBCostMicros,
		ArbiterCostMicros: sample.ArbiterCostMicros,
	}
	if result.Arbiter != nil {
		score := result.Arbiter.SuggestedScore
		item.ScoreC = &score
	}
	if _, err := gradingevaluation.CalculatePanelMetrics([]gradingevaluation.PanelObservation{item}); err != nil {
		return gradingevaluation.PanelObservation{}, err
	}
	return item, nil
}
