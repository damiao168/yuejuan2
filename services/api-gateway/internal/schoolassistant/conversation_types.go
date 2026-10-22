package schoolassistant

import (
	"encoding/json"
	"sync"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/modelgovernance"
)

// ConversationDependencies 只接入当前租户的模型解析和调用，不暴露配置修改能力。
type ConversationDependencies struct {
	Models modelgovernance.ManagedAPIConfigStore
	Model  modelgovernance.AgentModel
	Usage  modelgovernance.UsageLedger
}

type ConversationContext struct {
	ExamID        string `json:"exam_id,omitempty"`
	OriginModule  string `json:"origin_module,omitempty"`
	OriginSection string `json:"origin_section,omitempty"`
}

type ConversationRequest struct {
	ModelConfigID string                               `json:"model_config_id,omitempty"`
	Messages      []modelgovernance.ManagedChatMessage `json:"messages"`
	Context       *ConversationContext                 `json:"context,omitempty"`
}

type ConversationEvent struct {
	Type       string                                  `json:"type"`
	RequestID  string                                  `json:"request_id"`
	Seq        int                                     `json:"seq"`
	Model      *modelgovernance.ManagedChatModelStatus `json:"model,omitempty"`
	CallID     string                                  `json:"call_id,omitempty"`
	ToolName   string                                  `json:"tool_name,omitempty"`
	Label      string                                  `json:"label,omitempty"`
	Status     string                                  `json:"status,omitempty"`
	Channel    string                                  `json:"channel,omitempty"`
	Delta      string                                  `json:"delta,omitempty"`
	Sources    []Source                                `json:"sources,omitempty"`
	Actions    []Action                                `json:"actions,omitempty"`
	Scope      *Scope                                  `json:"scope,omitempty"`
	Completion *modelgovernance.ManagedChatCompletion  `json:"completion,omitempty"`
	Code       string                                  `json:"code,omitempty"`
	Message    string                                  `json:"message,omitempty"`
}

// MarshalJSON 只在来源事件中保证数组存在；其他事件不携带无关的 null 字段。
func (event ConversationEvent) MarshalJSON() ([]byte, error) {
	type wireEvent ConversationEvent
	if event.Type != "sources" {
		return json.Marshal(wireEvent(event))
	}
	sources, actions := event.Sources, event.Actions
	if sources == nil {
		sources = []Source{}
	}
	if actions == nil {
		actions = []Action{}
	}
	return json.Marshal(struct {
		wireEvent
		Sources []Source `json:"sources"`
		Actions []Action `json:"actions"`
	}{wireEvent: wireEvent(event), Sources: sources, Actions: actions})
}

type ConversationService struct {
	tools         *Service
	deps          ConversationDependencies
	mu            sync.Mutex
	active        map[string]int
	verifiedTools map[string]time.Time
}

func NewConversationService(tools *Service, deps ConversationDependencies) *ConversationService {
	if deps.Model == nil {
		deps.Model = modelgovernance.NewHTTPAgentModel(0)
	}
	return &ConversationService{tools: tools, deps: deps, active: make(map[string]int), verifiedTools: make(map[string]time.Time)}
}
