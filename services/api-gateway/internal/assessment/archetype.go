package assessment

type EvidenceType string

const (
	EvidenceSelectedOption   EvidenceType = "selected_option"
	EvidenceExactText        EvidenceType = "exact_text"
	EvidenceTextSpan         EvidenceType = "text_span"
	EvidenceNumericValue     EvidenceType = "numeric_value"
	EvidenceMathExpression   EvidenceType = "math_expression"
	EvidenceMathStep         EvidenceType = "math_step"
	EvidenceUnitValue        EvidenceType = "unit_value"
	EvidenceChemicalEquation EvidenceType = "chemical_equation"
	EvidenceConcept          EvidenceType = "concept"
	EvidenceRelation         EvidenceType = "relation"
	EvidenceDiagramFeature   EvidenceType = "diagram_feature"
	EvidenceTableCell        EvidenceType = "table_cell"
)

type QuestionArchetype struct {
	Code               string         `json:"code"`
	ResponseSchema     map[string]any `json:"response_schema"`
	EvidenceTypes      []EvidenceType `json:"evidence_types"`
	DefaultScoringMode ScoringMode    `json:"default_scoring_mode"`
}

var canonicalArchetypeCodes = map[string]struct{}{
	"selected_response":  {},
	"exact_text":         {},
	"numeric_expression": {},
	"structured_steps":   {},
	"short_constructed":  {},
	"extended_response":  {},
	"diagram_graph":      {},
	"table_experiment":   {},
}

func IsQuestionArchetype(code string) bool {
	_, ok := canonicalArchetypeCodes[code]
	return ok
}

func (e EvidenceType) Valid() bool {
	switch e {
	case EvidenceSelectedOption, EvidenceExactText, EvidenceTextSpan, EvidenceNumericValue,
		EvidenceMathExpression, EvidenceMathStep, EvidenceUnitValue, EvidenceChemicalEquation,
		EvidenceConcept, EvidenceRelation, EvidenceDiagramFeature, EvidenceTableCell:
		return true
	default:
		return false
	}
}

// DefaultQuestionArchetypes 定义题型可接受的证据及默认评分模式；默认模式不等于已获得外部 AI 准入。
func DefaultQuestionArchetypes() []QuestionArchetype {
	return []QuestionArchetype{
		{Code: "selected_response", ResponseSchema: map[string]any{"type": "selected_response"}, EvidenceTypes: []EvidenceType{EvidenceSelectedOption}, DefaultScoringMode: ScoringRuleAuto},
		{Code: "exact_text", ResponseSchema: map[string]any{"type": "text", "comparison": "normalized_exact"}, EvidenceTypes: []EvidenceType{EvidenceExactText, EvidenceTextSpan}, DefaultScoringMode: ScoringRuleAuto},
		{Code: "numeric_expression", ResponseSchema: map[string]any{"type": "numeric_expression"}, EvidenceTypes: []EvidenceType{EvidenceNumericValue, EvidenceMathExpression, EvidenceUnitValue}, DefaultScoringMode: ScoringRuleAuto},
		{Code: "structured_steps", ResponseSchema: map[string]any{"type": "ordered_steps"}, EvidenceTypes: []EvidenceType{EvidenceTextSpan, EvidenceMathExpression, EvidenceMathStep, EvidenceUnitValue, EvidenceChemicalEquation}, DefaultScoringMode: ScoringAIAssist},
		{Code: "short_constructed", ResponseSchema: map[string]any{"type": "constructed_response", "length": "short"}, EvidenceTypes: []EvidenceType{EvidenceTextSpan, EvidenceConcept, EvidenceRelation}, DefaultScoringMode: ScoringAIAssist},
		{Code: "extended_response", ResponseSchema: map[string]any{"type": "constructed_response", "length": "extended", "rubric": "multi_trait"}, EvidenceTypes: []EvidenceType{EvidenceTextSpan, EvidenceConcept, EvidenceRelation}, DefaultScoringMode: ScoringHumanPrimary},
		{Code: "diagram_graph", ResponseSchema: map[string]any{"type": "diagram_or_graph"}, EvidenceTypes: []EvidenceType{EvidenceDiagramFeature, EvidenceTextSpan, EvidenceNumericValue, EvidenceUnitValue}, DefaultScoringMode: ScoringHumanPrimary},
		{Code: "table_experiment", ResponseSchema: map[string]any{"type": "table_or_experiment"}, EvidenceTypes: []EvidenceType{EvidenceTableCell, EvidenceDiagramFeature, EvidenceTextSpan, EvidenceNumericValue, EvidenceUnitValue}, DefaultScoringMode: ScoringHumanPrimary},
	}
}
