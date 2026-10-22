package paper

import (
	"net/http"
	"strings"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/httpx"
)

const objectiveGridSchema = "answer-sheet-objective-grid-v1"

// ObjectiveGridInput 的大框和图像共享来源显示页坐标；这里只生成建议，不写入题号或选项映射。
type ObjectiveGridInput struct {
	ExpectedRevision     int                  `json:"expected_revision"`
	ExpectedSourceSHA256 string               `json:"expected_source_sha256"`
	PageNo               int                  `json:"page_no"`
	Width                int                  `json:"width"`
	Height               int                  `json:"height"`
	MediaType            string               `json:"media_type"`
	DataBase64           string               `json:"data_base64"`
	SHA256               string               `json:"sha256"`
	BlockBBox            TemplateAnalysisBBox `json:"block_bbox"`
	OptionCount          int                  `json:"option_count"`
	OptionOrientation    string               `json:"option_orientation"`
}

type ObjectiveGridOption struct {
	BBox TemplateAnalysisBBox `json:"bbox"`
}

type ObjectiveGridGroup struct {
	GroupID     string                `json:"group_id"`
	BBox        TemplateAnalysisBBox  `json:"bbox"`
	Options     []ObjectiveGridOption `json:"options"`
	ReasonCodes []string              `json:"reason_codes"`
}

type ObjectiveGridAnalysis struct {
	RequestID       string               `json:"request_id"`
	SchemaVersion   string               `json:"schema_version"`
	Status          string               `json:"status"`
	CoordinateSpace string               `json:"coordinate_space"`
	PageNo          int                  `json:"page_no"`
	SourceSHA256    string               `json:"source_sha256"`
	BlockBBox       TemplateAnalysisBBox `json:"block_bbox"`
	Groups          []ObjectiveGridGroup `json:"groups"`
	ReasonCodes     []string             `json:"reason_codes"`
	Versions        map[string]string    `json:"versions"`
}

type ObjectiveGridResponse struct {
	TemplateID       string                `json:"template_id"`
	TemplateRevision int                   `json:"template_revision"`
	SourceSHA256     string                `json:"source_sha256"`
	Analysis         ObjectiveGridAnalysis `json:"analysis"`
}

func (input ObjectiveGridInput) pageInput() TemplatePageAnalysisInput {
	return TemplatePageAnalysisInput{
		ExpectedRevision: input.ExpectedRevision, ExpectedSourceSHA256: input.ExpectedSourceSHA256,
		PageNo: input.PageNo, Width: input.Width, Height: input.Height, MediaType: input.MediaType,
		DataBase64: input.DataBase64, SHA256: input.SHA256, WritingState: "unknown",
	}
}

// AnalyzeObjectiveGrid 与多模态识别独立；学校未分配视觉模型时仍可提取人工大框内的实测小格。
// 结果不保存模板或授予 OMR 自动确认资格，调用方必须人工核对题号及选项顺序后再保存草稿。
func (h *Handler) AnalyzeObjectiveGrid(w http.ResponseWriter, r *http.Request) {
	if h.documentImport == nil || h.questionRubricExams == nil {
		httpx.Error(w, r, http.StatusServiceUnavailable, "objective_grid_unavailable", "选择题网格解析服务暂不可用")
		return
	}
	var input ObjectiveGridInput
	r.Body = http.MaxBytesReader(w, r.Body, 2<<20)
	if !decodeJSON(w, r, &input) {
		return
	}
	page := input.pageInput()
	if validateTemplatePageAnalysisInput(page) != nil || validateTemplateAnalysisBBox(input.BlockBBox) != nil ||
		input.BlockBBox.X+input.BlockBBox.Width > 1+1e-9 || input.BlockBBox.Y+input.BlockBBox.Height > 1+1e-9 ||
		input.OptionCount < 2 || input.OptionCount > 12 || input.OptionOrientation != "horizontal" && input.OptionOrientation != "vertical" {
		writeStoreError(w, r, ErrInvalidInput)
		return
	}
	user := mustUser(r)
	scope, ok := auth.AccessScopeFromContext(r.Context())
	if !ok || scope.TenantID != user.TenantID || scope.ActorID != user.ID {
		httpx.Error(w, r, http.StatusForbidden, "access_scope_forbidden", "考试不在当前访问范围内")
		return
	}
	current, source, _, signature, err := h.templateAnalysisSnapshot(r.Context(), scope, r.PathValue("id"), page)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	result, err := h.documentImport.analyzeObjectiveGrid(r.Context(), input)
	if err != nil {
		httpx.Error(w, r, http.StatusBadGateway, "objective_grid_analysis_failed", "选择题网格解析未完成，请重试或人工框选")
		return
	}
	// 解析期间允许教师换图、改框或考试状态变化；旧快照的候选不能混入当前版本。
	latest, latestSource, _, latestSignature, err := h.templateAnalysisSnapshot(r.Context(), scope, current.ID, page)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	if latest.ContentHash != current.ContentHash || latestSource.File.HashSHA256 != source.File.HashSHA256 || latestSignature != signature {
		writeStoreError(w, r, ErrConflict)
		return
	}
	if validateObjectiveGridResult(result, input) != nil {
		httpx.Error(w, r, http.StatusBadGateway, "objective_grid_analysis_invalid", "选择题网格候选未通过校验，请重试或人工框选")
		return
	}
	h.auditAction(r, "paper.objective_grid_analyzed", "answer_sheet_template", current.ID, "detect measured option geometry within a manually selected block; mapping requires review")
	httpx.JSON(w, http.StatusOK, ObjectiveGridResponse{TemplateID: current.ID, TemplateRevision: current.Revision,
		SourceSHA256: source.File.HashSHA256, Analysis: result})
}

func validateObjectiveGridResult(result ObjectiveGridAnalysis, input ObjectiveGridInput) error {
	if result.SchemaVersion != objectiveGridSchema || result.PageNo != input.PageNo || result.Status != "review_required" ||
		result.CoordinateSpace != "source_display_normalized" || result.SourceSHA256 != input.SHA256 ||
		result.BlockBBox != input.BlockBBox || result.Groups == nil || len(result.Groups) > 500 || result.ReasonCodes == nil ||
		strings.TrimSpace(result.Versions["algorithm"]) == "" || len(result.Versions["algorithm"]) > 128 ||
		!validObjectiveGridReasonCodes(result.ReasonCodes) {
		return ErrInvalidInput
	}
	seen, optionCount := map[string]bool{}, 0
	for _, group := range result.Groups {
		if strings.TrimSpace(group.GroupID) == "" || len(group.GroupID) > 128 || seen[group.GroupID] ||
			!objectiveGridContains(input.BlockBBox, group.BBox) || len(group.Options) != input.OptionCount ||
			group.ReasonCodes == nil || !validObjectiveGridReasonCodes(group.ReasonCodes) {
			return ErrInvalidInput
		}
		seen[group.GroupID] = true
		seenOptions := map[TemplateAnalysisBBox]bool{}
		for _, option := range group.Options {
			if !objectiveGridContains(group.BBox, option.BBox) || seenOptions[option.BBox] {
				return ErrInvalidInput
			}
			seenOptions[option.BBox] = true
		}
		optionCount += len(group.Options)
	}
	if optionCount > 2000 {
		return ErrInvalidInput
	}
	return nil
}

func objectiveGridContains(outer, inner TemplateAnalysisBBox) bool {
	// 0～1 页坐标只容纳浮点运算余量，不放宽人工大框的实际边界。
	return validateTemplateAnalysisBBox(inner) == nil && inner.X >= outer.X-1e-6 && inner.Y >= outer.Y-1e-6 &&
		inner.X+inner.Width <= outer.X+outer.Width+1e-6 && inner.Y+inner.Height <= outer.Y+outer.Height+1e-6
}

func validObjectiveGridReasonCodes(codes []string) bool {
	if len(codes) > 32 {
		return false
	}
	for _, code := range codes {
		if len(code) < 1 || len(code) > 64 || code[0] < 'A' || code[0] > 'Z' {
			return false
		}
		for _, char := range code {
			if !(char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' || char == '_') {
				return false
			}
		}
	}
	return true
}
