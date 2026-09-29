package httpx

import (
	"encoding/json"
	"net/http"

	"edugrade-enterprise/services/api-gateway/internal/logger"
)

const (
	ErrorCodeHeader        = "X-EduGrade-Error-Code"
	OperationOutcomeHeader = "X-EduGrade-Operation-Outcome"
	CommandIDHeader        = "X-EduGrade-Command-ID"
)

type ErrorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type ErrorResponse struct {
	RequestID string    `json:"request_id,omitempty"`
	TraceID   string    `json:"trace_id,omitempty"`
	Error     ErrorBody `json:"error"`
}

func JSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

// Error 将稳定错误码同时写入响应头和正文，供客户端及监控识别；message 只能包含可公开的信息。
func Error(w http.ResponseWriter, r *http.Request, status int, code string, message string) {
	w.Header().Set(ErrorCodeHeader, code)
	JSON(w, status, ErrorResponse{
		RequestID: logger.RequestID(r.Context()),
		TraceID:   logger.TraceID(r.Context()),
		Error: ErrorBody{
			Code:    code,
			Message: message,
		},
	})
}
