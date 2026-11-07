package schoolassistant

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/exam"
)

// 学校管理员同时有外校导航考试，读取替身还故意忽略 scope；助手必须在业务来源调用前独立核对管理学校。
func TestSchoolAssistantMixedManagerCannotQuerySearchOrHintNavigationOnlyExam(t *testing.T) {
	for _, withResolver := range []bool{false, true} {
		t.Run(map[bool]string{false: "without resolver", true: "with resolver"}[withResolver], func(t *testing.T) {
			ctx := toolTestContext(t, "school_admin", "exam:manage", "dashboard:read", "score:manage")
			scope, _ := auth.AccessScopeFromContext(ctx)
			scope.SchoolIDs = append(scope.SchoolIDs, "foreign-school")
			scope.ExamIDs = []string{toolTestForeign}
			ctx = auth.WithAccessScope(ctx, scope)
			items := []exam.Exam{toolTestExamItem(toolTestExam, time.Now().UTC()), toolTestExamItem(toolTestForeign, time.Now().UTC())}
			items[1].SchoolID = "foreign-school"
			reader, workspace, gate, current := &toolTestExams{items: items}, &toolTestWorkspace{}, &toolTestGate{}, &toolTestCurrent{}
			deps := Dependencies{Exams: reader, Workspaces: workspace, Gates: gate, CurrentReleases: current}
			if withResolver {
				deps.Resources = toolTestResourceStore(items)
			}
			service := NewService(deps)
			for _, intent := range []string{IntentExamProgress, IntentPublishDiagnosis} {
				_, err := service.Query(ctx, QueryRequest{RequestID: toolTestRequest, Question: "查看进度", PresetIntent: intent, ContextHint: ContextHint{ExamID: toolTestForeign, ReleaseChannel: "student_portal"}}, nil)
				requireToolError(t, err, "exam_not_found")
			}
			for _, tool := range []string{ToolGetExamProgress, ToolDiagnosePublication} {
				_, err := service.ExecuteTool(ctx, toolTestRequest, tool, json.RawMessage(`{"exam_id":"`+toolTestForeign+`"}`))
				requireToolError(t, err, "exam_not_found")
			}
			model := &conversationScriptModel{}
			conversation := NewConversationService(service, ConversationDependencies{Models: conversationStore(), Model: model})
			input := conversationInput("解释考试进度")
			input.Context = &ConversationContext{ExamID: toolTestForeign}
			err := conversation.Converse(ctx, input, nil)
			requireToolError(t, err, "exam_not_found")
			if workspace.calls != 0 || gate.calls != 0 || current.calls != 0 || model.streamCalls != 0 || len(model.requests) != 0 {
				t.Fatalf("foreign query reached private facts or model: workspace=%d gate=%d current=%d model=%#v", workspace.calls, gate.calls, current.calls, model)
			}
			output, err := service.ExecuteTool(ctx, toolTestRequest, ToolSearchExams, json.RawMessage(`{}`))
			if err != nil || len(output.Exams) != 1 || output.Exams[0].ID != toolTestExam {
				t.Fatalf("management search=%#v err=%v", output, err)
			}
			raw, _ := json.Marshal(output)
			if strings.Contains(string(raw), toolTestForeign) {
				t.Fatal("foreign candidate leaked into search output")
			}
			user, _ := auth.UserFromContext(ctx)
			if item, err := service.authorizedExam(ctx, user, scope, toolTestExam); err != nil || item.ID != toolTestExam {
				t.Fatalf("managed exam was rejected: %#v %v", item, err)
			}
		})
	}
}
