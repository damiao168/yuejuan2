package capture

import (
	"context"
	"errors"
	"net/http"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/exam"
	"edugrade-enterprise/services/api-gateway/internal/httpx"
	"edugrade-enterprise/services/api-gateway/internal/paper"
)

// ScanTemplateReader 读取考试已绑定版本；不把其它草稿或模板目录返回给扫描员。
type ScanTemplateReader interface {
	GetExamTemplateBinding(context.Context, string, string) (paper.ExamTemplateBinding, error)
	ListTemplates(context.Context, string, string) ([]paper.AnswerSheetTemplate, error)
}

type ScanTemplateRegion struct {
	ID     string  `json:"id"`
	Label  string  `json:"label,omitempty"`
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
}
type ScanTemplatePage struct {
	PageNo            int                  `json:"page_no"`
	Width             int                  `json:"width"`
	Height            int                  `json:"height"`
	RegistrationMarks []ScanTemplateRegion `json:"registration_marks"`
	IdentityRegions   []ScanTemplateRegion `json:"identity_regions"`
	QuestionRegions   []ScanTemplateRegion `json:"question_regions"`
}
type ScanTemplate struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	VersionNo       int    `json:"version_no"`
	PageCount       int    `json:"page_count"`
	ContentHash     string `json:"content_hash"`
	BindingRevision int    `json:"binding_revision"`
	Layout          struct {
		Pages []ScanTemplatePage `json:"pages"`
	} `json:"layout"`
}
type ScanContext struct {
	Batch Batch `json:"batch"`
	Exam  struct {
		ID      string `json:"id"`
		Name    string `json:"name"`
		Subject string `json:"subject"`
	} `json:"exam"`
	TemplateStatus string        `json:"template_status"`
	TemplateIssue  string        `json:"template_issue,omitempty"`
	Template       *ScanTemplate `json:"template,omitempty"`
}

// GetScanContext 按获授权批次投影考试名和当前锁定模板。
// 布局坐标沿用模板像素坐标，不推算实际纸型或 DPI；上传后的模板匹配仍由处理门禁决定。
func (h *Handler) GetScanContext(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	batch, err := h.store.GetBatch(r.Context(), user.TenantID, r.PathValue("id"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	scope, ok := auth.AccessScopeFromContext(r.Context())
	if !ok || scope.TenantID != user.TenantID {
		httpx.Error(w, r, http.StatusForbidden, "access_scope_missing", "no valid data access scope is assigned")
		return
	}
	// 路由资源 guard 与这里的考试读取均应用服务端作用域；内存适配没有资源解析器时也不能越过考试边界。
	currentExam, err := h.exams.GetExam(r.Context(), scope, batch.ExamID)
	if err != nil {
		if errors.Is(err, exam.ErrNotFound) || errors.Is(err, exam.ErrScopeForbidden) {
			httpx.Error(w, r, http.StatusNotFound, "exam_not_found", "exam not found")
		} else {
			scanContextUnavailable(w, r)
		}
		return
	}
	out := ScanContext{Batch: batch, TemplateStatus: "missing", TemplateIssue: "not_bound"}
	out.Exam.ID, out.Exam.Name, out.Exam.Subject = currentExam.ID, currentExam.Name, currentExam.Subject
	if h.scanTemplates == nil {
		scanContextUnavailable(w, r)
		return
	}
	if err := h.populateScanTemplate(r.Context(), user.TenantID, batch.ExamID, &out); err != nil {
		scanContextUnavailable(w, r)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}

// 缺少或失效的模板仍返回扫描上下文；读取失败交回 HTTP 入口处理，不能伪装成未绑定。
func (h *Handler) populateScanTemplate(ctx context.Context, tenantID, examID string, out *ScanContext) error {
	binding, err := h.scanTemplates.GetExamTemplateBinding(ctx, tenantID, examID)
	if errors.Is(err, paper.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	out.TemplateStatus, out.TemplateIssue = "invalid", "binding_mismatch"
	if !validScanTemplateBinding(binding, tenantID, examID) {
		return nil
	}
	templates, err := h.scanTemplates.ListTemplates(ctx, tenantID, examID)
	if err != nil {
		return err
	}
	// 读取模板期间可能重绑；再次核对绑定代次，不能把旧模板说成当前已确认。
	latest, err := h.scanTemplates.GetExamTemplateBinding(ctx, tenantID, examID)
	if errors.Is(err, paper.ErrNotFound) {
		out.TemplateIssue = "binding_changed"
		return nil
	}
	if err != nil {
		return err
	}
	if !sameScanTemplateBinding(latest, binding) {
		out.TemplateIssue = "binding_changed"
		return nil
	}
	out.TemplateIssue = "template_missing"
	for _, template := range templates {
		if template.ID != binding.TemplateID {
			continue
		}
		projected, issue := projectScanTemplate(template, binding, tenantID, examID)
		if issue != "" {
			out.TemplateIssue = issue
			return nil
		}
		out.TemplateStatus, out.TemplateIssue, out.Template = "ready", "", projected
		return nil
	}
	return nil
}

func validScanTemplateBinding(binding paper.ExamTemplateBinding, tenantID, examID string) bool {
	return binding.TenantID == tenantID && binding.ExamID == examID && binding.TemplateID != "" && binding.TemplateContentHash != "" && binding.Revision >= 1 && binding.Mode == "locked_with_guard"
}

func sameScanTemplateBinding(latest, binding paper.ExamTemplateBinding) bool {
	return latest.TenantID == binding.TenantID && latest.ExamID == binding.ExamID && latest.Mode == binding.Mode && latest.Revision == binding.Revision && latest.TemplateID == binding.TemplateID && latest.TemplateContentHash == binding.TemplateContentHash
}

// 只投影当前绑定的锁定版本；任一页面尺寸无效时丢弃整个投影，避免返回半份可用布局。
func projectScanTemplate(template paper.AnswerSheetTemplate, binding paper.ExamTemplateBinding, tenantID, examID string) (*ScanTemplate, string) {
	if template.TenantID != tenantID || template.ExamID != examID || template.Status != "locked" || template.ContentHash != binding.TemplateContentHash {
		return nil, "template_version_mismatch"
	}
	if template.PageCount < 1 || len(template.Layout.Pages) != template.PageCount {
		return nil, "template_layout_invalid"
	}
	projected := &ScanTemplate{ID: template.ID, Name: template.Name, VersionNo: template.VersionNo, PageCount: template.PageCount, ContentHash: template.ContentHash, BindingRevision: binding.Revision}
	for _, page := range template.Layout.Pages {
		if page.Width <= 0 || page.Height <= 0 {
			return nil, "template_layout_invalid"
		}
		projected.Layout.Pages = append(projected.Layout.Pages, ScanTemplatePage{PageNo: page.PageNo, Width: page.Width, Height: page.Height, RegistrationMarks: scanRegions(page.RegistrationMarks), IdentityRegions: scanRegions(page.IdentityRegions), QuestionRegions: scanRegions(page.QuestionRegions)})
	}
	return projected, ""
}

func scanRegions(regions []paper.LayoutRegion) []ScanTemplateRegion {
	out := make([]ScanTemplateRegion, 0, len(regions))
	for _, region := range regions {
		out = append(out, ScanTemplateRegion{ID: region.ID, Label: region.Label, X: region.X, Y: region.Y, Width: region.Width, Height: region.Height})
	}
	return out
}
func scanContextUnavailable(w http.ResponseWriter, r *http.Request) {
	// 数据源失败与真正未绑定不同；客户端可同页重读，不能把故障当成无配置。
	httpx.Error(w, r, http.StatusServiceUnavailable, "capture_scan_context_unavailable", "capture template context cannot be read")
}
