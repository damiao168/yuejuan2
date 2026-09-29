package subjective

import (
	"context"
	"encoding/hex"
	"errors"
	"math"
	"strings"

	"edugrade-enterprise/services/api-gateway/internal/assessment"
	"edugrade-enterprise/services/api-gateway/internal/mathunderstanding"
)

const MathScoringVersionV1 = "math-rubric-score-v1"

type MathEvidenceQuality struct {
	Recognition   float64 `json:"recognition"`
	Formula       float64 `json:"formula"`
	Structure     float64 `json:"structure"`
	Verification  float64 `json:"verification"`
	RubricMapping float64 `json:"rubric_mapping"`
	Critical      float64 `json:"critical"`
}

type MathEvidenceStep struct {
	ID         string   `json:"id"`
	Text       string   `json:"text,omitempty"`
	FormulaIDs []string `json:"formula_ids"`
	Confidence float64  `json:"confidence"`
}

type MathEvidenceFormula struct {
	ID             string                        `json:"id"`
	StepIDs        []string                      `json:"step_ids"`
	CanonicalLatex string                        `json:"canonical_latex,omitempty"`
	ParseStatus    string                        `json:"parse_status"`
	ReasonCodes    []string                      `json:"reason_codes"`
	BoundingBox    mathunderstanding.BoundingBox `json:"bbox"`
	Confidence     float64                       `json:"confidence"`
}

type MathEvidenceVerification struct {
	ID         string  `json:"id"`
	StepID     string  `json:"step_id,omitempty"`
	FormulaID  string  `json:"formula_id,omitempty"`
	Kind       string  `json:"kind"`
	Status     string  `json:"status"`
	ReasonCode string  `json:"reason_code"`
	Confidence float64 `json:"confidence"`
}

type MathEvidenceContext struct {
	ArtifactID              string                             `json:"artifact_id"`
	ArtifactVersion         int64                              `json:"artifact_version"`
	CorrectionRevision      int64                              `json:"correction_revision"`
	ExamQuestionSnapshotID  string                             `json:"exam_question_snapshot_id"`
	ScoringVersion          string                             `json:"scoring_version"`
	OverallConfidence       float64                            `json:"overall_confidence"`
	Quality                 MathEvidenceQuality                `json:"quality"`
	Steps                   []MathEvidenceStep                 `json:"steps"`
	Formulas                []MathEvidenceFormula              `json:"formulas"`
	Verifications           []MathEvidenceVerification         `json:"verifications"`
	RubricEvidence          []mathunderstanding.RubricEvidence `json:"rubric_evidence"`
	HasDiagram              bool                               `json:"has_diagram"`
	GraphUncertain          bool                               `json:"graph_uncertain"`
	RequiredRubricUncertain bool                               `json:"required_rubric_uncertain"`
	effective               mathunderstanding.EffectiveArtifact
	frozen                  mathunderstanding.FrozenRubric
	baseline                mathunderstanding.RubricScore
	cropInputHash           string
}

type MathCriterionCandidate struct {
	RubricPointID string   `json:"rubric_point_id"`
	Status        string   `json:"status"`
	EvidenceIDs   []string `json:"evidence_ids"`
	Confidence    float64  `json:"confidence"`
	ReasonCode    string   `json:"reason_code"`
}

type MathEvidenceSource struct {
	Artifacts   mathunderstanding.Store
	Corrections mathunderstanding.CorrectionStore
	Crops       ActiveCropEvidenceStore
	requireCrop bool
}

// 数学证据必须绑定当前答案版本、题目快照和裁剪哈希；绑定失败时宁可停在人工复核，也不复用旧证据。
func (s MathEvidenceSource) Prepare(ctx context.Context, tenantID string, value Context) (*MathEvidenceContext, error) {
	if value.AssessmentSnapshot.SubjectCode != assessment.SubjectMathematics || s.Artifacts == nil || s.Corrections == nil {
		return nil, nil
	}
	effective, err := mathunderstanding.ResolveEffectiveArtifact(ctx, s.Artifacts, s.Corrections, tenantID, value.SegmentID)
	if errors.Is(err, mathunderstanding.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	base, contract := effective.BaseArtifact, effective.EffectiveContract
	if !base.IsCurrent || base.Stage != "verified" || base.AnswerSegmentID != value.SegmentID ||
		base.ExamQuestionSnapshotID == "" || base.ExamQuestionSnapshotID != value.AssessmentSnapshot.ID ||
		contract.SubjectCode != "mathematics" {
		return nil, nil
	}
	if s.requireCrop || s.Crops != nil {
		if s.Crops == nil {
			return nil, ErrActiveCropUnavailable
		}
		crop, cropErr := s.Crops.GetEvidenceForQuestion(ctx, tenantID, value.SegmentID, value.Question.ID)
		if cropErr != nil || strings.TrimSpace(crop.CropSHA256) == "" {
			return nil, ErrActiveCropUnavailable
		}
		if crop.SegmentID != value.SegmentID || crop.QuestionID != value.Question.ID || !mathCropHashMatches(base.InputHash, crop.CropSHA256) {
			return nil, mathunderstanding.ErrRevisionConflict
		}
	}
	frozen := mathunderstanding.FrozenRubric{SnapshotID: value.AssessmentSnapshot.ID, Rubric: value.Rubric}
	baseline, err := mathunderstanding.ScoreFrozenRubric(frozen, effective, nil)
	if err != nil {
		return nil, nil
	}
	out := &MathEvidenceContext{
		// The verified artifact records the correction revision already applied;
		// EffectiveArtifact records a newer projection still attached to it. Their
		// sum is monotonic for this artifact and changes if a correction arrives
		// while grading is in flight.
		ArtifactID: base.ID, ArtifactVersion: base.Version, CorrectionRevision: base.CorrectionRevision + effective.CorrectionRevision,
		ExamQuestionSnapshotID: base.ExamQuestionSnapshotID, ScoringVersion: MathScoringVersionV1,
		OverallConfidence: contract.SolutionGraph.OverallConfidence,
		Steps:             []MathEvidenceStep{}, Formulas: []MathEvidenceFormula{}, Verifications: []MathEvidenceVerification{},
		RubricEvidence: append([]mathunderstanding.RubricEvidence(nil), baseline.RubricEvidence...),
		GraphUncertain: contract.SolutionGraph.RequiresHumanReview,
		effective:      effective, frozen: frozen, baseline: baseline, cropInputHash: base.InputHash,
	}
	stepIDsByFormula := map[string][]string{}
	for _, step := range contract.SolutionGraph.Steps {
		out.Steps = append(out.Steps, MathEvidenceStep{ID: step.ID, Text: step.NormalizedText, FormulaIDs: nonNilStrings(step.FormulaIDs), Confidence: step.Confidence})
		for _, formulaID := range step.FormulaIDs {
			stepIDsByFormula[formulaID] = append(stepIDsByFormula[formulaID], step.ID)
		}
		if step.Confidence < 0.8 || step.StructureConfidence < 0.8 {
			out.GraphUncertain = true
		}
	}
	for _, block := range contract.Blocks {
		if block.Kind == "diagram" {
			out.HasDiagram = true
		}
	}
	for _, formula := range contract.Formulas {
		out.Formulas = append(out.Formulas, MathEvidenceFormula{
			ID: formula.ID, StepIDs: nonNilStrings(stepIDsByFormula[formula.ID]), CanonicalLatex: formula.CanonicalLatex,
			ParseStatus: formula.ParseStatus, ReasonCodes: nonNilStrings(formula.Warnings), BoundingBox: formula.BoundingBox, Confidence: formula.Confidence,
		})
	}
	for _, check := range contract.Verifications {
		out.Verifications = append(out.Verifications, MathEvidenceVerification{ID: check.ID, StepID: check.StepID, FormulaID: check.FormulaID, Kind: check.Kind, Status: check.Status, ReasonCode: check.ReasonCode, Confidence: check.Confidence})
	}
	for _, decision := range baseline.CriterionDecisions {
		if decision.RequiresHumanReview {
			out.RequiredRubricUncertain = true
			break
		}
	}
	out.Quality = deriveMathEvidenceQuality(contract, baseline)
	return out, nil
}

func mathCropHashMatches(inputHash, cropSHA256 string) bool {
	inputHash = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(inputHash), "sha256:"))
	digest, err := hex.DecodeString(inputHash)
	return err == nil && len(digest) == 32 && inputHash == strings.ToLower(strings.TrimSpace(cropSHA256))
}

func deriveMathEvidenceQuality(contract mathunderstanding.CreateArtifactInput, score mathunderstanding.RubricScore) MathEvidenceQuality {
	quality := MathEvidenceQuality{Recognition: 1, Formula: 1, Structure: contract.SolutionGraph.OverallConfidence, Verification: 1, RubricMapping: 1}
	if len(contract.Blocks) == 0 {
		quality.Recognition = 0
	}
	for _, block := range contract.Blocks {
		quality.Recognition = math.Min(quality.Recognition, block.RecognitionConfidence)
	}
	if len(contract.Formulas) == 0 {
		quality.Formula = quality.Recognition
	}
	for _, formula := range contract.Formulas {
		quality.Formula = math.Min(quality.Formula, formula.Confidence)
		if formula.ParseStatus != "parsed" {
			quality.Formula = 0
		}
	}
	for _, step := range contract.SolutionGraph.Steps {
		quality.Structure = math.Min(quality.Structure, math.Min(step.Confidence, step.StructureConfidence))
	}
	if len(contract.Verifications) == 0 {
		quality.Verification = 0
	}
	for _, check := range contract.Verifications {
		quality.Verification = math.Min(quality.Verification, check.Confidence)
		if check.Status == "uncertain" {
			quality.Verification = 0
		}
	}
	if len(score.RubricEvidence) == 0 {
		quality.RubricMapping = 0
	}
	for _, evidence := range score.RubricEvidence {
		quality.RubricMapping = math.Min(quality.RubricMapping, evidence.Confidence)
		if evidence.Status == "uncertain" || evidence.Status == "unsupported" {
			quality.RubricMapping = 0
		}
	}
	quality.Critical = math.Min(quality.Recognition, math.Min(quality.Formula, math.Min(quality.Structure, math.Min(quality.Verification, quality.RubricMapping))))
	return quality
}

func (m *MathEvidenceContext) bindingMatches(run GradingRun) bool {
	return m != nil && run.MathArtifactID == m.ArtifactID && run.MathArtifactVersion == m.ArtifactVersion &&
		run.MathCorrectionRevision == m.CorrectionRevision && run.MathScoringVersion == m.ScoringVersion
}

func mathSubject(value Context) bool {
	return value.AssessmentSnapshot.SubjectCode == assessment.SubjectMathematics || strings.EqualFold(value.Subject, "mathematics")
}
