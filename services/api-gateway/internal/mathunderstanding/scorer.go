package mathunderstanding

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"strings"

	"edugrade-enterprise/services/api-gateway/internal/grading"
	"edugrade-enterprise/services/api-gateway/internal/paper"
)

type FrozenRubric struct {
	SnapshotID string
	Rubric     paper.Rubric
}

type CriterionDecision struct {
	RubricPointID       string   `json:"rubric_point_id"`
	Status              string   `json:"status"`
	MaxScore            float64  `json:"max_score"`
	AwardedScore        *float64 `json:"awarded_score"`
	EvidenceIDs         []string `json:"evidence_ids"`
	VerificationIDs     []string `json:"verification_ids"`
	DecisionSource      string   `json:"decision_source"`
	RequiresHumanReview bool     `json:"requires_human_review"`
	ReasonCode          string   `json:"reason_code"`
}

// CriterionCandidate deliberately has no score field. Candidates are hints,
// never authority to award or deny a frozen rubric point.
type CriterionCandidate struct {
	RubricPointID string   `json:"rubric_point_id"`
	Status        string   `json:"status"`
	EvidenceIDs   []string `json:"evidence_ids"`
}

type ScoreRange struct {
	Min float64 `json:"min"`
	Max float64 `json:"max"`
}

type RubricScore struct {
	SchemaVersion              string                `json:"schema_version"`
	Scope                      string                `json:"scope"`
	ArtifactID                 string                `json:"artifact_id"`
	ArtifactVersion            int64                 `json:"artifact_version"`
	CorrectionRevision         int64                 `json:"correction_revision"`
	VerifiedCorrectionRevision int64                 `json:"verified_correction_revision"`
	ExamQuestionSnapshotID     string                `json:"exam_question_snapshot_id"`
	RubricID                   string                `json:"rubric_id"`
	RubricVersion              string                `json:"rubric_version"`
	RubricSnapshotHash         string                `json:"rubric_snapshot_hash"`
	MaxScore                   float64               `json:"max_score"`
	VerifiedScore              float64               `json:"verified_score"`
	UnresolvedScore            float64               `json:"unresolved_score"`
	SuggestedScore             *float64              `json:"suggested_score"`
	ScoreRange                 ScoreRange            `json:"score_range"`
	RequiresHumanReview        bool                  `json:"requires_human_review"`
	CriterionDecisions         []CriterionDecision   `json:"criterion_decisions"`
	RubricEvidence             []RubricEvidence      `json:"rubric_evidence"`
	MatchedPoints              []grading.PointResult `json:"matched_points"`
	MissingPoints              []grading.PointResult `json:"missing_points"`
}

// Scores use fixed 1/10000-point units to avoid binary-float aggregation drift.
func scoreUnits(score float64) (int64, bool) {
	if math.IsNaN(score) || math.IsInf(score, 0) || score < 0 || score > 1e6 {
		return 0, false
	}
	units := math.Round(score * 10000)
	return int64(units), math.Abs(score*10000-units) < 1e-6
}

func validateFrozenRubric(frozen FrozenRubric) error {
	r := frozen.Rubric
	max, ok := scoreUnits(r.MaxScore)
	if frozen.SnapshotID == "" || !set("approved", "locked")[r.Status] || !ok || max <= 0 || len(r.Points) == 0 || len(r.Points) > 512 {
		return fmt.Errorf("%w: frozen rubric missing or invalid", ErrInvalidInput)
	}
	total := int64(0)
	ids := map[string]bool{}
	for _, point := range r.Points {
		units, valid := scoreUnits(point.Score)
		if strings.TrimSpace(point.ID) == "" || ids[point.ID] || !valid || !paper.ValidRubricEvidenceRequirements([]paper.RubricPoint{point}) {
			return fmt.Errorf("%w: invalid frozen rubric point", ErrInvalidInput)
		}
		ids[point.ID] = true
		total += units
	}
	if total != max {
		return fmt.Errorf("%w: frozen rubric total mismatch", ErrInvalidInput)
	}
	return nil
}

// ScoreFrozenRubric 按考试冻结的量规计算教师建议，不保存成绩。
// 模型候选只补充待复核线索，不能代替证据决定得分或零分。
func ScoreFrozenRubric(frozen FrozenRubric, effective EffectiveArtifact, candidates []CriterionCandidate) (RubricScore, error) {
	if err := validateFrozenRubric(frozen); err != nil {
		return RubricScore{}, err
	}
	base := effective.BaseArtifact
	contract := effective.EffectiveContract
	if base.ID == "" || !base.IsCurrent || base.SubjectCode != "mathematics" || base.ExamQuestionSnapshotID != frozen.SnapshotID || contract.ExamQuestionSnapshotID != frozen.SnapshotID || !sameArtifactBinding(contract, base) || effective.CorrectionRevision < 0 {
		return RubricScore{}, ErrInvalidInput
	}
	if err := ValidateCreateArtifact(contract); err != nil {
		return RubricScore{}, err
	}
	r := frozen.Rubric
	raw, err := json.Marshal(r)
	if err != nil {
		return RubricScore{}, err
	}
	digest := sha256.Sum256(raw)
	hash := "sha256:" + hex.EncodeToString(digest[:])
	rubricID, version := r.ID, r.Version
	if rubricID == "" {
		rubricID = "exam-question-snapshot:" + frozen.SnapshotID
	}
	if version == "" {
		version = hash
	}
	out := RubricScore{SchemaVersion: "math-rubric-score-v1", Scope: "teacher_suggestion_only",
		ArtifactID: base.ID, ArtifactVersion: base.Version, CorrectionRevision: effective.CorrectionRevision, VerifiedCorrectionRevision: base.CorrectionRevision,
		ExamQuestionSnapshotID: frozen.SnapshotID, RubricID: rubricID, RubricVersion: version, RubricSnapshotHash: hash, MaxScore: r.MaxScore,
		CriterionDecisions: []CriterionDecision{}, RubricEvidence: []RubricEvidence{}, MatchedPoints: []grading.PointResult{}, MissingPoints: []grading.PointResult{},
		// A settled arithmetic suggestion still requires teacher confirmation.
		RequiresHumanReview: true}
	input := EvidenceMatchInput{Requirements: RequirementsFromRubric(r.Points), Graph: contract.SolutionGraph, Blocks: contract.Blocks, Formulas: contract.Formulas, Verifications: contract.Verifications}
	matched, err := MatchRubricEvidence(input)
	if err != nil {
		return RubricScore{}, err
	}
	known := map[string]bool{}
	pointIDs := map[string]bool{}
	for _, point := range r.Points {
		pointIDs[point.ID] = true
	}
	for _, block := range contract.Blocks {
		known[block.ID] = true
	}
	for _, formula := range contract.Formulas {
		known[formula.ID] = true
	}
	for _, step := range contract.SolutionGraph.Steps {
		known[step.ID] = true
	}
	byPoint := map[string]CriterionCandidate{}
	for _, candidate := range candidates {
		if !pointIDs[candidate.RubricPointID] || !set("supported", "contradicted", "uncertain", "not_applicable", "unsupported")[candidate.Status] {
			return RubricScore{}, ErrInvalidInput
		}
		if set("supported", "contradicted")[candidate.Status] && len(candidate.EvidenceIDs) == 0 {
			return RubricScore{}, ErrInvalidInput
		}
		if _, exists := byPoint[candidate.RubricPointID]; exists {
			return RubricScore{}, ErrInvalidInput
		}
		for _, id := range candidate.EvidenceIDs {
			if !known[id] {
				return RubricScore{}, ErrInvalidInput
			}
		}
		byPoint[candidate.RubricPointID] = candidate
	}
	pending := ""
	if base.Stage != "verified" || effective.CorrectionRevision > 0 {
		pending = "math_verification_pending"
	}
	if len(r.Deductions) > 0 {
		pending = "deduction_policy_requires_review"
	}
	verified, unresolvedUnits := int64(0), int64(0)
	for index, point := range r.Points {
		evidence := matched[index]
		m := matchRequirement(input.Requirements[index], input)
		if pending != "" {
			evidence.Status = "uncertain"
			evidence.Explanation = pending
			evidence.SourceArtifactIDs = []string{}
			evidence.VerificationIDs = []string{}
			evidence.Confidence = 0
			m = unresolved(pending)
		}
		decision := CriterionDecision{RubricPointID: point.ID, Status: evidence.Status, MaxScore: point.Score, EvidenceIDs: uniqueIDs(evidence.SourceArtifactIDs), VerificationIDs: uniqueIDs(evidence.VerificationIDs), DecisionSource: m.source, ReasonCode: evidence.Explanation}
		if candidate, exists := byPoint[point.ID]; exists && decision.Status == "uncertain" && pending == "" {
			decision.EvidenceIDs = uniqueIDs(append(decision.EvidenceIDs, candidate.EvidenceIDs...))
			decision.DecisionSource = "model_candidate"
			decision.ReasonCode = "model_candidate_requires_review"
		}
		units, _ := scoreUnits(point.Score)
		switch decision.Status {
		case "supported":
			value := point.Score
			decision.AwardedScore = &value
			verified += units
			out.MatchedPoints = append(out.MatchedPoints, grading.PointResult{Code: point.ID, Label: point.Description, Score: point.Score, EvidenceIDs: decision.EvidenceIDs, Reason: decision.ReasonCode})
		case "contradicted":
			value := float64(0)
			decision.AwardedScore = &value
			out.MissingPoints = append(out.MissingPoints, grading.PointResult{Code: point.ID, Label: point.Description, Score: point.Score, EvidenceIDs: decision.EvidenceIDs, Reason: decision.ReasonCode})
		default:
			decision.Status = "uncertain"
			decision.RequiresHumanReview = true
			unresolvedUnits += units
		}
		out.RubricEvidence = append(out.RubricEvidence, evidence)
		out.CriterionDecisions = append(out.CriterionDecisions, decision)
	}
	out.VerifiedScore = float64(verified) / 10000
	out.UnresolvedScore = float64(unresolvedUnits) / 10000
	out.ScoreRange = ScoreRange{Min: out.VerifiedScore, Max: float64(verified+unresolvedUnits) / 10000}
	// 仍有未决分值时只给出分数范围；不能把这些分值默认当成零分凑成建议总分。
	if unresolvedUnits == 0 {
		value := out.VerifiedScore
		out.SuggestedScore = &value
	}
	return out, nil
}
