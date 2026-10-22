package paper

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"strings"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/httpx"
	"github.com/google/uuid"
)

const templatePageAnalysisSchema = "answer-sheet-analysis-v1"
const maxTemplatePageImageBytes = 1 << 20
const maxTemplatePageAnalysisResponseBytes = 2 << 20

var errTemplateVisionModelRequired = errors.New("a school answer sheet vision model assignment is required")

type TemplateAnalysisBBox struct {
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
}

type TemplateAnalysisGeometry struct {
	CandidateID  string               `json:"candidate_id"`
	BBox         TemplateAnalysisBBox `json:"bbox"`
	EvidenceRefs []string             `json:"evidence_refs"`
}

// TemplatePageAnalysisInput 的图片是浏览器当前显示页的有界快照，hash 不等同原 PDF 的 hash。
// 通过语义检查的输出可由编辑器自动对应题目；正式来源及模板版本在推断前后分别核对。
type TemplatePageAnalysisInput struct {
	ExpectedRevision     int                        `json:"expected_revision"`
	ExpectedSourceSHA256 string                     `json:"expected_source_sha256"`
	PageNo               int                        `json:"page_no"`
	Width                int                        `json:"width"`
	Height               int                        `json:"height"`
	MediaType            string                     `json:"media_type"`
	DataBase64           string                     `json:"data_base64"`
	SHA256               string                     `json:"sha256"`
	Candidates           []TemplateAnalysisGeometry `json:"candidates"`
	WritingState         string                     `json:"writing_state"`
}

type TemplateAnalyzedRegion struct {
	PageNo               int                             `json:"page_no"`
	CandidateID          string                          `json:"candidate_id"`
	PrintedQuestionLabel *string                         `json:"printed_question_label"`
	IdentityLabel        *string                         `json:"identity_label,omitempty"`
	Role                 string                          `json:"role"`
	Origin               string                          `json:"origin,omitempty"`
	SourceBBox           TemplateAnalysisBBox            `json:"source_bbox"`
	ProposedBBox         TemplateAnalysisBBox            `json:"proposed_bbox"`
	AssociationStatus    string                          `json:"association_status"`
	EvidenceRefs         []string                        `json:"evidence_refs"`
	ReasonCodes          []string                        `json:"reason_codes"`
	SemanticCheck        string                          `json:"semantic_check,omitempty"`
	ObjectiveItems       []TemplateAnalyzedObjectiveItem `json:"objective_items,omitempty"`
}

// TemplateAnalyzedObjectiveItem 使用整页归一化坐标，题号来自正式选择题白名单。
// 模型明确识别到的题号和选项共同提供自动对应依据，不能按框的位置补造题号。
type TemplateAnalyzedObjectiveItem struct {
	PrintedQuestionLabel string                            `json:"printed_question_label"`
	BBox                 TemplateAnalysisBBox              `json:"bbox"`
	Options              []TemplateAnalyzedObjectiveOption `json:"options"`
}

type TemplateAnalyzedObjectiveOption struct {
	Label string               `json:"label"`
	BBox  TemplateAnalysisBBox `json:"bbox"`
}

type TemplatePageAnalysis struct {
	RequestID         string                        `json:"request_id"`
	SchemaVersion     string                        `json:"schema_version"`
	PageNo            int                           `json:"page_no"`
	Status            string                        `json:"status"`
	SemanticStatus    string                        `json:"semantic_status"`
	SemanticError     json.RawMessage               `json:"semantic_error"`
	CoordinateSpace   string                        `json:"coordinate_space"`
	SourceSHA256      string                        `json:"source_sha256"`
	Candidates        []TemplateAnalyzedRegion      `json:"candidates"`
	Registration      *TemplateAnalysisRegistration `json:"registration,omitempty"`
	FillGradingGroups []TemplateAnalysisFillGroup   `json:"fill_grading_groups,omitempty"`
	ReasonCodes       []string                      `json:"reason_codes"`
	ModelIdentity     map[string]string             `json:"model_identity"`
	Versions          map[string]string             `json:"versions"`
	ModelUsage        json.RawMessage               `json:"model_usage"`
}

type TemplatePageAnalysisResponse struct {
	TemplateID       string               `json:"template_id"`
	TemplateRevision int                  `json:"template_revision"`
	SourceSHA256     string               `json:"source_sha256"`
	Analysis         TemplatePageAnalysis `json:"analysis"`
}

type templateAnalysisReader interface {
	ReadTemplateForAnalysis(context.Context, string, string) (AnswerSheetTemplate, error)
}

type templateAnalysisQuestionLabels struct {
	All       []string
	Objective []string
	FillBlank []string
}

type templateAnalysisDiagnosticContextKey struct{}

// templateAnalysisDiagnosticContext 只从已鉴权的来源、模板和正式题目快照构造。
// 它仅用于工作节点受控诊断，不能由浏览器提交，也不能成为模型提示词中的学生信息。
type templateAnalysisDiagnosticContext struct {
	TemplateID             string `json:"template_id"`
	TemplateRevision       int    `json:"template_revision"`
	SourcePaperID          string `json:"source_paper_id,omitempty"`
	SourcePDFSHA256        string `json:"source_pdf_sha256"`
	QuestionSnapshotSHA256 string `json:"question_snapshot_sha256"`
}

func (s *MemoryStore) ReadTemplateForAnalysis(_ context.Context, tenantID, id string) (AnswerSheetTemplate, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	item, ok := s.templates[id]
	if !ok || item.TenantID != tenantID {
		return AnswerSheetTemplate{}, ErrNotFound
	}
	return cloneTemplateSnapshot(item), nil
}

func (s *PostgresStore) ReadTemplateForAnalysis(ctx context.Context, tenantID, id string) (AnswerSheetTemplate, error) {
	var item AnswerSheetTemplate
	err := scanTemplate(s.db.QueryRowContext(ctx, `SELECT id::text,tenant_id::text,exam_id::text,exam_paper_id::text,
version_no,revision,name,status,page_count,layout,content_hash,created_by::text,
COALESCE(locked_by::text,''),locked_at,created_at,updated_at
FROM answer_sheet_template WHERE tenant_id=$1::uuid AND id=$2::uuid AND deleted_at IS NULL`, tenantID, id), &item)
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrNotFound
	}
	return item, err
}

// AnalyzeTemplatePage 不保存或锁定模板。学校先由平台管理员分配多模态模型；
// 语义失败只能交回未关联几何候选，不能用本地 OCR 的猜测冒充已经完成多模态识别。
func (h *Handler) AnalyzeTemplatePage(w http.ResponseWriter, r *http.Request) {
	if h.documentImport == nil || h.questionRubricExams == nil {
		httpx.Error(w, r, http.StatusServiceUnavailable, "template_analysis_unavailable", "答题卡后台分析暂不可用，可继续核对本地候选")
		return
	}
	var input TemplatePageAnalysisInput
	r.Body = http.MaxBytesReader(w, r.Body, 2<<20)
	if !decodeJSON(w, r, &input) {
		return
	}
	if validateTemplatePageAnalysisInput(input) != nil {
		writeStoreError(w, r, ErrInvalidInput)
		return
	}
	user := mustUser(r)
	scope, ok := auth.AccessScopeFromContext(r.Context())
	if !ok || scope.TenantID != user.TenantID || scope.ActorID != user.ID {
		httpx.Error(w, r, http.StatusForbidden, "access_scope_forbidden", "考试不在当前访问范围内")
		return
	}
	current, source, labels, signature, err := h.templateAnalysisSnapshot(r.Context(), scope, r.PathValue("id"), input)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	analysisCtx := context.WithValue(r.Context(), templateAnalysisDiagnosticContextKey{}, templateAnalysisDiagnosticContext{
		TemplateID: current.ID, TemplateRevision: current.Revision, SourcePaperID: source.ID,
		SourcePDFSHA256: source.File.HashSHA256, QuestionSnapshotSHA256: signature,
	})
	result, err := h.documentImport.analyzeTemplatePage(analysisCtx, scope.TenantID, input, labels.All, labels.Objective, labels.FillBlank)
	if err != nil {
		if errors.Is(err, errTemplateVisionModelRequired) {
			httpx.Error(w, r, http.StatusServiceUnavailable, "template_vision_model_required", "尚未配置可用的答题卡多模态模型，请平台管理员在学校模型库分配答题卡识别用途")
			return
		}
		httpx.Error(w, r, http.StatusBadGateway, "template_analysis_failed", "后台分析未完成，已保留本地候选供人工核对")
		return
	}
	// 图片推断可能跨越另一位教师的改框、换底图或考试冻结，迟到结果不能被当成当前建议。
	latest, latestSource, _, latestSignature, err := h.templateAnalysisSnapshot(r.Context(), scope, current.ID, input)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	if latest.ContentHash != current.ContentHash || latestSource.File.HashSHA256 != source.File.HashSHA256 || latestSignature != signature {
		writeStoreError(w, r, ErrConflict)
		return
	}
	// 来源和核对状态先检查原始响应，避免后续降级把本地检测伪报的关联成功掩盖掉。
	for _, candidate := range result.Candidates {
		if validateTemplateCandidateOrigin(candidate, labels.FillBlank) != nil {
			httpx.Error(w, r, http.StatusBadGateway, "template_analysis_invalid", "后台候选未通过校验，请核对本地候选")
			return
		}
	}
	// 内部工作节点也可能返回矛盾类别。先保留原因并降为待核对，不能清理字段后丢失矛盾证据。
	defendTemplatePageSemantics(&result, labels)
	if err := validateTemplatePageAnalysisResult(result, input, labels.All, labels.FillBlank); err != nil {
		httpx.Error(w, r, http.StatusBadGateway, "template_analysis_invalid", "后台候选未通过校验，请核对本地候选")
		return
	}
	h.auditAction(r, "paper.template_page_analyzed", "answer_sheet_template", current.ID, "analyze page geometry and question labels; supported results can be matched automatically")
	httpx.JSON(w, http.StatusOK, TemplatePageAnalysisResponse{
		TemplateID: current.ID, TemplateRevision: current.Revision, SourceSHA256: source.File.HashSHA256, Analysis: result,
	})
}

func (h *Handler) templateAnalysisSnapshot(ctx context.Context, scope auth.AccessScope, id string, input TemplatePageAnalysisInput) (AnswerSheetTemplate, Paper, templateAnalysisQuestionLabels, string, error) {
	reader, ok := h.templates.(templateAnalysisReader)
	if !ok {
		return AnswerSheetTemplate{}, Paper{}, templateAnalysisQuestionLabels{}, "", ErrInvalidInput
	}
	current, err := reader.ReadTemplateForAnalysis(ctx, scope.TenantID, id)
	if err != nil {
		return current, Paper{}, templateAnalysisQuestionLabels{}, "", err
	}
	examItem, err := h.questionRubricExams.GetExam(ctx, scope, current.ExamID)
	if err != nil || examItem.TenantID != scope.TenantID || examItem.ID != current.ExamID {
		return current, Paper{}, templateAnalysisQuestionLabels{}, "", ErrNotFound
	}
	if examItem.Status != "draft" && examItem.Status != "configured" {
		return current, Paper{}, templateAnalysisQuestionLabels{}, "", ErrExamFrozen
	}
	if current.Status != "draft" {
		return current, Paper{}, templateAnalysisQuestionLabels{}, "", ErrTemplateLocked
	}
	if current.Revision != input.ExpectedRevision || input.PageNo > current.PageCount {
		return current, Paper{}, templateAnalysisQuestionLabels{}, "", ErrConflict
	}
	papers, err := h.papers.ListPapers(ctx, scope.TenantID, current.ExamID)
	if err != nil {
		return current, Paper{}, templateAnalysisQuestionLabels{}, "", err
	}
	var source Paper
	for _, item := range papers {
		if item.ID == current.ExamPaperID {
			source = item
		}
	}
	if source.ID == "" || source.Purpose != PaperPurposeAnswerSheet || !strings.EqualFold(source.File.HashSHA256, input.ExpectedSourceSHA256) {
		return current, source, templateAnalysisQuestionLabels{}, "", ErrConflict
	}
	questions, err := h.questions.ListQuestions(ctx, scope.TenantID, current.ExamID)
	if err != nil {
		return current, source, templateAnalysisQuestionLabels{}, "", err
	}
	labels := templateAnalysisQuestionLabels{All: make([]string, 0, len(questions)), Objective: []string{}, FillBlank: []string{}}
	seen, seenObjective, seenFillBlank := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, question := range questions {
		label := strings.TrimSpace(question.QuestionNo)
		if label != "" && !seen[label] {
			labels.All, seen[label] = append(labels.All, label), true
		}
		// 题型只取正式试题快照；选择题大区不能依据浏览器标签或模型猜测拆成单题答题框。
		switch question.QuestionType {
		case "single_choice", "multiple_choice", "true_false":
			if label != "" && !seenObjective[label] {
				labels.Objective, seenObjective[label] = append(labels.Objective, label), true
			}
		case "fill_blank":
			// 只给本地填空区检测正式填空题号；数值、公式等其他题型不能依空白横线推断归属。
			if label != "" && !seenFillBlank[label] {
				labels.FillBlank, seenFillBlank[label] = append(labels.FillBlank, label), true
			}
		}
	}
	return current, source, labels, stableContentHash(questions), nil
}

func validateTemplatePageAnalysisInput(input TemplatePageAnalysisInput) error {
	if input.ExpectedRevision < 1 || input.PageNo < 1 || input.Width < 1 || input.Height < 1 || input.Width > 4096 || input.Height > 4096 || int64(input.Width)*int64(input.Height) > 4_000_000 || len(input.Candidates) > 500 {
		return ErrInvalidInput
	}
	if input.MediaType != "image/png" && input.MediaType != "image/jpeg" {
		return ErrInvalidInput
	}
	if input.WritingState != "blank" && input.WritingState != "written" && input.WritingState != "unknown" {
		return ErrInvalidInput
	}
	if len(input.DataBase64) > base64.StdEncoding.EncodedLen(maxTemplatePageImageBytes) || len(input.SHA256) != 64 || len(input.ExpectedSourceSHA256) != 64 {
		return ErrInvalidInput
	}
	if _, err := hex.DecodeString(input.ExpectedSourceSHA256); err != nil {
		return ErrInvalidInput
	}
	data, err := base64.StdEncoding.Strict().DecodeString(input.DataBase64)
	if err != nil || len(data) == 0 || len(data) > maxTemplatePageImageBytes || fmt.Sprintf("%x", sha256.Sum256(data)) != input.SHA256 {
		return ErrInvalidInput
	}
	dimensions, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || dimensions.Width != input.Width || dimensions.Height != input.Height ||
		!(format == "jpeg" && input.MediaType == "image/jpeg" || format == "png" && input.MediaType == "image/png") {
		return ErrInvalidInput
	}
	seen := map[string]bool{}
	for _, candidate := range input.Candidates {
		if strings.TrimSpace(candidate.CandidateID) == "" || len(candidate.CandidateID) > 128 || seen[candidate.CandidateID] || validateTemplateAnalysisBBox(candidate.BBox) != nil || len(candidate.EvidenceRefs) > 20 {
			return ErrInvalidInput
		}
		seen[candidate.CandidateID] = true
		for _, ref := range candidate.EvidenceRefs {
			if len(ref) > 128 {
				return ErrInvalidInput
			}
		}
	}
	return nil
}

func validateTemplateAnalysisBBox(box TemplateAnalysisBBox) error {
	return validateRegion(LayoutRegion{X: box.X, Y: box.Y, Width: box.Width, Height: box.Height})
}

func validateTemplatePageAnalysisResult(result TemplatePageAnalysis, input TemplatePageAnalysisInput, labels []string, fillBlankLabels ...[]string) error {
	if result.SchemaVersion != templatePageAnalysisSchema || result.PageNo != input.PageNo || result.Status != "review_required" || result.SourceSHA256 != input.SHA256 || result.CoordinateSpace != "source_display_normalized" || len(result.Candidates) > 500 {
		return ErrInvalidInput
	}
	if result.SemanticStatus != "completed" && result.SemanticStatus != "not_configured" && result.SemanticStatus != "failed" && result.SemanticStatus != "not_applicable" {
		return ErrInvalidInput
	}
	allowedLabels, seenCandidates, seenLabels := map[string]bool{}, map[string]bool{}, map[string]bool{}
	objectiveCounts := templateObjectiveItemCounts(result.Candidates)
	objectiveConflicts := templateObjectiveConflictingGroups(result.Candidates)
	for _, label := range labels {
		allowedLabels[label] = true
	}
	var officialFillBlank []string
	if len(fillBlankLabels) > 0 {
		officialFillBlank = fillBlankLabels[0]
	}
	if validateTemplateAnalysisRegistration(result.Registration, input) != nil || validateTemplateAnalysisFillGroups(result, officialFillBlank) != nil {
		return ErrInvalidInput
	}
	for candidateIndex, candidate := range result.Candidates {
		if validateTemplateCandidateOrigin(candidate, officialFillBlank) != nil || candidate.PrintedQuestionLabel != nil && !allowedLabels[*candidate.PrintedQuestionLabel] {
			return ErrInvalidInput
		}
		if candidate.SemanticCheck != "" && candidate.SemanticCheck != "supported" && candidate.SemanticCheck != "needs_review" && candidate.SemanticCheck != "not_evaluated" {
			return ErrInvalidInput
		}
		if len(candidate.ObjectiveItems) > 0 && (candidate.Role != "objective_group" || candidate.SemanticCheck != "supported" || result.SemanticStatus != "completed" || objectiveConflicts[candidateIndex] || !templateObjectiveItemsValid(candidate, allowedLabels, objectiveCounts)) {
			return ErrInvalidInput
		}
		if candidate.PageNo != input.PageNo || candidate.CandidateID == "" || seenCandidates[candidate.CandidateID] || validateTemplateAnalysisBBox(candidate.SourceBBox) != nil || validateTemplateAnalysisBBox(candidate.ProposedBBox) != nil {
			return ErrInvalidInput
		}
		seenCandidates[candidate.CandidateID] = true
		// 身份候选只传字段类别和区域；姓名、学号填写值不能经题号或标签进入可复用模板。
		if candidate.Role == "identity" && (result.SemanticStatus != "completed" || candidate.PrintedQuestionLabel != nil) {
			return ErrInvalidInput
		}
		// 大区本身不绑定单题；有完整题号和选项时由子框自动对应，旧响应仍可手动设置网格。
		if candidate.Role == "objective_group" && (result.SemanticStatus != "completed" || candidate.PrintedQuestionLabel != nil || candidate.IdentityLabel != nil || candidate.AssociationStatus != "unassociated") {
			return ErrInvalidInput
		}
		if candidate.IdentityLabel != nil {
			if candidate.Role != "identity" {
				return ErrInvalidInput
			}
			switch *candidate.IdentityLabel {
			case "姓名", "学号", "准考证号", "考号", "班级", "学校", "条码", "二维码", "身份信息":
			default:
				return ErrInvalidInput
			}
		}
		switch candidate.Role {
		case "answer", "objective_group", "identity", "instruction", "other", "uncertain":
		default:
			return ErrInvalidInput
		}
		switch candidate.AssociationStatus {
		case "associated":
			if result.SemanticStatus != "completed" || candidate.Role != "answer" || candidate.PrintedQuestionLabel == nil || !allowedLabels[*candidate.PrintedQuestionLabel] || seenLabels[*candidate.PrintedQuestionLabel] {
				return ErrInvalidInput
			}
			seenLabels[*candidate.PrintedQuestionLabel] = true
		case "unassociated", "ambiguous":
		default:
			return ErrInvalidInput
		}
	}
	return nil
}

func validateTemplateCandidateOrigin(candidate TemplateAnalyzedRegion, fillBlankLabels []string) error {
	switch candidate.Origin {
	case "", "model":
		return nil
	case "local_detector":
		// 普通本地线索仍只作待核对建议；独立填空布局经过完整证据核验后另走下方窄入口。
		if candidate.SemanticCheck == "needs_review" && candidate.AssociationStatus == "unassociated" {
			return nil
		}
		if candidate.Role != "answer" || candidate.SemanticCheck != "supported" || candidate.AssociationStatus != "associated" ||
			candidate.PrintedQuestionLabel == nil || candidate.IdentityLabel != nil || len(candidate.ObjectiveItems) != 0 {
			return ErrInvalidInput
		}
		// 题型取已鉴权的正式快照，不能仅凭工作节点自报原因码把任意本地区域自动关联。
		formalFillBlank := false
		for _, label := range fillBlankLabels {
			if label == *candidate.PrintedQuestionLabel {
				formalFillBlank = true
				break
			}
		}
		if !formalFillBlank {
			return ErrInvalidInput
		}
		codes := map[string]bool{}
		for _, code := range candidate.ReasonCodes {
			codes[code] = true
		}
		// 标题、印刷题号、横线三证据和原子合并结论须同时存在；随后仍检查重复题号、禁区及冲突。
		for _, required := range []string{"LOCAL_FILL_HEADER_VERIFIED", "LOCAL_FILL_PRINTED_MARKER_VERIFIED", "LOCAL_FILL_UNDERLINE_VERIFIED", "LOCAL_FILL_LAYOUT_VERIFIED"} {
			if !codes[required] {
				return ErrInvalidInput
			}
		}
		return nil
	}
	return ErrInvalidInput
}

func (s *DocumentImportService) analyzeTemplatePage(ctx context.Context, tenantID string, input TemplatePageAnalysisInput, labels []string, questionTypeLabels ...[]string) (TemplatePageAnalysis, error) {
	if s.templateVisionResolver == nil {
		return TemplatePageAnalysis{}, errTemplateVisionModelRequired
	}
	// 学校选择、启用状态和凭据由平台仓储在每次请求重新核对，前端不能传入模型 ID 或供应商。
	model, err := s.templateVisionResolver(ctx, tenantID)
	if err != nil || model == nil {
		return TemplatePageAnalysis{}, errTemplateVisionModelRequired
	}
	return s.requestTemplatePageAnalysis(ctx, input, labels, model, questionTypeLabels...)
}

// requestTemplatePageAnalysis 承接内部协议；传 nil 只供内部几何诊断，教师入口必须经过上方视觉模型门禁。
func (s *DocumentImportService) requestTemplatePageAnalysis(ctx context.Context, input TemplatePageAnalysisInput, labels []string, model *DocumentModelConfig, questionTypeLabels ...[]string) (TemplatePageAnalysis, error) {
	client, ok := s.parser.(*HTTPDocumentParser)
	if !ok || client == nil || client.baseURL == "" || len(client.token) < 32 {
		return TemplatePageAnalysis{}, errors.New("template analysis service unavailable")
	}
	payload := map[string]any{
		"request_id": uuid.NewString(), "schema_version": templatePageAnalysisSchema,
		"page": map[string]any{"page_no": input.PageNo, "width": input.Width, "height": input.Height,
			"media_type": input.MediaType, "data_base64": input.DataBase64, "sha256": input.SHA256},
		"candidates": input.Candidates, "question_labels": labels, "writing_state": input.WritingState,
	}
	if diagnostic, ok := ctx.Value(templateAnalysisDiagnosticContextKey{}).(templateAnalysisDiagnosticContext); ok {
		payload["diagnostic_context"] = diagnostic
	}
	// 两个可选切片依次是选择题和填空题白名单；保留旧几何诊断调用不传题型切片的兼容性。
	if len(questionTypeLabels) > 0 {
		payload["objective_question_labels"] = append([]string{}, questionTypeLabels[0]...)
	}
	if len(questionTypeLabels) > 1 {
		payload["fill_blank_question_labels"] = append([]string{}, questionTypeLabels[1]...)
	}
	if model != nil {
		payload["managed_model"] = model
	}
	if input.Candidates == nil {
		payload["candidates"] = []TemplateAnalysisGeometry{}
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return TemplatePageAnalysis{}, err
	}
	// 每页等待有界；请求取消会传递到内部 HTTP，不在 handler 结束后偷偷重试模型。
	callCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(callCtx, http.MethodPost, client.baseURL+"/answer-sheet/analyze", bytes.NewReader(body))
	if err != nil {
		return TemplatePageAnalysis{}, err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+client.token)
	response, err := client.client.Do(request)
	if err != nil {
		return TemplatePageAnalysis{}, errors.New("template analysis request failed")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return TemplatePageAnalysis{}, fmt.Errorf("template analysis returned %d", response.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, maxTemplatePageAnalysisResponseBytes+1))
	if err != nil || len(raw) > maxTemplatePageAnalysisResponseBytes {
		return TemplatePageAnalysis{}, errors.New("template analysis response exceeded limit")
	}
	var result TemplatePageAnalysis
	if err := json.Unmarshal(raw, &result); err != nil {
		return TemplatePageAnalysis{}, errors.New("template analysis response is invalid")
	}
	if result.RequestID != payload["request_id"] {
		return TemplatePageAnalysis{}, errors.New("template analysis response belongs to another request")
	}
	return result, nil
}
