package assessment

import (
	"edugrade-enterprise/services/api-gateway/internal/subjectcode"
	"time"
)

type EducationStage string

const (
	StageJunior EducationStage = "junior"
	StageSenior EducationStage = "senior"
)

type SubjectCode string

const (
	SubjectChinese        SubjectCode = "chinese"
	SubjectMathematics    SubjectCode = "mathematics"
	SubjectEnglish        SubjectCode = "english"
	SubjectPhysics        SubjectCode = "physics"
	SubjectChemistry      SubjectCode = "chemistry"
	SubjectBiology        SubjectCode = "biology"
	SubjectHistory        SubjectCode = "history"
	SubjectGeography      SubjectCode = "geography"
	SubjectEthicsPolitics SubjectCode = "ethics_politics"
)

type SubjectProfile struct {
	ID             string         `json:"id"`
	TenantID       string         `json:"tenant_id"`
	Code           string         `json:"code"`
	EducationStage EducationStage `json:"education_stage"`
	SubjectCode    SubjectCode    `json:"subject_code"`
	Version        int            `json:"version"`
	Status         string         `json:"status"`
	ParserPolicy   map[string]any `json:"parser_policy"`
	EvidencePolicy map[string]any `json:"evidence_policy"`
	ScoringDefault ScoringPolicy  `json:"scoring_default"`
	CreatedAt      time.Time      `json:"created_at"`
	UpdatedAt      time.Time      `json:"updated_at"`
}

func NormalizeSubjectCode(value string) (SubjectCode, bool) {
	code, ok := subjectcode.Normalize(value)
	return SubjectCode(code), ok
}

func (s SubjectCode) Valid() bool {
	switch s {
	case SubjectChinese, SubjectMathematics, SubjectEnglish, SubjectPhysics,
		SubjectChemistry, SubjectBiology, SubjectHistory, SubjectGeography,
		SubjectEthicsPolitics:
		return true
	default:
		return false
	}
}

func (s EducationStage) Valid() bool {
	return s == StageJunior || s == StageSenior
}

func DefaultSubjectProfiles(tenantID string) []SubjectProfile {
	now := time.Now().UTC()
	profiles := make([]SubjectProfile, 0, 18)
	for _, stage := range []EducationStage{StageJunior, StageSenior} {
		for _, subject := range []SubjectCode{
			SubjectChinese, SubjectMathematics, SubjectEnglish, SubjectPhysics,
			SubjectChemistry, SubjectBiology, SubjectHistory, SubjectGeography,
			SubjectEthicsPolitics,
		} {
			profiles = append(profiles, SubjectProfile{
				ID:             string(stage) + "." + string(subject) + ".v1",
				TenantID:       tenantID,
				Code:           string(stage) + "." + string(subject) + ".standard",
				EducationStage: stage,
				SubjectCode:    subject,
				Version:        1,
				Status:         "active",
				ParserPolicy:   map[string]any{"parsers": parserNames(subject)},
				EvidencePolicy: map[string]any{"allowed_types": evidenceNames(subjectEvidenceTypes(subject))},
				ScoringDefault: ScoringPolicy{Mode: ScoringAIAssist, RequireEvidence: true, HumanReviewBelowConfidence: true},
				CreatedAt:      now,
				UpdatedAt:      now,
			})
		}
	}
	return profiles
}

func parserNames(subject SubjectCode) []any {
	values := []any{"layout_text"}
	switch subject {
	case SubjectChinese, SubjectHistory, SubjectEthicsPolitics:
		values = append(values, "chinese_handwriting")
	case SubjectEnglish:
		values = append(values, "latin_handwriting")
	case SubjectMathematics:
		values = append(values, "math_expression")
	case SubjectPhysics:
		values = append(values, "math_expression", "diagram")
	case SubjectChemistry:
		values = append(values, "chemical_equation", "math_expression")
	case SubjectBiology:
		values = append(values, "diagram", "table")
	case SubjectGeography:
		values = append(values, "map", "diagram", "table")
	}
	return values
}

func subjectEvidenceTypes(subject SubjectCode) []EvidenceType {
	base := []EvidenceType{EvidenceSelectedOption, EvidenceExactText, EvidenceTextSpan}
	switch subject {
	case SubjectChinese, SubjectEnglish, SubjectHistory, SubjectEthicsPolitics:
		return append(base, EvidenceConcept, EvidenceRelation)
	case SubjectMathematics:
		return append(base, EvidenceNumericValue, EvidenceMathExpression, EvidenceMathStep, EvidenceUnitValue, EvidenceDiagramFeature)
	case SubjectPhysics:
		return append(base, EvidenceNumericValue, EvidenceMathExpression, EvidenceMathStep, EvidenceUnitValue, EvidenceDiagramFeature, EvidenceTableCell)
	case SubjectChemistry:
		return append(base, EvidenceNumericValue, EvidenceMathExpression, EvidenceMathStep, EvidenceUnitValue, EvidenceChemicalEquation, EvidenceConcept, EvidenceRelation, EvidenceDiagramFeature, EvidenceTableCell)
	case SubjectBiology, SubjectGeography:
		return append(base, EvidenceNumericValue, EvidenceUnitValue, EvidenceConcept, EvidenceRelation, EvidenceDiagramFeature, EvidenceTableCell)
	default:
		return base
	}
}

func evidenceNames(values []EvidenceType) []any {
	out := make([]any, len(values))
	for index, value := range values {
		out[index] = string(value)
	}
	return out
}
