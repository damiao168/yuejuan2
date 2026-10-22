package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/exam"
	"edugrade-enterprise/services/api-gateway/internal/files"
	"edugrade-enterprise/services/api-gateway/internal/logger"
	"edugrade-enterprise/services/api-gateway/internal/paper"
	"github.com/google/uuid"
)

// 实际路由需要同时守住考试与题目边界；允许同校考试管理者请求建议，不能借题目快照绕过另一学校。
func TestQuestionRubricDraftRouteChecksPermissionAndBothResourceBoundaries(t *testing.T) {
	for _, endpoint := range []string{"rubric-draft", "rubric-draft-jobs"} {
		t.Run(endpoint, func(t *testing.T) {
			for _, scenario := range []string{"allowed", "unauthenticated", "no permission", "foreign exam", "foreign question", "question in other exam", "unknown field"} {
				t.Run(scenario, func(t *testing.T) {
					stores := NewMemoryApplicationStores()
					identities := stores.Identity.Auth.(*auth.MemoryStore)
					permissions := []string{"exam:manage"}
					if scenario == "no permission" {
						permissions = []string{"exam:read"}
					}
					user := auth.UserWithPassword{User: auth.User{ID: "teacher", TenantID: "tenant", Username: "teacher", Status: "active", Roles: []string{"school_admin"},
						Permissions: permissions, DataScope: map[string]any{"school_admin": map[string]any{"scope": "school", "school_id": "school-a"}}}, SchoolID: "school-a", SecurityEpoch: 1}
					backendRegressionSession(t, identities, user, "session")
					item, err := stores.Exam.Exam.CreateExam(context.Background(), auth.AccessScope{TenantID: "tenant", TenantWide: true}, "teacher", exam.CreateInput{SchoolID: "school-a", Subject: "mathematics", Name: "math", TotalScore: 4})
					if err != nil {
						t.Fatal(err)
					}
					questionExam := item.ID
					if scenario == "question in other exam" {
						questionExam = "other-exam"
					}
					question, err := stores.Exam.Paper.CreateQuestion(context.Background(), "tenant", questionExam, "teacher", paper.CreateQuestionInput{
						QuestionNo: "1", QuestionType: "calculation", Score: 4, Stem: "解方程", Options: []string{}, KnowledgePoints: []string{}, AnswerKey: &paper.AnswerKeyInput{StandardAnswer: "移项\n求解"}})
					if err != nil {
						t.Fatal(err)
					}
					examBoundary := auth.ResourceBoundary{TenantID: "tenant", SchoolID: "school-a", ExamID: item.ID}
					questionBoundary := examBoundary
					if scenario == "foreign exam" {
						examBoundary.SchoolID = "school-b"
					}
					if scenario == "foreign question" {
						questionBoundary.SchoolID = "school-b"
					}
					identities.AddResourceBoundary("exam", item.ID, examBoundary)
					identities.AddResourceBoundary("question", question.ID, questionBoundary)
					calls := 0
					upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						calls++
						var input map[string]any
						if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
							t.Error(err)
							w.WriteHeader(400)
							return
						}
						q := input["question_candidate"].(map[string]any)
						solution := input["solution_candidate"].(map[string]any)
						_ = json.NewEncoder(w).Encode(map[string]any{"suggested_rubric_candidates": []any{map[string]any{
							"candidate_id": "generated", "question_no_normalized": q["question_no_normalized"], "max_score": q["score"], "status": "review_required", "origin": "ai_suggestion_from_solution",
							"provenance": map[string]any{"solution_candidate_id": solution["candidate_id"], "question_reference": input["question_reference"]},
							"points":     []any{map[string]any{"id": "p1", "description": "求解", "suggested_score": q["score"], "evidence_step_ids": []string{"step-1"}, "source_refs": solution["source_refs"]}},
						}}})
					}))
					defer upstream.Close()
					cfg := backendRegressionConfig()
					cfg.AIService.URL, cfg.AIService.Token, cfg.AIService.Timeout = upstream.URL, strings.Repeat("t", 32), time.Second
					router := NewRouterWithApplicationStores(cfg, logger.New(io.Discard, "error"), nil, files.NewMemoryObjectStorage(), stores)
					body := map[string]any{"expected_question": question}
					if endpoint == "rubric-draft-jobs" {
						body["request_id"] = uuid.NewString()
					}
					if scenario == "unknown field" {
						body["managed_model"] = map[string]any{"api_key": "injected"}
					}
					data, _ := json.Marshal(body)
					request := httptest.NewRequest(http.MethodPost, "/api/v1/exams/"+item.ID+"/questions/"+question.ID+"/"+endpoint, bytes.NewReader(data))
					request.Header.Set("Content-Type", "application/json")
					if scenario != "unauthenticated" {
						request.Header.Set("Authorization", "Bearer session")
					}
					response := httptest.NewRecorder()
					router.ServeHTTP(response, request)
					want, wantCalls := http.StatusOK, 1
					if endpoint == "rubric-draft-jobs" {
						want, wantCalls = http.StatusAccepted, 0
					}
					switch scenario {
					case "unauthenticated":
						want, wantCalls = http.StatusUnauthorized, 0
					case "no permission", "foreign exam", "foreign question":
						want, wantCalls = http.StatusForbidden, 0
					case "question in other exam":
						want, wantCalls = http.StatusNotFound, 0
					case "unknown field":
						want, wantCalls = http.StatusBadRequest, 0
					}
					if response.Code != want || calls != wantCalls {
						t.Fatalf("response=%d calls=%d body=%s; want %d calls=%d", response.Code, calls, response.Body.String(), want, wantCalls)
					}
					if endpoint == "rubric-draft-jobs" && scenario == "allowed" {
						var created struct {
							Job paper.RubricDraftJob `json:"job"`
						}
						if err := json.Unmarshal(response.Body.Bytes(), &created); err != nil || created.Job.ID == "" || created.Job.Status != "queued" || created.Job.Result != nil {
							t.Fatalf("queued submission: %s err=%v", response.Body.String(), err)
						}
						// 新路由只接收短提交；列表、详情和取消均能在没有浏览器长连接时独立读取。
						prefix := "/api/v1/exams/" + item.ID + "/rubric-draft-jobs"
						for _, check := range []struct{ method, path, status string }{
							{http.MethodGet, prefix, "queued"},
							{http.MethodGet, prefix + "/" + created.Job.ID, "queued"},
							{http.MethodPost, prefix + "/" + created.Job.ID + "/cancel", "cancelled"},
						} {
							next := httptest.NewRequest(check.method, check.path, nil)
							next.Header.Set("Authorization", "Bearer session")
							receipt := httptest.NewRecorder()
							router.ServeHTTP(receipt, next)
							if receipt.Code != http.StatusOK || !strings.Contains(receipt.Body.String(), `"status":"`+check.status+`"`) || strings.Contains(receipt.Body.String(), `"result":`) {
								t.Fatalf("%s %s: %d %s", check.method, check.path, receipt.Code, receipt.Body.String())
							}
						}
					}
				})
			}
		})
	}
}
