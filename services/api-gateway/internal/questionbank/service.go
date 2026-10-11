package questionbank

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math"
	"regexp"
	"strings"
	"unicode/utf8"

	"edugrade-enterprise/services/api-gateway/internal/assessment"
	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/paper"
	"github.com/google/uuid"
)

var codePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$`)

func validScope(s auth.AccessScope) bool { return s.TenantID != "" && s.ActorID != "" && !s.IsPlatform }
func validID(id string) bool             { _, err := uuid.Parse(id); return err == nil }
func cleanFilter(f Filter) Filter {
	if f.Limit <= 0 || f.Limit > 100 {
		f.Limit = 50
	}
	if f.Offset < 0 {
		f.Offset = 0
	}
	return f
}
func validText(value string, max int) bool {
	return utf8.RuneCountInString(value) <= max && !strings.ContainsRune(value, 0)
}
func normalizeBank(in CreateBankInput) (CreateBankInput, error) {
	in.Name = strings.TrimSpace(in.Name)
	in.Description = strings.TrimSpace(in.Description)
	if !validID(in.SchoolID) || in.Name == "" || !validText(in.Name, 160) || !validText(in.Description, 2000) {
		return in, ErrInvalidInput
	}
	return in, nil
}
func normalizeContent(in Content) (Content, error) {
	in.Stem = strings.TrimSpace(in.Stem)
	subject, ok := assessment.NormalizeSubjectCode(in.Metadata.SubjectCode)
	if !ok || !assessment.EducationStage(in.Metadata.EducationStage).Valid() || !paper.IsValidQuestionType(in.QuestionType) || !assessment.IsQuestionArchetype(in.AssessmentArchetype) {
		return in, ErrInvalidInput
	}
	if in.Stem == "" || !validText(in.Stem, 50000) || math.IsNaN(in.DefaultScore) || math.IsInf(in.DefaultScore, 0) || in.DefaultScore <= 0 || in.DefaultScore > 100000 || math.Abs(in.DefaultScore*100-math.Round(in.DefaultScore*100)) > 1e-8 {
		return in, ErrInvalidInput
	}
	in.DefaultScore = math.Round(in.DefaultScore*100) / 100
	in.Metadata.SubjectCode = string(subject)
	// 按原顺序规范化；失败时只返回已经处理的部分，后续默认值和拷贝不提前执行。
	if err := normalizeContentMetadata(&in.Metadata); err != nil {
		return in, err
	}
	if err := normalizeContentLists(&in); err != nil {
		return in, err
	}
	in.CustomMetadata = copyMetadataValues(in.CustomMetadata)
	return in, nil
}

func normalizeContentMetadata(in *Metadata) error {
	in.GradeScope = strings.TrimSpace(in.GradeScope)
	if in.GradeScope == "" || !validText(in.GradeScope, 80) {
		return ErrInvalidInput
	}
	if in.DifficultyBand == "" {
		in.DifficultyBand = "unclassified"
	}
	if in.CognitiveLevel == "" {
		in.CognitiveLevel = "unclassified"
	}
	if in.Copyright == "" {
		in.Copyright = "unknown"
	}
	if in.Language == "" {
		in.Language = "zh-CN"
	}
	if in.IntendedUse == "" {
		in.IntendedUse = "practice"
	}
	if !oneOf(in.DifficultyBand, "unclassified", "easy", "medium", "hard") || !oneOf(in.CognitiveLevel, "unclassified", "remember", "understand", "apply", "analyze", "evaluate", "create") || !oneOf(in.Copyright, "unknown", "owned", "licensed") || !oneOf(in.Language, "zh-CN", "en") || !oneOf(in.IntendedUse, "practice", "homework", "quiz", "exam", "mock_exam") {
		return ErrInvalidInput
	}
	if in.SuggestedTimeMinutes != nil && (*in.SuggestedTimeMinutes < 1 || *in.SuggestedTimeMinutes > 1440) || in.SourceYear != nil && (*in.SourceYear < 1900 || *in.SourceYear > 2200) {
		return ErrInvalidInput
	}
	return nil
}

func normalizeContentLists(in *Content) error {
	if len(in.Options) > 32 || len(in.KnowledgePoints) > 64 {
		return ErrInvalidInput
	}
	// 复制数组，避免调用方或内存夹具改变已存储事实。
	in.Options = append([]string{}, in.Options...)
	in.KnowledgePoints = append([]string{}, in.KnowledgePoints...)
	for _, option := range in.Options {
		if strings.TrimSpace(option) == "" || !validText(option, 10000) {
			return ErrInvalidInput
		}
	}
	seen := map[string]bool{}
	for i, point := range in.KnowledgePoints {
		point = strings.TrimSpace(point)
		if point == "" || !validText(point, 120) || seen[point] {
			return ErrInvalidInput
		}
		in.KnowledgePoints[i] = point
		seen[point] = true
	}
	return nil
}
func oneOf(value string, values ...string) bool {
	for _, candidate := range values {
		if value == candidate {
			return true
		}
	}
	return false
}
func contentHash(in Content) string {
	// 这只是草稿内容哈希，绝不是未来发布的评分资料包。
	raw, _ := json.Marshal(struct {
		SchemaVersion int     `json:"schema_version"`
		ScoreUnits    int64   `json:"score_units"`
		Content       Content `json:"content"`
	}{1, int64(math.Round(in.DefaultScore * 100)), in})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
