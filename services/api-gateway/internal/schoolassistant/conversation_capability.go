package schoolassistant

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/modelgovernance"
	"github.com/google/uuid"
)

func (s *ConversationService) verifyToolSupport(ctx context.Context, p preparedConversation) (modelgovernance.ManagedAPIProbeUsage, bool, error) {
	config := p.connection.Config
	key := strings.Join([]string{p.user.TenantID, config.ID, config.ModelName, config.AdapterType, config.UpdatedAt.UTC().Format(time.RFC3339Nano)}, ":")
	s.mu.Lock()
	expires := s.verifiedTools[key]
	s.mu.Unlock()
	if time.Now().Before(expires) {
		return modelgovernance.ManagedAPIProbeUsage{}, false, nil
	}
	marker := uuid.NewString()
	parameters, _ := json.Marshal(map[string]any{"type": "object", "additionalProperties": false, "required": []string{"marker"}, "properties": map[string]any{"marker": map[string]any{"type": "string", "enum": []string{marker}}}})
	response, err := s.deps.Model.Complete(ctx, p.connection, modelgovernance.AgentRequest{
		Messages: []modelgovernance.AgentMessage{{Role: "system", Content: "这是无业务数据的工具能力检测。必须调用 check_tool_support 一次，把给定 marker 原样放入参数；不要回答文本。"}, {Role: "user", Content: "marker=" + marker}},
		Tools:    []modelgovernance.AgentTool{{Name: "check_tool_support", Description: "验证模型原生函数调用格式；不会执行业务。", Parameters: parameters}}, ToolChoice: "auto",
	})
	if err == nil {
		var args struct {
			Marker string `json:"marker"`
		}
		if len(response.Message.ToolCalls) != 1 || response.Message.ToolCalls[0].Name != "check_tool_support" || decodeToolArguments(response.Message.ToolCalls[0].Arguments, &args) != nil || args.Marker != marker {
			err = modelgovernance.ErrAgentToolsUnsupported
		}
	}
	s.recordUsage(ctx, p, 0, "tool_capability", response.Usage, err)
	if err != nil {
		return response.Usage, true, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	// 缓存只记录已通过的模型配置，过期或配置更新后重新验证，不把配置可用等同于工具可用。
	for cached, expiry := range s.verifiedTools {
		if !time.Now().Before(expiry) {
			delete(s.verifiedTools, cached)
		}
	}
	if len(s.verifiedTools) >= 256 {
		clear(s.verifiedTools)
	}
	s.verifiedTools[key] = time.Now().Add(10 * time.Minute)
	return response.Usage, true, nil
}
