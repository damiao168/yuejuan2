package mathunderstanding

import (
	"errors"
	"fmt"
	"time"
)

var (
	ErrInvalidInput = errors.New("invalid math understanding input")
	ErrNotFound     = errors.New("math understanding artifact not found")
)

// BoundingBox 使用相对于输入图像的 0–1 坐标；X、Y 是左上角，宽高不是像素值。
type BoundingBox struct {
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
}

type MathAnswerBlock struct {
	ID                    string      `json:"id"`
	Kind                  string      `json:"kind"`
	Status                string      `json:"status"`
	BoundingBox           BoundingBox `json:"bbox"`
	Text                  string      `json:"text,omitempty"`
	Normalized            string      `json:"normalized,omitempty"`
	RecognitionEngine     string      `json:"recognition_engine"`
	RecognitionVersion    string      `json:"recognition_version"`
	RecognitionConfidence float64     `json:"recognition_confidence"`
	StructureConfidence   float64     `json:"structure_confidence"`
	SourceImageHash       string      `json:"source_image_hash"`
	SourceRefs            []string    `json:"source_refs,omitempty"`
}

type FormulaArtifact struct {
	ID                 string             `json:"id"`
	BlockID            string             `json:"block_id"`
	BoundingBox        BoundingBox        `json:"bbox"`
	RawLatex           string             `json:"raw_latex,omitempty"`
	CanonicalLatex     string             `json:"canonical_latex,omitempty"`
	RecognitionEngine  string             `json:"recognition_engine"`
	RecognitionVersion string             `json:"recognition_version"`
	ParserVersion      string             `json:"parser_version"`
	ParseStatus        string             `json:"parse_status"`
	Confidence         float64            `json:"confidence"`
	AST                *FormulaAST        `json:"ast,omitempty"`
	Symbols            []FormulaSymbol    `json:"symbols,omitempty"`
	Relations          []FormulaRelation  `json:"relations,omitempty"`
	Warnings           []string           `json:"warnings,omitempty"`
	Candidates         []FormulaCandidate `json:"candidates,omitempty"`
	SelectedCandidate  int                `json:"selected_candidate"`
}

// FormulaCandidate preserves the governed recognition alternatives that were
// considered for one ROI. CanonicalLatex remains the selected immutable
// evidence value; candidates make recognition disputes auditable.
type FormulaCandidate struct {
	Latex       string   `json:"latex"`
	Engine      string   `json:"engine"`
	Version     string   `json:"version"`
	Confidence  float64  `json:"confidence"`
	SyntaxValid bool     `json:"syntax_valid"`
	RenderScore *float64 `json:"render_score,omitempty"`
}

type FormulaSymbol struct {
	ID          string      `json:"id"`
	Value       string      `json:"value"`
	BoundingBox BoundingBox `json:"bbox"`
	Confidence  float64     `json:"confidence"`
}

type FormulaRelation struct {
	FromSymbolID string  `json:"from_symbol_id"`
	ToSymbolID   string  `json:"to_symbol_id"`
	Kind         string  `json:"kind"`
	Confidence   float64 `json:"confidence"`
}

// FormulaAST is intentionally restricted. It is an evidence representation,
// not an executable language and never carries arbitrary code.
type FormulaAST struct {
	Kind     string       `json:"kind"`
	Value    string       `json:"value,omitempty"`
	Children []FormulaAST `json:"children,omitempty"`
}

type SpatialRelation struct {
	ID                  string   `json:"id"`
	FromID              string   `json:"from_id"`
	ToID                string   `json:"to_id"`
	Kind                string   `json:"kind"`
	GeometryScore       float64  `json:"geometry_score"`
	ConnectorScore      float64  `json:"connector_score"`
	MathDependencyScore float64  `json:"math_dependency_score"`
	Confidence          float64  `json:"confidence"`
	Evidence            []string `json:"evidence,omitempty"`
}

type SolutionStep struct {
	ID                    string       `json:"id"`
	OrderHint             int          `json:"order_hint"`
	BlockIDs              []string     `json:"block_ids"`
	FormulaIDs            []string     `json:"formula_ids,omitempty"`
	NormalizedText        string       `json:"normalized_text,omitempty"`
	BoundingBox           *BoundingBox `json:"bbox,omitempty"`
	Kind                  string       `json:"kind,omitempty"`
	RecognitionConfidence float64      `json:"recognition_confidence,omitempty"`
	StructureConfidence   float64      `json:"structure_confidence,omitempty"`
	Confidence            float64      `json:"confidence"`
}

type SolutionEdge struct {
	FromStepID string `json:"from_step_id"`
	ToStepID   string `json:"to_step_id"`
	Kind       string `json:"kind"`
}

type SolutionGraph struct {
	ID                  string         `json:"id"`
	AnswerSegmentID     string         `json:"answer_segment_id"`
	BuilderVersion      string         `json:"builder_version"`
	FormulaModelVersion string         `json:"formula_model_version"`
	OverallConfidence   float64        `json:"overall_confidence"`
	RequiresHumanReview bool           `json:"requires_human_review"`
	Steps               []SolutionStep `json:"steps"`
	Edges               []SolutionEdge `json:"edges"`
}

type MathVerification struct {
	ID             string         `json:"id"`
	StepID         string         `json:"step_id,omitempty"`
	FormulaID      string         `json:"formula_id,omitempty"`
	Kind           string         `json:"kind"`
	Status         string         `json:"status"`
	ReasonCode     string         `json:"reason_code"`
	Domain         string         `json:"domain"`
	Constraints    []string       `json:"constraints,omitempty"`
	Engine         string         `json:"engine"`
	EngineVersion  string         `json:"engine_version"`
	RulesetVersion string         `json:"ruleset_version"`
	Details        map[string]any `json:"details,omitempty"`
	Confidence     float64        `json:"confidence"`
}

type RubricEvidence struct {
	ID                 string   `json:"id"`
	RubricCriterionKey string   `json:"rubric_criterion_key"`
	EvidenceType       string   `json:"evidence_type"`
	SourceArtifactIDs  []string `json:"source_artifact_ids"`
	VerificationIDs    []string `json:"verification_ids,omitempty"`
	Status             string   `json:"status"`
	Explanation        string   `json:"explanation,omitempty"`
	Confidence         float64  `json:"confidence"`
}

type CreateArtifactInput struct {
	SubjectCode            string             `json:"subject_code"`
	AnswerSegmentID        string             `json:"answer_segment_id"`
	ExamQuestionSnapshotID string             `json:"exam_question_snapshot_id"`
	InputHash              string             `json:"input_hash"`
	EngineVersion          string             `json:"engine_version"`
	Blocks                 []MathAnswerBlock  `json:"blocks"`
	Formulas               []FormulaArtifact  `json:"formulas"`
	Relations              []SpatialRelation  `json:"relations"`
	SolutionGraph          SolutionGraph      `json:"solution_graph"`
	Verifications          []MathVerification `json:"verifications"`
	RubricEvidence         []RubricEvidence   `json:"rubric_evidence"`
}

type Artifact struct {
	ID                     string         `json:"id"`
	TenantID               string         `json:"tenant_id"`
	AnswerSegmentID        string         `json:"answer_segment_id"`
	ExamQuestionSnapshotID string         `json:"exam_question_snapshot_id"`
	Version                int64          `json:"version"`
	InputHash              string         `json:"input_hash"`
	EngineVersion          string         `json:"engine_version"`
	IsCurrent              bool           `json:"is_current"`
	Stage                  string         `json:"stage"`
	ParentArtifactID       string         `json:"parent_artifact_id,omitempty"`
	CorrectionRevision     int64          `json:"correction_revision"`
	QualitySummary         map[string]any `json:"quality_summary"`
	CreateArtifactInput
	CreatedAt time.Time `json:"created_at"`
}

// EffectiveArtifact keeps the immutable recognition artifact separate from the
// latest teacher-authored projection. Downstream consumers should use
// EffectiveContract for mathematical evidence and BaseArtifact for identity,
// provenance, and version binding.
type EffectiveArtifact struct {
	BaseArtifact       Artifact
	EffectiveContract  CreateArtifactInput
	CorrectionRevision int64
	Corrected          bool
}

var validBlockKinds = set("text", "formula", "mixed", "diagram", "table", "connector", "unknown")
var validBlockStatuses = set("active", "crossed_out", "inserted", "scratch_candidate", "uncertain")
var validASTKinds = set("number", "symbol", "operator", "fraction", "radical", "power", "subscript", "equation", "inequality", "function", "matrix", "unit", "group", "unknown")
var validFormulaRelationKinds = set("right_of", "above", "below", "superscript", "subscript", "numerator_of", "denominator_of", "inside_radical", "argument_of")
var validParseStatuses = set("parsed", "ambiguous", "unsupported", "failed")
var validRelationKinds = set("left_of", "right_of", "above", "below", "contains", "continues", "implies", "labels", "same_line", "unknown")
var validEdgeKinds = set("next", "derives", "supports", "corrects", "branches")
var validStepKinds = set("setup", "transformation", "calculation", "conclusion", "explanation", "branch")
var validVerificationKinds = set("syntax", "equivalence", "substitution", "unit", "constraint", "arithmetic")
var validVerificationStatuses = set("verified", "contradicted", "uncertain", "not_applicable")
var validEvidenceStatuses = set("supported", "contradicted", "uncertain", "not_applicable", "unsupported")

// ValidateCreateArtifact 检查字段范围、证据引用和图结构。
// 它不查询数据库；题块是否属于该租户和考试快照，由存储层核实。
func ValidateCreateArtifact(input CreateArtifactInput) error {
	if !set("mathematics", "physics", "chemistry")[input.SubjectCode] || input.AnswerSegmentID == "" || input.ExamQuestionSnapshotID == "" || input.InputHash == "" || input.EngineVersion == "" {
		return ErrInvalidInput
	}
	if len(input.Blocks) == 0 || len(input.Blocks) > 512 || len(input.Formulas) > 512 || len(input.Relations) > 2048 {
		return ErrInvalidInput
	}
	blockIDs := map[string]bool{}
	formulaIDs := map[string]bool{}
	for _, block := range input.Blocks {
		if block.ID == "" || blockIDs[block.ID] || !validBlockKinds[block.Kind] || !validBlockStatuses[block.Status] || !validBox(block.BoundingBox) || block.RecognitionEngine == "" || block.RecognitionVersion == "" || block.SourceImageHash == "" || !validConfidence(block.RecognitionConfidence) || !validConfidence(block.StructureConfidence) {
			return fmt.Errorf("%w: invalid answer block", ErrInvalidInput)
		}
		blockIDs[block.ID] = true
	}
	for _, formula := range input.Formulas {
		if formula.ID == "" || formulaIDs[formula.ID] || !blockIDs[formula.BlockID] || !validBox(formula.BoundingBox) || formula.RecognitionEngine == "" || formula.RecognitionVersion == "" || formula.ParserVersion == "" || !validParseStatuses[formula.ParseStatus] || !validConfidence(formula.Confidence) {
			return fmt.Errorf("%w: invalid formula artifact", ErrInvalidInput)
		}
		if formula.AST != nil && !validAST(*formula.AST, 0, new(int)) {
			return fmt.Errorf("%w: invalid formula AST", ErrInvalidInput)
		}
		if len(formula.Candidates) > 16 || (len(formula.Candidates) > 0 && (formula.SelectedCandidate < 0 || formula.SelectedCandidate >= len(formula.Candidates))) {
			return fmt.Errorf("%w: invalid formula candidate selection", ErrInvalidInput)
		}
		for _, candidate := range formula.Candidates {
			if candidate.Latex == "" || candidate.Engine == "" || candidate.Version == "" || !validConfidence(candidate.Confidence) || (candidate.RenderScore != nil && !validConfidence(*candidate.RenderScore)) {
				return fmt.Errorf("%w: invalid formula candidate", ErrInvalidInput)
			}
		}
		symbols := map[string]bool{}
		for _, symbol := range formula.Symbols {
			if symbol.ID == "" || symbols[symbol.ID] || symbol.Value == "" || !validBox(symbol.BoundingBox) || !validConfidence(symbol.Confidence) {
				return fmt.Errorf("%w: invalid formula symbol", ErrInvalidInput)
			}
			symbols[symbol.ID] = true
		}
		for _, relation := range formula.Relations {
			if !symbols[relation.FromSymbolID] || !symbols[relation.ToSymbolID] || relation.FromSymbolID == relation.ToSymbolID || !validFormulaRelationKinds[relation.Kind] || !validConfidence(relation.Confidence) {
				return fmt.Errorf("%w: invalid formula relation", ErrInvalidInput)
			}
		}
		formulaIDs[formula.ID] = true
	}
	for _, relation := range input.Relations {
		if relation.ID == "" || relation.FromID == relation.ToID || !validRelationKinds[relation.Kind] || !validConfidence(relation.Confidence) || !knownArtifact(relation.FromID, blockIDs, formulaIDs) || !knownArtifact(relation.ToID, blockIDs, formulaIDs) {
			return fmt.Errorf("%w: invalid spatial relation", ErrInvalidInput)
		}
	}
	if input.SolutionGraph.AnswerSegmentID != input.AnswerSegmentID {
		return fmt.Errorf("%w: graph segment mismatch", ErrInvalidInput)
	}
	if err := validateGraph(input.SolutionGraph, blockIDs, formulaIDs); err != nil {
		return err
	}
	stepIDs := map[string]bool{}
	for _, step := range input.SolutionGraph.Steps {
		stepIDs[step.ID] = true
	}
	verificationIDs := map[string]bool{}
	for _, check := range input.Verifications {
		if check.ID == "" || verificationIDs[check.ID] || check.Domain == "" || check.Engine == "" || check.EngineVersion == "" || check.RulesetVersion == "" || !validVerificationKinds[check.Kind] || !validVerificationStatuses[check.Status] || !validConfidence(check.Confidence) || (check.StepID != "" && !stepIDs[check.StepID]) || (check.FormulaID != "" && !formulaIDs[check.FormulaID]) {
			return fmt.Errorf("%w: invalid verification", ErrInvalidInput)
		}
		verificationIDs[check.ID] = true
	}
	evidenceIDs := map[string]bool{}
	for _, evidence := range input.RubricEvidence {
		if evidence.ID == "" || evidenceIDs[evidence.ID] || evidence.RubricCriterionKey == "" || evidence.EvidenceType == "" || (len(evidence.SourceArtifactIDs) == 0 && set("supported", "contradicted", "unsupported")[evidence.Status]) || !validEvidenceStatuses[evidence.Status] || !validConfidence(evidence.Confidence) {
			return fmt.Errorf("%w: invalid rubric evidence", ErrInvalidInput)
		}
		evidenceIDs[evidence.ID] = true
		for _, id := range evidence.VerificationIDs {
			if !verificationIDs[id] {
				return fmt.Errorf("%w: unknown evidence verification", ErrInvalidInput)
			}
		}
		for _, id := range evidence.SourceArtifactIDs {
			if !knownArtifact(id, blockIDs, formulaIDs) && !stepIDs[id] {
				return fmt.Errorf("%w: unknown evidence source", ErrInvalidInput)
			}
		}
	}
	return nil
}

// validateGraph 要求所有边连接已存在的步骤且不能形成环；否则后续依赖回溯没有可靠的起止关系。
func validateGraph(graph SolutionGraph, blocks, formulas map[string]bool) error {
	if graph.ID == "" || graph.AnswerSegmentID == "" || graph.BuilderVersion == "" || graph.FormulaModelVersion == "" || !validConfidence(graph.OverallConfidence) || len(graph.Steps) == 0 || len(graph.Steps) > 512 || len(graph.Edges) > 2048 {
		return ErrInvalidInput
	}
	steps := map[string]bool{}
	for _, step := range graph.Steps {
		if step.ID == "" || steps[step.ID] || !validConfidence(step.Confidence) || !validConfidence(step.RecognitionConfidence) || !validConfidence(step.StructureConfidence) || len(step.BlockIDs) == 0 || (step.BoundingBox != nil && !validBox(*step.BoundingBox)) || (step.Kind != "" && !validStepKinds[step.Kind]) {
			return fmt.Errorf("%w: invalid solution step", ErrInvalidInput)
		}
		for _, id := range step.BlockIDs {
			if !blocks[id] {
				return fmt.Errorf("%w: unknown block", ErrInvalidInput)
			}
		}
		for _, id := range step.FormulaIDs {
			if !formulas[id] {
				return fmt.Errorf("%w: unknown formula", ErrInvalidInput)
			}
		}
		steps[step.ID] = true
	}
	adj := map[string][]string{}
	for _, edge := range graph.Edges {
		if !steps[edge.FromStepID] || !steps[edge.ToStepID] || edge.FromStepID == edge.ToStepID || !validEdgeKinds[edge.Kind] {
			return fmt.Errorf("%w: invalid solution edge", ErrInvalidInput)
		}
		adj[edge.FromStepID] = append(adj[edge.FromStepID], edge.ToStepID)
	}
	state := map[string]uint8{}
	var visit func(string) bool
	visit = func(id string) bool {
		if state[id] == 1 {
			return false
		}
		if state[id] == 2 {
			return true
		}
		state[id] = 1
		for _, next := range adj[id] {
			if !visit(next) {
				return false
			}
		}
		state[id] = 2
		return true
	}
	for id := range steps {
		if !visit(id) {
			return fmt.Errorf("%w: solution graph must be acyclic", ErrInvalidInput)
		}
	}
	return nil
}

// 深度、总节点数和单节点子项数分别受限，避免异常公式让递归校验耗尽资源。
func validAST(node FormulaAST, depth int, count *int) bool {
	*count++
	if depth > 32 || *count > 2048 || !validASTKinds[node.Kind] || len(node.Children) > 64 {
		return false
	}
	for _, child := range node.Children {
		if !validAST(child, depth+1, count) {
			return false
		}
	}
	return true
}

func validBox(box BoundingBox) bool {
	return box.X >= 0 && box.Y >= 0 && box.Width > 0 && box.Height > 0 && box.X+box.Width <= 1.000001 && box.Y+box.Height <= 1.000001
}
func validConfidence(value float64) bool { return value >= 0 && value <= 1 }
func knownArtifact(id string, blocks, formulas map[string]bool) bool {
	return blocks[id] || formulas[id]
}
func set(values ...string) map[string]bool {
	out := map[string]bool{}
	for _, value := range values {
		out[value] = true
	}
	return out
}
