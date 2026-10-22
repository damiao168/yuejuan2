package paper

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/google/uuid"
)

func (s *DocumentImportService) analyzeObjectiveGrid(ctx context.Context, input ObjectiveGridInput) (ObjectiveGridAnalysis, error) {
	client, ok := s.parser.(*HTTPDocumentParser)
	if !ok || client == nil || client.baseURL == "" || len(client.token) < 32 {
		return ObjectiveGridAnalysis{}, errors.New("objective grid service unavailable")
	}
	requestID := uuid.NewString()
	payload := map[string]any{
		"request_id": requestID, "schema_version": objectiveGridSchema,
		"page": map[string]any{"page_no": input.PageNo, "width": input.Width, "height": input.Height,
			"media_type": input.MediaType, "data_base64": input.DataBase64, "sha256": input.SHA256},
		"block_bbox": input.BlockBBox, "option_count": input.OptionCount, "option_orientation": input.OptionOrientation,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return ObjectiveGridAnalysis{}, err
	}
	// 仅执行一次有界 CPU 解析，不调用学校模型；浏览器取消会终止内部等待。
	callCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(callCtx, http.MethodPost, client.baseURL+"/answer-sheet/objective-grid", bytes.NewReader(body))
	if err != nil {
		return ObjectiveGridAnalysis{}, err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+client.token)
	response, err := client.client.Do(request)
	if err != nil {
		return ObjectiveGridAnalysis{}, errors.New("objective grid request failed")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return ObjectiveGridAnalysis{}, errors.New("objective grid response failed")
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, maxTemplatePageAnalysisResponseBytes+1))
	if err != nil || len(raw) > maxTemplatePageAnalysisResponseBytes {
		return ObjectiveGridAnalysis{}, errors.New("objective grid response exceeded limit")
	}
	var result ObjectiveGridAnalysis
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&result) != nil || decoder.Decode(&struct{}{}) != io.EOF || result.RequestID != requestID {
		return ObjectiveGridAnalysis{}, errors.New("objective grid response is invalid")
	}
	return result, nil
}
