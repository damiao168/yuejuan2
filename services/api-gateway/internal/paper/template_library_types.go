package paper

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/auth"
)

// TemplateLibraryRegion 用印刷题号连接新考试，不携带旧考试的 question_id。
type TemplateLibraryRegion struct {
	LayoutRegion
	QuestionNo   string `json:"question_no"`
	QuestionType string `json:"question_type"`
}

type TemplateLibraryPage struct {
	PageNo            int                           `json:"page_no"`
	Width             int                           `json:"width"`
	Height            int                           `json:"height"`
	RegistrationMarks []LayoutRegion                `json:"registration_marks"`
	IdentityRegions   []LayoutRegion                `json:"identity_regions"`
	QuestionRegions   []TemplateLibraryRegion       `json:"question_regions"`
	GradingGroups     []TemplateLibraryGradingGroup `json:"grading_groups,omitempty"`
}

// TemplateLibraryGradingGroup 仅保存印刷题号，实例化时映射到新考试；不能沿用旧考试题目 ID。
type TemplateLibraryGradingGroup struct {
	GroupID     string             `json:"group_id"`
	QuestionNos []string           `json:"question_nos"`
	BBox        TemplateRegionBBox `json:"bbox"`
}

type TemplateLibraryLayout struct {
	Pages []TemplateLibraryPage `json:"pages"`
}

// AnswerSheetTemplateLibraryVersion 是学校内不可修改的几何快照。
// 人工声明只表示核对事实，不表示服务端已自动验证图片没有学生信息。
type AnswerSheetTemplateLibraryVersion struct {
	ID           string                `json:"id"`
	TenantID     string                `json:"tenant_id"`
	SchoolID     string                `json:"school_id"`
	Name         string                `json:"name"`
	VersionNo    int                   `json:"version_no"`
	PageCount    int                   `json:"page_count"`
	Layout       TemplateLibraryLayout `json:"layout"`
	SourceSHA256 string                `json:"source_sha256"`
	ContentHash  string                `json:"content_hash"`
	ReviewedBy   string                `json:"reviewed_by"`
	ReviewedAt   time.Time             `json:"reviewed_at"`
}

type PublishTemplateToLibraryInput struct {
	Name                    string `json:"name"`
	ExpectedRevision        int    `json:"expected_revision"`
	ConfirmedBlankReference bool   `json:"confirmed_blank_reference"`
	ConfirmedReusableLayout bool   `json:"confirmed_reusable_layout"`
}

type ResolveTemplateLibraryInput struct {
	ExamPaperID string `json:"exam_paper_id"`
}

type TemplateLibraryInstance struct {
	Template AnswerSheetTemplate `json:"template"`
	Paper    Paper               `json:"paper"`
	Reused   bool                `json:"reused"`
}

type TemplateLibraryResolution struct {
	Status     string                              `json:"status"`
	Candidates []AnswerSheetTemplateLibraryVersion `json:"candidates"`
	Instance   *TemplateLibraryInstance            `json:"instance,omitempty"`
}

type TemplateLibraryRepository interface {
	ListTemplateLibrary(context.Context, auth.AccessScope, string) ([]AnswerSheetTemplateLibraryVersion, error)
	PublishTemplateToLibrary(context.Context, auth.AccessScope, string, PublishTemplateToLibraryInput) (AnswerSheetTemplateLibraryVersion, error)
	InstantiateTemplateLibrary(context.Context, auth.AccessScope, string, string) (TemplateLibraryInstance, error)
	ResolveTemplateLibrary(context.Context, auth.AccessScope, string, ResolveTemplateLibraryInput) (TemplateLibraryResolution, error)
	TemplateLibraryMatchSources(context.Context, auth.AccessScope, string, ResolveTemplateLibraryInput) (TemplateLibraryMatchSources, error)
}

const TemplateLibraryMatchCandidateLimit = 8

// TemplateLibraryMatchDocument 只供服务器读取对象，不得序列化到浏览器响应。
// Source 的 PageCount 为 0，表示尚未解码；匹配器必须读取原文件确认页数。
type TemplateLibraryMatchDocument struct {
	LibraryID    string
	PageCount    int
	SourceSHA256 string
	Layout       TemplateLibraryLayout
	File         FileAssetInput `json:"-"`
}

type TemplateLibraryMatchSources struct {
	Source     TemplateLibraryMatchDocument
	Candidates []TemplateLibraryMatchDocument
	Complete   bool
}

type TemplateLibraryVisualMatch struct {
	Status     string
	LibraryIDs []string
}

// TemplateLibraryVisualMatcher 只提供几何匹配证据；发布和本场绑定仍由领域门禁决定。
type TemplateLibraryVisualMatcher interface {
	MatchTemplateLibrary(context.Context, TemplateLibraryMatchSources) (TemplateLibraryVisualMatch, error)
}

type templateLibrarySource struct {
	TemplateID string
	Revision   int
	File       FileAssetInput
}

func copyLibraryValue[T any](value T) T {
	raw, _ := json.Marshal(value)
	var out T
	_ = json.Unmarshal(raw, &out)
	return out
}

func libraryPublishInputValid(input PublishTemplateToLibraryInput) bool {
	return strings.TrimSpace(input.Name) != "" && len([]rune(input.Name)) <= 200 && input.ExpectedRevision > 0 && input.ConfirmedBlankReference && input.ConfirmedReusableLayout
}

func newLibraryLayout(source AnswerSheetTemplate, questions []Question) (TemplateLibraryLayout, error) {
	source.Layout = copyLibraryValue(source.Layout)
	if err := validateTemplateReviewResolved(source.Layout); err != nil {
		return TemplateLibraryLayout{}, err
	}
	if err := ValidateTemplateInput(source.Name, source.PageCount, source.Layout); err != nil {
		return TemplateLibraryLayout{}, ErrInvalidInput
	}
	if err := validateTemplateGradingGroups(source.Layout, questions); err != nil {
		return TemplateLibraryLayout{}, ErrInvalidInput
	}
	if !libraryRegionsDoNotConflict(source.Layout) {
		return TemplateLibraryLayout{}, ErrInvalidInput
	}
	byID := map[string]Question{}
	byNumber := map[string]bool{}
	for _, q := range questions {
		number := strings.TrimSpace(q.QuestionNo)
		if q.Status == "deleted" {
			continue
		}
		if number == "" || byNumber[number] {
			return TemplateLibraryLayout{}, ErrInvalidInput
		}
		byNumber[number] = true
		byID[q.ID] = q
	}
	out := TemplateLibraryLayout{Pages: []TemplateLibraryPage{}}
	used := map[string]bool{}
	for _, page := range source.Layout.Pages {
		p := TemplateLibraryPage{PageNo: page.PageNo, Width: page.Width, Height: page.Height, RegistrationMarks: cleanLibraryRegions(page.RegistrationMarks, "registration"), IdentityRegions: cleanLibraryRegions(page.IdentityRegions, "identity"), QuestionRegions: []TemplateLibraryRegion{}}
		for _, r := range page.QuestionRegions {
			q, ok := byID[r.QuestionID]
			if !ok || used[q.ID] {
				return TemplateLibraryLayout{}, ErrInvalidInput
			}
			used[q.ID] = true
			r.QuestionID = ""
			r.ID = "question-" + strings.TrimSpace(q.QuestionNo)
			for i := range r.OptionRegions {
				r.OptionRegions[i].ID = "option-" + r.OptionRegions[i].Label
			}
			r.SuggestionConfidence = 0
			r.SuggestionSource = ""
			markLibraryRegion(&r)
			p.QuestionRegions = append(p.QuestionRegions, TemplateLibraryRegion{LayoutRegion: r, QuestionNo: strings.TrimSpace(q.QuestionNo), QuestionType: q.QuestionType})
		}
		for _, group := range page.GradingGroups {
			entry := TemplateLibraryGradingGroup{GroupID: group.GroupID, BBox: group.BBox}
			for _, id := range group.QuestionIDs {
				q, ok := byID[id]
				if !ok || q.QuestionType != "fill_blank" {
					return TemplateLibraryLayout{}, ErrInvalidInput
				}
				entry.QuestionNos = append(entry.QuestionNos, strings.TrimSpace(q.QuestionNo))
				used[id] = true
			}
			p.GradingGroups = append(p.GradingGroups, entry)
		}
		out.Pages = append(out.Pages, p)
	}
	// 缺单题 ROI 可由独立整区覆盖，但不能把完全未映射的题目收录成全套模板。
	if len(used) == 0 || len(used) != len(byID) {
		return TemplateLibraryLayout{}, ErrInvalidInput
	}
	return out, nil
}

func cleanLibraryRegions(regions []LayoutRegion, prefix string) []LayoutRegion {
	out := copyLibraryValue(regions)
	for i := range out {
		out[i].ID = fmt.Sprintf("%s-%d", prefix, i+1)
		out[i].QuestionID = ""
		out[i].SuggestionConfidence = 0
		out[i].SuggestionSource = ""
		markLibraryRegion(&out[i])
	}
	return out
}

// 库保留已收录的几何，但不伪造本场人工确认或新的识别证据；实例化仍须匹配正式题号、题型和覆盖范围。
func markLibraryRegion(region *LayoutRegion) {
	region.Origin, region.ReviewState = "library", "suggested"
	region.SemanticCheck, region.AnalysisRunID, region.ReasonCodes = "not_evaluated", "", nil
}

func instantiateLibraryLayout(item AnswerSheetTemplateLibraryVersion, questions []Question) (TemplateLayout, error) {
	byNumber := map[string]Question{}
	for _, q := range questions {
		if q.Status == "deleted" {
			continue
		}
		no := strings.TrimSpace(q.QuestionNo)
		if no == "" || byNumber[no].ID != "" {
			return TemplateLayout{}, ErrInvalidInput
		}
		byNumber[no] = q
	}
	layout := TemplateLayout{Pages: []TemplatePage{}}
	used := map[string]bool{}
	for _, p := range item.Layout.Pages {
		page := TemplatePage{PageNo: p.PageNo, Width: p.Width, Height: p.Height, RegistrationMarks: copyLibraryValue(p.RegistrationMarks), IdentityRegions: copyLibraryValue(p.IdentityRegions), QuestionRegions: []LayoutRegion{}}
		for _, slot := range p.QuestionRegions {
			question := byNumber[slot.QuestionNo]
			id := question.ID
			if id == "" || used[id] || question.QuestionType != slot.QuestionType {
				return TemplateLayout{}, ErrInvalidInput
			}
			used[id] = true
			r := copyLibraryValue(slot.LayoutRegion)
			r.QuestionID = id
			page.QuestionRegions = append(page.QuestionRegions, r)
		}
		for _, group := range p.GradingGroups {
			entry := TemplateGradingGroup{GroupID: group.GroupID, BBox: group.BBox}
			for _, no := range group.QuestionNos {
				question := byNumber[no]
				if question.ID == "" || question.QuestionType != "fill_blank" {
					return TemplateLayout{}, ErrInvalidInput
				}
				entry.QuestionIDs = append(entry.QuestionIDs, question.ID)
				used[question.ID] = true
			}
			page.GradingGroups = append(page.GradingGroups, entry)
		}
		layout.Pages = append(layout.Pages, page)
	}
	if len(used) == 0 || len(used) != len(byNumber) {
		return TemplateLayout{}, ErrInvalidInput
	}
	// 新考试没有继承旧校准证据；只恢复保守的人工确认提取配置。
	layout = NormalizeTemplateLayout(layout)
	if err := ValidateTemplateInput(item.Name, item.PageCount, layout); err != nil {
		return TemplateLayout{}, ErrInvalidInput
	}
	if err := validateTemplateGradingGroups(layout, questions); err != nil {
		return TemplateLayout{}, ErrInvalidInput
	}
	if !libraryRegionsDoNotConflict(layout) {
		return TemplateLayout{}, ErrInvalidInput
	}
	return layout, nil
}

// 库会影响后续整批学生；题区不能共享有效面积，也不能覆盖身份区。
// 仅触边和 1e-6 的浮点误差不算冲突，选项与其母题的正常包含关系不参与比较。
func libraryRegionsDoNotConflict(layout TemplateLayout) bool {
	overlaps := func(a, b LayoutRegion) bool {
		return math.Min(a.X+a.Width, b.X+b.Width)-math.Max(a.X, b.X) > 1e-6 &&
			math.Min(a.Y+a.Height, b.Y+b.Height)-math.Max(a.Y, b.Y) > 1e-6
	}
	for _, page := range layout.Pages {
		for index, region := range page.QuestionRegions {
			for _, other := range page.QuestionRegions[index+1:] {
				if overlaps(region, other) {
					return false
				}
			}
			for _, identity := range page.IdentityRegions {
				if overlaps(region, identity) {
					return false
				}
			}
		}
	}
	return true
}
