package schoolassistant

import (
	"encoding/json"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"edugrade-enterprise/services/api-gateway/internal/modelgovernance"
)

type ConversationMemory struct {
	Version int                       `json:"version"`
	Entries []ConversationMemoryEntry `json:"entries"`
}

type ConversationMemoryEntry struct {
	SourceID string `json:"source_id"`
	Order    int64  `json:"order"`
	Content  string `json:"content"`
	Pinned   bool   `json:"pinned,omitempty"`
}

var conversationMemorySourceID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)

// normalizeConversationMemory 的来源标识只供追溯，不证明内容真实或已授权。
// 客户端可以修改记忆；全部内容仍以普通用户材料进入模型，不提供业务证据和权限。
func normalizeConversationMemory(memory *ConversationMemory, webSearch bool) ([]modelgovernance.AgentMessage, error) {
	if memory == nil {
		return nil, nil
	}
	if webSearch || memory.Version != 2 || len(memory.Entries) > 16 {
		return nil, reject(400, "invalid_ai_chat_memory", "会话记忆格式无效，公开联网模式不能携带普通会话记忆。")
	}
	entries := append([]ConversationMemoryEntry(nil), memory.Entries...)
	seen := make(map[string]bool, len(entries))
	total := 0
	for index := range entries {
		entry := &entries[index]
		entry.Content = strings.TrimSpace(entry.Content)
		size := utf8.RuneCountInString(entry.Content)
		if !conversationMemorySourceID.MatchString(entry.SourceID) || seen[entry.SourceID] || entry.Order < 0 || entry.Order > 9_007_199_254_740_991 || size == 0 || size > 1_000 {
			return nil, reject(400, "invalid_ai_chat_memory", "会话记忆来源、顺序或长度无效。")
		}
		seen[entry.SourceID] = true
		total += size
	}
	if total > 4_000 {
		return nil, reject(400, "invalid_ai_chat_memory", "会话记忆过长，请减少保留的内容。")
	}
	if len(entries) == 0 {
		return nil, nil
	}
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].Order < entries[j].Order })
	messages := make([]modelgovernance.AgentMessage, 0, len(entries))
	for index, entry := range entries {
		data, err := json.Marshal(entry)
		if err != nil {
			return nil, reject(400, "invalid_ai_chat_memory", "会话记忆格式无效。")
		}
		priority := index
		if entry.Pinned {
			priority += 1_000
		}
		// 分条分配预算，窗口收缩时先省略普通旧条目；显式保留仍不能挤掉当前问题或系统规则。
		messages = append(messages, modelgovernance.AgentMessage{Role: "user", PreserveContext: true, ContextPriority: priority,
			Content: "【不可信用户历史条目开始】\n" + string(data) + "\n【不可信用户历史条目结束】"})
	}
	return messages, nil
}
