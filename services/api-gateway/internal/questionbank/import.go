package questionbank

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"sort"
	"strings"

	"edugrade-enterprise/services/api-gateway/internal/paper"
)

type frozenImportSnapshot struct {
	Version           int                    `json:"version"`
	ConfigurationHash string                 `json:"configuration_hash"`
	Questions         []frozenImportQuestion `json:"questions"`
}

type frozenImportQuestion struct {
	ID                      string                `json:"id"`
	AssessmentSnapshotID    string                `json:"assessment_snapshot_id"`
	AssessmentSnapshotHash  string                `json:"assessment_snapshot_hash"`
	QuestionNo              string                `json:"question_no"`
	QuestionType            string                `json:"question_type"`
	AssessmentArchetype     string                `json:"assessment_archetype"`
	Score                   float64               `json:"score"`
	Stem                    string                `json:"stem"`
	KnowledgePoints         []string              `json:"knowledge_points"`
	SourceType              string                `json:"source_type,omitempty"`
	SourceBankItemID        string                `json:"source_bank_item_id,omitempty"`
	SourceBankItemVersionID string                `json:"source_bank_item_version_id,omitempty"`
	SourceContentHash       string                `json:"source_content_hash,omitempty"`
	BankContent             map[string]any        `json:"bank_content,omitempty"`
	Answer                  *paper.AnswerKeyInput `json:"answer,omitempty"`
	Solution                *frozenImportSolution `json:"solution,omitempty"`
	Rubric                  *paper.RubricInput    `json:"rubric,omitempty"`
}

type frozenImportSolution struct {
	RawText            string               `json:"raw_text"`
	Steps              []paper.SolutionStep `json:"steps"`
	VerificationStatus string               `json:"verification_status"`
}

type importAssessmentSnapshot struct {
	ID             string
	Version        int
	Profile        map[string]any
	Archetype      map[string]any
	Rubric         map[string]any
	ScoringPolicy  map[string]any
	ContentHash    string
	ArchetypeCode  string
	SubjectCode    string
	EducationStage string
}

type preparedImport struct {
	Content           Content
	Scoring           Scoring
	Source            ImportSource
	Issues            []ImportIssue
	Schema            MetadataSchema
	Bank              Bank
	ScoringConsistent bool
}

func hashJSON(value any) string {
	raw, _ := json.Marshal(value)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func frozenQuestion(snapshot frozenImportSnapshot, questionID string) (frozenImportQuestion, bool) {
	for _, question := range snapshot.Questions {
		if question.ID == questionID {
			return question, true
		}
	}
	return frozenImportQuestion{}, false
}

func sourceBankContent(question frozenImportQuestion) (Content, []Asset) {
	var content Content
	assets := []Asset{}
	if question.BankContent == nil {
		return content, assets
	}
	if raw, ok := question.BankContent["content"]; ok {
		encoded, _ := json.Marshal(raw)
		_ = json.Unmarshal(encoded, &content)
	}
	if raw, ok := question.BankContent["assets"]; ok {
		encoded, _ := json.Marshal(raw)
		_ = json.Unmarshal(encoded, &assets)
	}
	return content, assets
}

func buildImportedFacts(question frozenImportQuestion, assessment importAssessmentSnapshot, mapping ImportMapping) (Content, Scoring, error) {
	bankContent, assets := sourceBankContent(question)
	metadata := bankContent.Metadata
	metadata.SubjectCode = assessment.SubjectCode
	metadata.EducationStage = assessment.EducationStage
	// 历史快照可能没有题库所需的年级、版权等信息；保留缺失标记，导入后补全才能发布。
	if metadata.GradeScope == "" {
		metadata.GradeScope = "unmapped"
	}
	if metadata.DifficultyBand == "" {
		metadata.DifficultyBand = "unclassified"
	}
	if metadata.CognitiveLevel == "" {
		metadata.CognitiveLevel = "unclassified"
	}
	if metadata.Copyright == "" {
		metadata.Copyright = "unknown"
	}
	if metadata.Language == "" {
		metadata.Language = "zh-CN"
	}
	if metadata.IntendedUse == "" {
		metadata.IntendedUse = "exam"
	}
	for _, entry := range []struct {
		value  string
		target *string
	}{
		{mapping.GradeScope, &metadata.GradeScope}, {mapping.DifficultyBand, &metadata.DifficultyBand},
		{mapping.CognitiveLevel, &metadata.CognitiveLevel}, {mapping.Copyright, &metadata.Copyright},
		{mapping.Language, &metadata.Language}, {mapping.IntendedUse, &metadata.IntendedUse},
	} {
		if strings.TrimSpace(entry.value) != "" {
			*entry.target = entry.value
		}
	}
	options := append([]string{}, bankContent.Options...)
	if mapping.Options != nil {
		options = append([]string{}, mapping.Options...)
	}
	knowledgePoints := append([]string{}, question.KnowledgePoints...)
	if mapping.KnowledgePoints != nil {
		knowledgePoints = append([]string{}, mapping.KnowledgePoints...)
	}
	content, err := normalizeContent(Content{
		QuestionType: question.QuestionType, AssessmentArchetype: assessment.ArchetypeCode,
		Stem: question.Stem, Options: options, DefaultScore: question.Score,
		KnowledgePoints: knowledgePoints, Metadata: metadata,
		CustomMetadata: copyMetadataValues(mapping.CustomMetadata),
	})
	if err != nil {
		return Content{}, Scoring{}, err
	}
	scoring := Scoring{Answer: question.Answer, Rubric: question.Rubric, Assets: assets, UsePolicy: "exam_allowed"}
	if question.Solution != nil {
		scoring.Solution = &paper.SolutionInput{RawText: question.Solution.RawText, Steps: append([]paper.SolutionStep{}, question.Solution.Steps...), SourceRefs: []paper.PaperImportSourceRef{}}
	}
	scoring, err = normalizeScoring(scoring)
	return content, scoring, err
}

func assessmentRubric(value map[string]any) (*paper.RubricInput, error) {
	if len(value) == 0 {
		return nil, nil
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var rubric paper.RubricInput
	if err = json.Unmarshal(raw, &rubric); err != nil {
		return nil, err
	}
	normalized, err := normalizeScoring(Scoring{Rubric: &rubric, Assets: []Asset{}, UsePolicy: "exam_allowed"})
	if err != nil {
		return nil, err
	}
	return normalized.Rubric, nil
}

func scoringMatchesAssessment(question frozenImportQuestion, assessment importAssessmentSnapshot) bool {
	if question.AssessmentArchetype != assessment.ArchetypeCode {
		return false
	}
	assessmentFacts, err := assessmentRubric(assessment.Rubric)
	if err != nil {
		return false
	}
	readiness, err := normalizeScoring(Scoring{Rubric: question.Rubric, Assets: []Asset{}, UsePolicy: "exam_allowed"})
	if err != nil {
		return false
	}
	return reflect.DeepEqual(readiness.Rubric, assessmentFacts)
}

func importIssues(content Content, scoring Scoring, schema MetadataSchema, itemCode string) []ImportIssue {
	issues := []ImportIssue{}
	add := func(code, field, message string) {
		issues = append(issues, ImportIssue{Code: code, Field: field, Message: message, Blocking: true})
	}
	if strings.TrimSpace(itemCode) == "" {
		add("item_code_required", "mapping.item_code", "新建题目需填写目标题号编码")
	} else if !codePattern.MatchString(itemCode) {
		add("item_code_invalid", "mapping.item_code", "题号编码格式无效")
	}
	if content.Metadata.GradeScope == "unmapped" {
		add("grade_scope_unmapped", "mapping.grade_scope", "请映射目标年级范围")
	}
	if content.Metadata.Copyright == "unknown" {
		add("copyright_unknown", "mapping.copyright", "版权状态未知，发布前需确认")
	}
	validation := validateContentMetadata(schema, content, true)
	keys := make([]string, 0, len(validation.FieldErrors))
	for key := range validation.FieldErrors {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		for _, message := range validation.FieldErrors[key] {
			add("target_schema_mapping_required", key, message)
		}
	}
	copyForScoring := content
	// 版权问题上面已单独报告；临时副本跳过这一项，只用于检查答案和评分规则，不回写内容。
	if copyForScoring.Metadata.Copyright == "unknown" {
		copyForScoring.Metadata.Copyright = "owned"
	}
	if err := publishable(Version{Content: copyForScoring, Scoring: scoring}, "question"); err != nil {
		add("scoring_incomplete", "scoring", "答案、选项或 Rubric 尚不满足发布规则")
	}
	return issues
}

func suggestedImportCode(questionID string) string {
	compact := strings.ReplaceAll(questionID, "-", "")
	if len(compact) > 12 {
		compact = compact[:12]
	}
	return "HIST-" + strings.ToUpper(compact)
}

func importErrorCode(err error) (string, bool) {
	switch {
	case errors.Is(err, ErrSourceUnavailable):
		return "source_snapshot_unavailable", false
	case errors.Is(err, ErrNotFound):
		return "resource_not_found", false
	case errors.Is(err, ErrConflict):
		return "target_revision_conflict", true
	case errors.Is(err, ErrLocked):
		return "target_locked", true
	case errors.Is(err, ErrInvalidInput):
		return "invalid_import_input", false
	default:
		return "import_unavailable", true
	}
}
