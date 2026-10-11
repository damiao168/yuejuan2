package questionbank

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math"
	"strings"

	"edugrade-enterprise/services/api-gateway/internal/paper"
)

func emptyScoring() Scoring { return Scoring{Assets: []Asset{}, UsePolicy: "practice_only"} }
func bundleHash(content Content, scoring Scoring) string {
	// 将结构体转换为 map，以便排序全部对象键，包括嵌套答案 JSON。
	// 数组保持原有含义；SQL 使用相同的标准表示。
	raw, _ := json.Marshal(map[string]any{"schema_version": 2, "content": content, "scoring": scoring})
	var value any
	_ = json.Unmarshal(raw, &value)
	facts := value.(map[string]any)["scoring"].(map[string]any)
	// 模板和文件 ID 只记录来源；哈希保留评分内容与附件摘要，避免同内容因重新分配 ID 而改变。
	delete(facts, "template_version_id")
	for _, a := range facts["assets"].([]any) {
		delete(a.(map[string]any), "file_asset_id")
	}
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(value)
	sum := sha256.Sum256(bytes.TrimSuffix(b.Bytes(), []byte("\n")))
	return hex.EncodeToString(sum[:])
}
func copyScoring(s Scoring) Scoring {
	raw, _ := json.Marshal(s)
	var out Scoring
	_ = json.Unmarshal(raw, &out)
	return out
}
func scoreUnits(v float64) (int64, bool) {
	return int64(math.Round(v * 100)), !math.IsNaN(v) && !math.IsInf(v, 0) && v >= 0 && v <= 100000 && math.Abs(v*100-math.Round(v*100)) < 1e-8
}
func normalizeScoring(s Scoring) (Scoring, error) {
	// 按附件、答案、解析、评分细则依次处理副本；失败时保留当前处理结果，后续部分不再归一化。
	s = copyScoring(s)
	if s.UsePolicy == "" {
		s.UsePolicy = "practice_only"
	}
	if !oneOf(s.UsePolicy, "practice_only", "exam_allowed") || len(s.Assets) > 32 {
		return s, ErrInvalidInput
	}
	if s.Assets == nil {
		s.Assets = []Asset{}
	}
	if s.TemplateVersionID != nil && !validID(*s.TemplateVersionID) {
		return s, ErrInvalidInput
	}
	if err := validateScoringAssets(s.Assets); err != nil {
		return s, err
	}
	if err := normalizeScoringAnswer(s.Answer); err != nil {
		return s, err
	}
	if err := normalizeScoringSolution(s.Solution); err != nil {
		return s, err
	}
	if err := normalizeScoringRubric(s.Rubric); err != nil {
		return s, err
	}
	return s, nil
}

func validateScoringAssets(assets []Asset) error {
	seen := map[string]bool{}
	for _, a := range assets {
		if !validID(a.FileAssetID) || len(a.SHA256) != 64 || seen[a.FileAssetID] || !validText(a.Name, 500) || !validText(a.ContentType, 160) {
			return ErrInvalidInput
		}
		if _, e := hex.DecodeString(a.SHA256); e != nil {
			return ErrInvalidInput
		}
		seen[a.FileAssetID] = true
	}
	return nil
}

func normalizeScoringAnswer(answer *paper.AnswerKeyInput) error {
	if answer == nil {
		return nil
	}
	if answer.EquivalentAnswers == nil {
		answer.EquivalentAnswers = []any{}
	}
	if len(answer.EquivalentAnswers) > 64 {
		return ErrInvalidInput
	}
	// absolute 和 relative 分别表示绝对、相对容差，两者都必须是有限非负数。
	if answer.Tolerance == nil {
		answer.Tolerance = map[string]any{}
	}
	t, ok := answer.Tolerance.(map[string]any)
	if !ok {
		return ErrInvalidInput
	}
	for k, v := range t {
		n, ok := v.(float64)
		if (k != "absolute" && k != "relative") || !ok || math.IsNaN(n) || math.IsInf(n, 0) || n < 0 {
			return ErrInvalidInput
		}
	}
	return nil
}

func normalizeScoringSolution(solution *paper.SolutionInput) error {
	if solution == nil {
		return nil
	}
	if !validText(solution.RawText, 50000) || len(solution.Steps) > 128 || len(solution.SourceRefs) > 0 {
		return ErrInvalidInput
	}
	if solution.Steps == nil {
		solution.Steps = []paper.SolutionStep{}
	}
	solution.SourceRefs = []paper.PaperImportSourceRef{}
	for i, p := range solution.Steps {
		if p.StepNo != i+1 || strings.TrimSpace(p.Content) == "" || !validText(p.Content, 10000) {
			return ErrInvalidInput
		}
	}
	return nil
}

func normalizeScoringRubric(r *paper.RubricInput) error {
	if r == nil {
		return nil
	}
	// 这里的 approved 是评分事实状态；题库版本仍须经过 nextStatus 的审核与发布校验。
	r.Status = "approved"
	if r.Points == nil {
		r.Points = []paper.RubricPoint{}
	}
	if r.Deductions == nil {
		r.Deductions = []any{}
	}
	if r.Examples == nil {
		r.Examples = []any{}
	}
	if _, ok := scoreUnits(r.MaxScore); !ok || r.MaxScore <= 0 || len(r.Points) > 128 || len(r.Deductions) > 128 || len(r.Examples) > 128 || !paper.ValidRubricEvidenceRequirements(r.Points) {
		return ErrInvalidInput
	}
	seen := map[string]bool{}
	for _, p := range r.Points {
		if strings.TrimSpace(p.ID) == "" || seen[p.ID] || !validText(p.ID, 80) || strings.TrimSpace(p.Description) == "" || !validText(p.Description, 10000) {
			return ErrInvalidInput
		}
		if _, ok := scoreUnits(p.Score); !ok || p.Score <= 0 {
			return ErrInvalidInput
		}
		seen[p.ID] = true
	}
	return nil
}

func publishable(v Version, kind string) error {
	if v.Metadata.GradeScope == "unmapped" || v.Metadata.Copyright == "unknown" || v.Scoring.UsePolicy != "exam_allowed" {
		return ErrInvalidInput
	}
	if kind == "rubric_template" {
		if v.Scoring.Rubric == nil {
			return ErrInvalidInput
		}
		return validateRubric(v)
	}
	if err := validatePublishableAnswers(v); err != nil {
		return err
	}
	a := v.Scoring.Answer
	if a != nil && v.AssessmentArchetype != "numeric_expression" {
		t, _ := a.Tolerance.(map[string]any)
		if len(t) > 0 {
			return ErrInvalidInput
		}
	}
	if v.Scoring.Rubric != nil {
		return validateRubric(v)
	}
	return nil
}

func validatePublishableAnswers(v Version) error {
	if !oneOf(v.AssessmentArchetype, "selected_response", "exact_text", "numeric_expression") {
		if v.Scoring.Rubric == nil {
			return ErrInvalidInput
		}
		return nil
	}
	a := v.Scoring.Answer
	if a == nil || a.StandardAnswer == nil {
		return ErrInvalidInput
	}
	// 标准答案与每个等价答案都要按当前题型校验，不能只校验其中一条。
	answers := append([]any{a.StandardAnswer}, a.EquivalentAnswers...)
	for _, answer := range answers {
		if err := validatePublishableAnswer(v, answer); err != nil {
			return err
		}
	}
	return nil
}

func validatePublishableAnswer(v Version, answer any) error {
	switch v.AssessmentArchetype {
	case "selected_response":
		return validateSelectedResponseAnswer(v.QuestionType, len(v.Options), answer)
	case "exact_text":
		text, ok := answer.(string)
		if !ok || strings.TrimSpace(text) == "" {
			return ErrInvalidInput
		}
		return nil
	default:
		return validateNumericAnswer(answer)
	}
}

func validateSelectedResponseAnswer(questionType string, optionCount int, answer any) error {
	switch questionType {
	case "true_false":
		if _, ok := answer.(bool); !ok {
			return ErrInvalidInput
		}
	case "multiple_choice":
		list, ok := answer.([]any)
		if !ok || len(list) == 0 {
			return ErrInvalidInput
		}
		seen := map[string]bool{}
		for _, x := range list {
			if !validSelectedOption(x, optionCount) || seen[x.(string)] {
				return ErrInvalidInput
			}
			seen[x.(string)] = true
		}
	default:
		if !validSelectedOption(answer, optionCount) {
			return ErrInvalidInput
		}
	}
	return nil
}

func validSelectedOption(value any, optionCount int) bool {
	text, ok := value.(string)
	return ok && len(text) == 1 && text[0] >= 'A' && int(text[0]-'A') < optionCount
}

func validateNumericAnswer(answer any) error {
	switch x := answer.(type) {
	case float64:
		if math.IsNaN(x) || math.IsInf(x, 0) {
			return ErrInvalidInput
		}
	case string:
		if strings.TrimSpace(x) == "" {
			return ErrInvalidInput
		}
	default:
		return ErrInvalidInput
	}
	return nil
}

func validateRubric(v Version) error {
	// 用百分之一分的整数比较，要求评分点合计、Rubric 满分和题目默认分值完全一致。
	r := v.Scoring.Rubric
	max, _ := scoreUnits(r.MaxScore)
	score, _ := scoreUnits(v.DefaultScore)
	sum := int64(0)
	for _, p := range r.Points {
		u, _ := scoreUnits(p.Score)
		sum += u
	}
	if max != score || sum != max {
		return ErrInvalidInput
	}
	return nil
}
func transitionAction(decision string, author bool) string {
	switch decision {
	case "approve":
		return "review"
	case "publish":
		return "publish"
	case "return-to-draft":
		if !author {
			return "review"
		}
	}
	return "edit"
}
func nextStatus(v Version, kind, actor, decision string, in ReviewInput) (string, error) {
	// 修订号和整包哈希一起匹配，防止审核的是旧内容；作者也不能批准自己的版本。
	if in.ExpectedRevision != v.Revision || in.BundleHash != v.BundleHash {
		return "", ErrConflict
	}
	if !validText(in.Comment, 2000) {
		return "", ErrInvalidInput
	}
	switch decision {
	case "submit-review":
		if v.WorkflowStatus != "draft" {
			return "", ErrLocked
		}
		if err := publishable(v, kind); err != nil {
			return "", err
		}
		return "reviewing", nil
	case "approve":
		if actor == v.AuthorID {
			return "", ErrInvalidInput
		}
		if v.WorkflowStatus != "reviewing" {
			return "", ErrLocked
		}
		return "approved", nil
	case "return-to-draft":
		if !oneOf(v.WorkflowStatus, "reviewing", "approved") {
			return "", ErrLocked
		}
		return "draft", nil
	case "publish":
		if v.WorkflowStatus != "approved" {
			return "", ErrLocked
		}
		if err := publishable(v, kind); err != nil {
			return "", err
		}
		return "published", nil
	}
	return "", ErrInvalidInput
}
