package paper

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const maxDocumentParseResponseBytes = 4 << 20

type DocumentParseRequest struct {
	RequestID    string                     `json:"request_id"`
	Subject      string                     `json:"subject"`
	Documents    []normalizedImportDocument `json:"documents"`
	VisualPages  []documentVisualPage       `json:"visual_pages,omitempty"`
	ManagedModel *DocumentModelConfig       `json:"managed_model,omitempty"`
}

type DocumentParseResponse struct {
	Documents          []PaperImportDetectedDocument `json:"documents"`
	QuestionCandidates []QuestionCandidate           `json:"question_candidates"`
	AnswerCandidates   []AnswerCandidate             `json:"answer_candidates"`
	SolutionCandidates []SolutionCandidate           `json:"solution_candidates"`
	RubricCandidates   []RubricCandidate             `json:"rubric_candidates"`
	Issues             []PaperImportIssue            `json:"issues"`
	ModelUsage         PaperImportModelUsage         `json:"model_usage,omitempty"`
}

type documentParseRequest = DocumentParseRequest
type documentParseResponse = DocumentParseResponse
type PaperImportParseResult = DocumentParseResponse

type DocumentParser interface {
	Parse(context.Context, DocumentParseRequest, func(map[string]any) error) (DocumentParseResponse, error)
}

type HTTPDocumentParser struct {
	client  *http.Client
	baseURL string
	token   string
}

func NewHTTPDocumentParser(baseURL, token string, timeout time.Duration) *HTTPDocumentParser {
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	return &HTTPDocumentParser{
		client: &http.Client{Timeout: timeout}, baseURL: strings.TrimRight(strings.TrimSpace(baseURL), "/"), token: token,
	}
}

func (p *HTTPDocumentParser) Parse(ctx context.Context, input DocumentParseRequest, onProgress func(map[string]any) error) (DocumentParseResponse, error) {
	if p == nil || p.baseURL == "" || len(p.token) < 32 {
		return DocumentParseResponse{}, errors.New("AI service not configured")
	}
	body, _ := json.Marshal(input)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/paper/parse", bytes.NewReader(body))
	if err != nil {
		return DocumentParseResponse{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+p.token)
	req.Header.Set("Accept", "application/x-ndjson, application/json")
	resp, err := p.client.Do(req)
	if err != nil {
		return DocumentParseResponse{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return DocumentParseResponse{}, fmt.Errorf("paper parser returned %d", resp.StatusCode)
	}
	if !strings.Contains(strings.ToLower(resp.Header.Get("Content-Type")), "application/x-ndjson") {
		data, err := io.ReadAll(io.LimitReader(resp.Body, maxDocumentParseResponseBytes+1))
		if err != nil {
			return DocumentParseResponse{}, err
		}
		if len(data) > maxDocumentParseResponseBytes {
			return DocumentParseResponse{}, errors.New("paper parser response exceeded size limit")
		}
		var out DocumentParseResponse
		if err := json.NewDecoder(bytes.NewReader(data)).Decode(&out); err != nil {
			return DocumentParseResponse{}, err
		}
		return out, nil
	}
	return parseDocumentStream(resp.Body, onProgress)
}

type documentStreamFrame struct {
	Type     string                 `json:"type"`
	Progress map[string]any         `json:"progress"`
	Result   *DocumentParseResponse `json:"result"`
	Error    json.RawMessage        `json:"error"`
}

type documentStreamError struct {
	Status int `json:"status"`
	Error  struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func parseDocumentStream(body io.Reader, onProgress func(map[string]any) error) (DocumentParseResponse, error) {
	scanner := bufio.NewScanner(io.LimitReader(body, maxDocumentParseResponseBytes+1))
	scanner.Buffer(make([]byte, 64<<10), maxDocumentParseResponseBytes+1)
	readBytes := 0
	var result *DocumentParseResponse
	for scanner.Scan() {
		readBytes += len(scanner.Bytes()) + 1
		if readBytes > maxDocumentParseResponseBytes {
			return DocumentParseResponse{}, errors.New("paper parser response exceeded size limit")
		}
		var frame documentStreamFrame
		if err := json.Unmarshal(scanner.Bytes(), &frame); err != nil {
			return DocumentParseResponse{}, errors.New("paper parser stream contained invalid JSON")
		}
		switch frame.Type {
		case "progress":
			if onProgress != nil {
				if err := onProgress(frame.Progress); err != nil {
					return DocumentParseResponse{}, err
				}
			}
		case "result":
			if frame.Result == nil || result != nil {
				return DocumentParseResponse{}, errors.New("paper parser stream contained an invalid result")
			}
			result = frame.Result
		case "error":
			var detail documentStreamError
			if json.Unmarshal(frame.Error, &detail) != nil {
				return DocumentParseResponse{}, errors.New("paper parser stream reported an invalid error")
			}
			code := strings.TrimSpace(detail.Error.Code)
			if code == "" {
				code = "unknown_error"
			}
			return DocumentParseResponse{}, fmt.Errorf("paper parser stream failed: %s", code)
		default:
			return DocumentParseResponse{}, errors.New("paper parser stream contained an unknown event")
		}
	}
	if err := scanner.Err(); err != nil {
		return DocumentParseResponse{}, fmt.Errorf("read paper parser stream: %w", err)
	}
	if result == nil {
		return DocumentParseResponse{}, errors.New("paper parser stream ended without a result")
	}
	return *result, nil
}
