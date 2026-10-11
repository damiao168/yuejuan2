package schoolassistant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/exam"
	"edugrade-enterprise/services/api-gateway/internal/releasegate"
	"edugrade-enterprise/services/api-gateway/internal/scorerelease"
	"edugrade-enterprise/services/api-gateway/internal/workspace"
)

const (
	toolTestTenant  = "11111111-1111-4111-8111-111111111111"
	toolTestSchool  = "22222222-2222-4222-8222-222222222222"
	toolTestExam    = "33333333-3333-4333-8333-333333333333"
	toolTestForeign = "44444444-4444-4444-8444-444444444444"
	toolTestRequest = "77777777-7777-4777-8777-777777777777"
)

// 读替身故意忽略 scope，验证工具仍逐条执行身份、学校和 body 资源边界检查。
type toolTestExams struct {
	items     []exam.Exam
	listCalls int
	getCalls  int
	failAt    int
}

func (s *toolTestExams) GetExam(_ context.Context, _ auth.AccessScope, id string) (exam.Exam, error) {
	s.getCalls++
	for _, item := range s.items {
		if item.ID == id {
			return item, nil
		}
	}
	return exam.Exam{}, exam.ErrNotFound
}

func (s *toolTestExams) ListExams(_ context.Context, _ auth.AccessScope, filter exam.ListFilter) ([]exam.Exam, error) {
	s.listCalls++
	if s.failAt == s.listCalls {
		return nil, errors.New("source failed")
	}
	items := append([]exam.Exam{}, s.items...)
	sort.Slice(items, func(i, j int) bool {
		if items[i].CreatedAt.Equal(items[j].CreatedAt) {
			return items[i].ID > items[j].ID
		}
		return items[i].CreatedAt.After(items[j].CreatedAt)
	})
	result := []exam.Exam{}
	for _, item := range items {
		if !filter.CursorAt.IsZero() && (item.CreatedAt.After(filter.CursorAt) || (item.CreatedAt.Equal(filter.CursorAt) && item.ID >= filter.CursorID)) {
			continue
		}
		result = append(result, item)
		if len(result) == filter.Limit {
			break
		}
	}
	return result, nil
}

type toolTestWorkspace struct {
	projection workspace.Projection
	err        error
	calls      int
}

func (w *toolTestWorkspace) Get(_ context.Context, _ auth.AccessScope, _ string) (workspace.Projection, error) {
	w.calls++
	return w.projection, w.err
}

type toolTestGate struct {
	value releasegate.Evaluation
	calls int
}

func (g *toolTestGate) Inspect(_ context.Context, _, _ string) (releasegate.Evaluation, error) {
	g.calls++
	return g.value, nil
}

type toolTestCurrent struct {
	value scorerelease.Detail
	calls int
}

func (c *toolTestCurrent) CurrentPublished(_ context.Context, _, _ string) (scorerelease.Detail, error) {
	c.calls++
	return c.value, nil
}

func toolTestContext(t *testing.T, role string, permissions ...string) context.Context {
	t.Helper()
	user := auth.User{ID: "66666666-6666-4666-8666-666666666666", TenantID: toolTestTenant, Roles: []string{role}, Permissions: permissions, DataScope: map[string]any{"scope": "school", "school_id": toolTestSchool}}
	scope, err := auth.ResolveDeclaredAccessScope(user)
	if err != nil {
		t.Fatal(err)
	}
	return auth.WithAccessScope(auth.WithUser(context.Background(), user), scope)
}

func toolTestExamItem(id string, createdAt time.Time) exam.Exam {
	return exam.Exam{ID: id, TenantID: toolTestTenant, SchoolID: toolTestSchool, Name: "十月数学月考", Subject: "math", Status: "grading", Revision: 3, CreatedAt: createdAt}
}

func toolTestResourceStore(items []exam.Exam) *auth.MemoryStore {
	store := auth.NewMemoryStore()
	for _, item := range items {
		store.AddResourceBoundary("exam", item.ID, auth.ResourceBoundary{TenantID: item.TenantID, SchoolID: item.SchoolID, ExamID: item.ID})
	}
	return store
}

func requireToolError(t *testing.T, err error, code string) {
	t.Helper()
	var value *queryError
	if !errors.As(err, &value) || value.code != code {
		t.Fatalf("want %s, got %v", code, err)
	}
}

func TestToolDefinitionsAndExecutionCheckCurrentPermission(t *testing.T) {
	reader := &toolTestExams{}
	w := &toolTestWorkspace{}
	service := NewService(Dependencies{Exams: reader, Workspaces: w})
	ctx := toolTestContext(t, "school_admin", "dashboard:read")
	definitions, err := service.ToolDefinitions(ctx)
	if err != nil || len(definitions) != 1 || definitions[0].Name != ToolGetExamProgress || definitions[0].Label == "" {
		t.Fatalf("unexpected definitions: %+v, %v", definitions, err)
	}
	_, err = service.ExecuteTool(ctx, toolTestRequest, ToolSearchExams, json.RawMessage(`{}`))
	requireToolError(t, err, "permission_denied")
	if reader.listCalls != 0 {
		t.Fatal("unauthorized search reached reader")
	}
	_, err = service.ExecuteTool(toolTestContext(t, "teacher", "exam:manage"), toolTestRequest, ToolSearchExams, json.RawMessage(`{}`))
	requireToolError(t, err, "forbidden")
	// 工具曾在上一轮可见不构成授权；换成没有权限的本轮上下文仍须拒绝。
	_, err = service.ExecuteTool(toolTestContext(t, "school_admin"), toolTestRequest, ToolGetExamProgress, json.RawMessage(`{"exam_id":"`+toolTestExam+`"}`))
	requireToolError(t, err, "permission_denied")
	if w.calls != 0 {
		t.Fatal("revoked permission reached workspace")
	}
}

func TestToolsRejectUnknownFieldsInvalidParametersAndUnknownTools(t *testing.T) {
	reader := &toolTestExams{}
	service := NewService(Dependencies{Exams: reader})
	ctx := toolTestContext(t, "school_admin", "exam:manage", "dashboard:read", "score:manage")
	for _, args := range []string{`null`, `[]`, `{}`, `{"limit":11}`, `{"limit":0}`, `{"limit":null}`, `{"query":null}`, `{"recent_days":0}`, `{"recent_days":366}`, `{"tenant_id":"other"}`, `{"actor_id":"other"}`, `{"cursor":"invalid"}`, `{"query":"a"} {}`} {
		if args == `{}` {
			continue
		}
		_, err := service.ExecuteTool(ctx, toolTestRequest, ToolSearchExams, json.RawMessage(args))
		requireToolError(t, err, "invalid_tool_arguments")
	}
	_, err := service.ExecuteTool(ctx, toolTestRequest, "publish_scores", json.RawMessage(`{}`))
	requireToolError(t, err, "unknown_tool")
	_, err = service.ExecuteTool(ctx, toolTestRequest, ToolGetExamProgress, json.RawMessage(`{"exam_id":"bad-id"}`))
	requireToolError(t, err, "invalid_exam_id")
	_, err = service.ExecuteTool(ctx, toolTestRequest, ToolDiagnosePublication, json.RawMessage(`{"exam_id":"`+toolTestExam+`","release_channel":"legacy"}`))
	requireToolError(t, err, "unsupported_channel")
	if reader.listCalls != 0 || reader.getCalls != 0 {
		t.Fatal("invalid arguments reached readers")
	}
}

func TestSearchExamsUsesChineseSubjectAndFiltersForeignResources(t *testing.T) {
	now := time.Now().UTC()
	items := []exam.Exam{toolTestExamItem(toolTestExam, now.Add(-time.Hour)), toolTestExamItem(toolTestForeign, now.Add(-2*time.Hour))}
	items[1].SchoolID = "other-school"
	items = append(items, toolTestExamItem("55555555-5555-4555-8555-555555555555", now.Add(-60*24*time.Hour)))
	reader := &toolTestExams{items: items}
	service := NewService(Dependencies{Exams: reader, Resources: toolTestResourceStore(items)})
	output, err := service.ExecuteTool(toolTestContext(t, "school_admin", "exam:manage"), toolTestRequest, ToolSearchExams, json.RawMessage(`{"query":"十月","subject":"数学","recent_days":30}`))
	if err != nil || len(output.Exams) != 1 || output.Exams[0].ID != toolTestExam || output.Status != "completed" {
		t.Fatalf("unexpected search output: %+v, %v", output, err)
	}
	raw, _ := json.Marshal(output)
	if strings.Contains(string(raw), toolTestForeign) || output.HasMore {
		t.Fatalf("foreign or out-of-window candidate leaked: %s", raw)
	}
	if len(output.Result.Actions) != 1 || output.Result.Actions[0].Target.ExamID != toolTestExam {
		t.Fatalf("unsafe navigation: %+v", output.Result.Actions)
	}
	// 存储可返回本校行，但独立资源 resolver 的拒绝仍优先，防止薄适配器绕过统一边界。
	service.deps.Resources = auth.NewMemoryStore()
	output, err = service.ExecuteTool(toolTestContext(t, "school_admin", "exam:manage"), toolTestRequest, ToolSearchExams, json.RawMessage(`{}`))
	if err != nil || len(output.Exams) != 0 {
		t.Fatalf("resolver denial ignored: %+v, %v", output, err)
	}
}

func TestSearchExamsBoundsScanAndResumesWithoutSkippingCandidates(t *testing.T) {
	now := time.Now().UTC()
	items := []exam.Exam{}
	for index := 0; index < 240; index++ {
		item := toolTestExamItem(fmt.Sprintf("%08d-3333-4333-8333-333333333333", index+1), now.Add(-time.Duration(index)*time.Minute))
		item.Name = "其他考试"
		if index >= 200 {
			item.Name = "目标考试"
		}
		items = append(items, item)
	}
	reader := &toolTestExams{items: items}
	service := NewService(Dependencies{Exams: reader, Resources: toolTestResourceStore(items)})
	ctx := toolTestContext(t, "school_admin", "exam:manage")
	output, err := service.ExecuteTool(ctx, toolTestRequest, ToolSearchExams, json.RawMessage(`{"query":"目标","limit":2}`))
	if err != nil || output.Scanned != examSearchMaxScan || len(output.Exams) != 0 || !output.HasMore || output.NextCursor == "" || reader.listCalls != 4 {
		t.Fatalf("search was not bounded: %+v, calls=%d, err=%v", output, reader.listCalls, err)
	}
	args, _ := json.Marshal(map[string]any{"query": "目标", "limit": 2, "cursor": output.NextCursor})
	next, err := service.ExecuteTool(ctx, toolTestRequest, ToolSearchExams, args)
	if err != nil || len(next.Exams) != 2 || next.Exams[0].ID != items[200].ID || next.Exams[1].ID != items[201].ID || !next.HasMore {
		t.Fatalf("cursor skipped candidates: %+v, %v", next, err)
	}
	if len(next.Result.Actions) != 2 || next.Result.Actions[0].ID == next.Result.Actions[1].ID {
		t.Fatalf("different exams share a navigation action: %+v", next.Result.Actions)
	}
}

func TestSearchExamsSourceFailureIsUnavailableNotZero(t *testing.T) {
	reader := &toolTestExams{failAt: 1}
	service := NewService(Dependencies{Exams: reader})
	output, err := service.ExecuteTool(toolTestContext(t, "school_admin", "exam:manage"), toolTestRequest, ToolSearchExams, json.RawMessage(`{}`))
	if err != nil || output.Status != "unavailable" || len(output.Result.Sources) != 1 || output.Result.Sources[0].Status != "unavailable" || output.Result.Facts[0].Value != nil || output.Result.Facts[0].Available {
		t.Fatalf("failure became empty statistics: %+v, %v", output, err)
	}
}

func TestExamToolsRejectCrossSchoolAndPreserveUnavailableSources(t *testing.T) {
	items := []exam.Exam{toolTestExamItem(toolTestExam, time.Now().UTC()), toolTestExamItem(toolTestForeign, time.Now().UTC())}
	items[1].SchoolID = "other-school"
	w := &toolTestWorkspace{err: errors.New("workspace down")}
	service := NewService(Dependencies{Exams: &toolTestExams{items: items}, Resources: toolTestResourceStore(items), Workspaces: w})
	ctx := toolTestContext(t, "school_admin", "dashboard:read")
	_, err := service.ExecuteTool(ctx, toolTestRequest, ToolGetExamProgress, json.RawMessage(`{"exam_id":"`+toolTestForeign+`"}`))
	requireToolError(t, err, "exam_not_found")
	if w.calls != 0 {
		t.Fatal("cross-school request read workspace")
	}
	output, err := service.ExecuteTool(ctx, toolTestRequest, ToolGetExamProgress, json.RawMessage(`{"exam_id":"`+toolTestExam+`"}`))
	if err != nil || output.Status != "unavailable" || len(output.Result.Facts) != 0 || output.Result.Sources[0].Status != "unavailable" {
		t.Fatalf("source failure became progress facts: %+v, %v", output, err)
	}
}

func TestDiagnosePublicationUsesFullGateAndCurrentPointer(t *testing.T) {
	items := []exam.Exam{toolTestExamItem(toolTestExam, time.Now().UTC())}
	w := &toolTestWorkspace{projection: workspace.Projection{ExamID: toolTestExam, Revision: 3}}
	gate := &toolTestGate{value: releasegate.Evaluation{Policy: releasegate.Policy{TenantID: toolTestTenant, ExamID: toolTestExam, Version: "policy-v2"}, BaseGate: scorerelease.Gate{Blocking: []scorerelease.GateIssue{{Code: "future_blocker", Message: "真实领域阻塞", Blocking: true, Count: 2, ActionRoute: "unknown-future-route"}}}}}
	current := &toolTestCurrent{value: scorerelease.Detail{Release: scorerelease.Release{ID: "current-release", TenantID: toolTestTenant, ExamID: toolTestExam, Status: scorerelease.StatusPublished, Version: 2}}}
	service := NewService(Dependencies{Exams: &toolTestExams{items: items}, Resources: toolTestResourceStore(items), Workspaces: w, Gates: gate, CurrentReleases: current})
	output, err := service.ExecuteTool(toolTestContext(t, "school_admin", "score:manage"), toolTestRequest, ToolDiagnosePublication, json.RawMessage(`{"exam_id":"`+toolTestExam+`"}`))
	if err != nil || output.Status != "completed" || gate.calls != 1 || current.calls != 1 || len(output.Result.Sources) != 3 {
		t.Fatalf("unexpected publication tool: %+v, %v", output, err)
	}
	raw, _ := json.Marshal(output)
	if !strings.Contains(string(raw), "future_blocker") || strings.Contains(string(raw), "unknown-future-route") || output.Result.Sources[2].ReleaseVersion == nil || *output.Result.Sources[2].ReleaseVersion != 2 {
		t.Fatalf("lost domain blocker/current pointer or exposed unknown action: %s", raw)
	}
}
