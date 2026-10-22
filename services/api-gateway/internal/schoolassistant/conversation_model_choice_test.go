package schoolassistant

import (
	"context"
	"testing"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/exam"
	"edugrade-enterprise/services/api-gateway/internal/modelgovernance"
)

const secondConversationModel = "88888888-8888-4888-8888-888888888888"

type conversationChoiceStore struct {
	*conversationModelStore
	approved []string
}

func (s conversationChoiceStore) GetChatModelAccess(context.Context, string) (modelgovernance.ChatModelAccess, error) {
	return modelgovernance.ChatModelAccess{Revision: "absent", ModelIDs: s.approved}, nil
}
func (s conversationChoiceStore) SaveChatModelAccess(context.Context, string, string, modelgovernance.ChatModelAccessInput) (modelgovernance.ChatModelAccess, error) {
	return modelgovernance.ChatModelAccess{}, modelgovernance.ErrInvalidManagedConfig
}
func (s conversationChoiceStore) GetManagedAPIConnection(ctx context.Context, tenantID, id string) (modelgovernance.ManagedAPIConnection, error) {
	if id == secondConversationModel && tenantID == s.connection.Config.TenantID {
		copy := s.connection
		copy.Config.ID = id
		copy.Config.IsDefault = false
		copy.Config.ProviderKey = "other-provider"
		return copy, nil
	}
	return s.conversationModelStore.GetManagedAPIConnection(ctx, tenantID, id)
}

type conversationChoiceModel struct {
	conversationScriptModel
	connections []string
}

func (m *conversationChoiceModel) Complete(ctx context.Context, connection modelgovernance.ManagedAPIConnection, request modelgovernance.AgentRequest) (modelgovernance.AgentResponse, error) {
	m.connections = append(m.connections, connection.Config.ID)
	return m.conversationScriptModel.Complete(ctx, connection, request)
}
func (m *conversationChoiceModel) Stream(ctx context.Context, connection modelgovernance.ManagedAPIConnection, request modelgovernance.AgentRequest, emit func(modelgovernance.AgentEvent) error) (modelgovernance.AgentResponse, error) {
	m.connections = append(m.connections, connection.Config.ID)
	return m.conversationScriptModel.Stream(ctx, connection, request, emit)
}

func TestConversationKeepsApprovedChoiceAcrossPlanningAndAnswer(t *testing.T) {
	for _, withTool := range []bool{false, true} {
		t.Run(map[bool]string{false: "ordinary reply", true: "reply after tool lookup"}[withTool], func(t *testing.T) {
			model := &conversationChoiceModel{conversationScriptModel: conversationScriptModel{answer: "synthetic answer"}}
			store := conversationStore()
			store.connection.Config.CredentialConfigured = true
			approved := &conversationChoiceStore{conversationModelStore: store, approved: []string{store.connection.Config.ID, secondConversationModel}}
			reader := &toolTestExams{items: []exam.Exam{toolTestExamItem(toolTestExam, time.Now().UTC())}}
			if withTool {
				model.plans = [][]modelgovernance.AgentToolCall{{conversationCall("search", ToolSearchExams, `{}`)}}
			}
			service := NewConversationService(NewService(Dependencies{Exams: reader}), ConversationDependencies{Models: approved, Model: model})
			input := ConversationRequest{ModelConfigID: secondConversationModel, Messages: []modelgovernance.ManagedChatMessage{{Role: "user", Content: "普通问题"}}}
			ctx := toolTestContext(t, "school_admin", "exam:manage")
			// 在首个模型事件后撤销开放，当前已选定的连接仍贯穿这一轮；下一轮必须重新检查，不能沿用旧资格。
			if err := service.Converse(ctx, input, func(event ConversationEvent) error {
				if event.Type == "model" {
					approved.approved = []string{store.connection.Config.ID}
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			if len(model.connections) < 3 || model.probeCalls != 1 || model.streamCalls != 1 {
				t.Fatalf("probe, planning and answer not exercised: %v", model.connections)
			}
			if withTool && len(model.requests) != 2 {
				t.Fatalf("tool planning rounds not exercised: %d", len(model.requests))
			}
			for _, id := range model.connections {
				if id != secondConversationModel {
					t.Fatalf("model changed inside request: %v", model.connections)
				}
			}
			before := len(model.connections)
			for _, id := range []string{secondConversationModel, "77777777-7777-4777-8777-777777777777"} {
				input.ModelConfigID = id
				if err := service.Converse(ctx, input, func(ConversationEvent) error { return nil }); err == nil {
					t.Fatalf("unapproved or revoked model accepted: %s", id)
				}
			}
			if len(model.connections) != before {
				t.Fatal("provider called for unapproved model")
			}
		})
	}
}
