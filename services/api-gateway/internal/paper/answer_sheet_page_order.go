package paper

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"io"
	"net/http"
	"regexp"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/httpx"
	"github.com/google/uuid"
)

const answerSheetPageOrderSchema = "answer-sheet-page-order-v1"
const answerSheetPageOrderBodyLimit = 20 << 20
const answerSheetOrderImageLimit = 128 << 10

var answerSheetOrderPageID = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,128}$`)

type AnswerSheetOrderPage struct {
	PageID     string `json:"page_id"`
	Width      int    `json:"width"`
	Height     int    `json:"height"`
	MediaType  string `json:"media_type"`
	DataBase64 string `json:"data_base64"`
	SHA256     string `json:"sha256"`
}

type AnswerSheetPageOrderInput struct {
	Pages []AnswerSheetOrderPage `json:"pages"`
}
type AnswerSheetPageOrderResponse struct {
	Status        string            `json:"status"`
	PageOrder     []string          `json:"page_order"`
	ReasonCodes   []string          `json:"reason_codes"`
	ModelIdentity map[string]string `json:"model_identity"`
}

func (h *Handler) AnalyzeAnswerSheetPageOrder(w http.ResponseWriter, r *http.Request) {
	if h.documentImport == nil || h.questionRubricExams == nil {
		httpx.Error(w, r, http.StatusServiceUnavailable, "answer_sheet_page_order_unavailable", "答题卡页序分析暂不可用")
		return
	}
	var input AnswerSheetPageOrderInput
	r.Body = http.MaxBytesReader(w, r.Body, answerSheetPageOrderBodyLimit)
	if !decodeJSON(w, r, &input) {
		return
	}
	if validateAnswerSheetPageOrderInput(input) != nil {
		httpx.Error(w, r, http.StatusBadRequest, "invalid_request", "答题卡页序图片或页标识不符合要求")
		return
	}
	user := mustUser(r)
	scope, ok := auth.AccessScopeFromContext(r.Context())
	if !ok || scope.TenantID != user.TenantID || scope.ActorID != user.ID {
		httpx.Error(w, r, http.StatusForbidden, "access_scope_forbidden", "考试不在当前访问范围内")
		return
	}
	if err := h.answerSheetPageOrderExam(r.Context(), scope, r.PathValue("examId")); err != nil {
		writeStoreError(w, r, err)
		return
	}
	result, err := h.documentImport.analyzeAnswerSheetPageOrder(r.Context(), scope.TenantID, input)
	if errors.Is(err, errTemplateVisionModelRequired) {
		httpx.Error(w, r, http.StatusServiceUnavailable, "template_vision_model_required", "请平台管理员分配可用的答题卡多模态模型")
		return
	}
	if err != nil || validateAnswerSheetPageOrderResult(result, input) != nil {
		httpx.Error(w, r, http.StatusBadGateway, "answer_sheet_page_order_failed", "答题卡自动页序分析未完成，请重试")
		return
	}
	// 分析本身不登记文件；考试在等待模型时进入正式状态，就不能继续据此上传配置资料。
	if err := h.answerSheetPageOrderExam(r.Context(), scope, r.PathValue("examId")); err != nil {
		writeStoreError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, result)
}

func (h *Handler) answerSheetPageOrderExam(ctx context.Context, scope auth.AccessScope, id string) error {
	item, err := h.questionRubricExams.GetExam(ctx, scope, id)
	if err != nil {
		return ErrNotFound
	}
	if item.Status != "draft" && item.Status != "configured" {
		return ErrExamFrozen
	}
	return nil
}

func validateAnswerSheetPageOrderInput(input AnswerSheetPageOrderInput) error {
	if len(input.Pages) < 1 || len(input.Pages) > 100 {
		return ErrInvalidInput
	}
	seen := map[string]bool{}
	for _, page := range input.Pages {
		if !answerSheetOrderPageID.MatchString(page.PageID) || seen[page.PageID] || page.Width < 1 || page.Width > 1000 || page.Height < 1 || page.Height > 1000 || page.MediaType != "image/jpeg" || len(page.DataBase64) > base64.StdEncoding.EncodedLen(answerSheetOrderImageLimit) || len(page.SHA256) != 64 {
			return ErrInvalidInput
		}
		seen[page.PageID] = true
		if _, err := hex.DecodeString(page.SHA256); err != nil {
			return ErrInvalidInput
		}
		data, err := base64.StdEncoding.Strict().DecodeString(page.DataBase64)
		if err != nil || len(data) == 0 || len(data) > answerSheetOrderImageLimit || fmt.Sprintf("%x", sha256.Sum256(data)) != page.SHA256 {
			return ErrInvalidInput
		}
		dimensions, format, err := image.DecodeConfig(bytes.NewReader(data))
		if err != nil || format != "jpeg" || dimensions.Width != page.Width || dimensions.Height != page.Height {
			return ErrInvalidInput
		}
	}
	return nil
}

func validateAnswerSheetPageOrderResult(result AnswerSheetPageOrderResponse, input AnswerSheetPageOrderInput) error {
	if result.PageOrder == nil || result.ReasonCodes == nil || len(result.ReasonCodes) > 32 || result.ModelIdentity == nil {
		return ErrInvalidInput
	}
	for _, code := range result.ReasonCodes {
		switch code {
		case "PAGE_ORDER_DOES_NOT_VERIFY_STUDENT_IDENTITY", "PAGE_IMAGE_DUPLICATED", "SEMANTIC_MODEL_NOT_CONFIGURED", "PAGE_ORDER_TIME_BUDGET_EXCEEDED", "MODEL_ANALYSIS_FAILED", "MODEL_OUTPUT_INVALID", "MULTIPLE_DOCUMENTS_SUSPECTED", "PRINTED_PAGE_NUMBER_REQUIRED", "DUPLICATE_PRINTED_PAGE_NUMBER", "PRINTED_PAGE_SET_INCOMPLETE", "PRINTED_TOTAL_PAGE_COUNT_REQUIRED", "PRINTED_TOTAL_PAGE_COUNT_CONFLICT":
		case "PAGE_COUNT_NOT_VERIFIED", "PAGE_ORDER_FROM_QUESTION_SEQUENCE", "PAGE_ORDER_FROM_CONTENT_CONTINUITY", "PAGE_ORDER_EVIDENCE_CONFLICT", "PAGE_ORDER_EVIDENCE_INSUFFICIENT", "MODEL_AUTH_FAILED", "MODEL_RATE_LIMITED", "MODEL_TIMEOUT", "MODEL_REQUEST_REJECTED":
		default:
			return ErrInvalidInput
		}
	}
	if result.Status == "uncertain" {
		if len(result.PageOrder) != 0 {
			return ErrInvalidInput
		}
		return nil
	}
	if result.Status != "ordered" || len(result.PageOrder) != len(input.Pages) {
		return ErrInvalidInput
	}
	allowed := map[string]bool{}
	for _, page := range input.Pages {
		allowed[page.PageID] = true
	}
	for _, id := range result.PageOrder {
		if !allowed[id] {
			return ErrInvalidInput
		}
		delete(allowed, id)
	}
	return nil
}

func (s *DocumentImportService) analyzeAnswerSheetPageOrder(ctx context.Context, tenantID string, input AnswerSheetPageOrderInput) (AnswerSheetPageOrderResponse, error) {
	if s.templateVisionResolver == nil {
		return AnswerSheetPageOrderResponse{}, errTemplateVisionModelRequired
	}
	model, err := s.templateVisionResolver(ctx, tenantID)
	if err != nil || model == nil {
		return AnswerSheetPageOrderResponse{}, errTemplateVisionModelRequired
	}
	client, ok := s.parser.(*HTTPDocumentParser)
	if !ok || client == nil || client.baseURL == "" || len(client.token) < 32 {
		return AnswerSheetPageOrderResponse{}, errors.New("page order service unavailable")
	}
	requestID := uuid.NewString()
	body, err := json.Marshal(map[string]any{"request_id": requestID, "schema_version": answerSheetPageOrderSchema, "pages": input.Pages, "managed_model": model})
	if err != nil {
		return AnswerSheetPageOrderResponse{}, err
	}
	callCtx, cancel := context.WithTimeout(ctx, 240*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(callCtx, http.MethodPost, client.baseURL+"/answer-sheet/page-order", bytes.NewReader(body))
	if err != nil {
		return AnswerSheetPageOrderResponse{}, err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+client.token)
	response, err := client.client.Do(request)
	if err != nil {
		return AnswerSheetPageOrderResponse{}, errors.New("page order request failed")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return AnswerSheetPageOrderResponse{}, errors.New("page order service rejected request")
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, (2<<20)+1))
	if err != nil || len(raw) > 2<<20 {
		return AnswerSheetPageOrderResponse{}, errors.New("page order response exceeded limit")
	}
	var envelope struct {
		RequestID     string `json:"request_id"`
		SchemaVersion string `json:"schema_version"`
		AnswerSheetPageOrderResponse
	}
	if json.Unmarshal(raw, &envelope) != nil || envelope.RequestID != requestID || envelope.SchemaVersion != answerSheetPageOrderSchema {
		return AnswerSheetPageOrderResponse{}, errors.New("page order response is invalid")
	}
	// 模型名只取本请求已授权配置，不采用供应商自由返回的身份描述。
	envelope.ModelIdentity = map[string]string{"provider_key": model.ProviderKey, "model_name": model.ModelName, "model_version": model.ModelVersion}
	return envelope.AnswerSheetPageOrderResponse, nil
}
