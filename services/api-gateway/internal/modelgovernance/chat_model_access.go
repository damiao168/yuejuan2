package modelgovernance

import (
	"context"
	"errors"
	"sort"

	"github.com/google/uuid"
)

var ErrChatAccessChanged = errors.New("chat model access changed")

// ChatModelAccess 只开放聊天用途，不改变解析默认项或阅卷角色。
type ChatModelAccess struct {
	Revision string   `json:"revision"`
	ModelIDs []string `json:"model_ids"`
}
type ChatModelAccessInput struct {
	ExpectedRevision string   `json:"expected_revision"`
	ModelIDs         []string `json:"model_ids"`
}
type ChatModelAccessStore interface {
	GetChatModelAccess(context.Context, string) (ChatModelAccess, error)
	SaveChatModelAccess(context.Context, string, string, ChatModelAccessInput) (ChatModelAccess, error)
}

func initialChatAccess(configs []ManagedAPIConfig) ChatModelAccess {
	result := ChatModelAccess{Revision: "absent", ModelIDs: []string{}}
	// 存量学校继承原默认项；不能因升级就自动开放所有保存过密钥的模型。
	for _, config := range configs {
		if config.IsDefault {
			result.ModelIDs = append(result.ModelIDs, config.ID)
		}
	}
	return result
}
func normalizeChatAccess(input ChatModelAccessInput) (ChatModelAccessInput, error) {
	if input.ExpectedRevision != "absent" {
		if _, err := uuid.Parse(input.ExpectedRevision); err != nil {
			return input, ErrInvalidManagedConfig
		}
	}
	if len(input.ModelIDs) > 100 {
		return input, ErrInvalidManagedConfig
	}
	seen := map[string]bool{}
	for _, id := range input.ModelIDs {
		if _, err := uuid.Parse(id); err != nil || seen[id] {
			return input, ErrInvalidManagedConfig
		}
		seen[id] = true
	}
	input.ModelIDs = append([]string{}, input.ModelIDs...)
	sort.Strings(input.ModelIDs)
	return input, nil
}
func chatConfigEligible(config ManagedAPIConfig) bool {
	return config.Status == "active" && config.CredentialConfigured && managedTextVerified(config) && config.DeletedAt == nil
}
func validateChatAccess(current ChatModelAccess, input ChatModelAccessInput, configs []ManagedAPIConfig) error {
	if current.Revision != input.ExpectedRevision {
		return ErrChatAccessChanged
	}
	for _, id := range input.ModelIDs {
		found := false
		for _, config := range configs {
			if config.ID == id {
				found = true
				// 已开放模型可能在检测后失效；保存其他模型时可保留原编号，但运行时仍拒绝使用，撤销后也必须重新通过资格检查。
				if !contains(current.ModelIDs, id) && !chatConfigEligible(config) {
					return ErrManagedTextRequired
				}
				break
			}
		}
		if !found {
			return ErrManagedConfigUnavailable
		}
	}
	return nil
}

func (s *MemoryStore) chatConfigsLocked(tenantID string) []ManagedAPIConfig {
	configs := []ManagedAPIConfig{}
	for _, config := range s.managedConfigs {
		if config.TenantID == tenantID && config.DeletedAt == nil {
			configs = append(configs, config)
		}
	}
	return configs
}
func (s *MemoryStore) GetChatModelAccess(_ context.Context, tenantID string) (ChatModelAccess, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result, ok := s.chatAccess[tenantID]
	if !ok {
		result = initialChatAccess(s.chatConfigsLocked(tenantID))
	}
	result.ModelIDs = append([]string{}, result.ModelIDs...)
	return result, nil
}
func (s *MemoryStore) SaveChatModelAccess(_ context.Context, tenantID, actorID string, input ChatModelAccessInput) (ChatModelAccess, error) {
	input, err := normalizeChatAccess(input)
	if err != nil {
		return ChatModelAccess{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	configs := s.chatConfigsLocked(tenantID)
	current, ok := s.chatAccess[tenantID]
	if !ok {
		current = initialChatAccess(configs)
	}
	if err = validateChatAccess(current, input, configs); err != nil {
		return ChatModelAccess{}, err
	}
	result := ChatModelAccess{Revision: uuid.NewString(), ModelIDs: input.ModelIDs}
	if s.chatAccess == nil {
		s.chatAccess = map[string]ChatModelAccess{}
	}
	s.chatAccess[tenantID] = result
	result.ModelIDs = append([]string{}, result.ModelIDs...)
	return result, nil
}

// 调用方持有模型写锁；只有原默认成员将改变时才冻结继承名单，普通改名不制造无关修订冲突。
func (s *MemoryStore) preserveInheritedChatAccessLocked(tenantID, changedID string, newDefault bool) {
	if _, exists := s.chatAccess[tenantID]; exists {
		return
	}
	inherited := initialChatAccess(s.chatConfigsLocked(tenantID))
	changed := false
	for _, id := range inherited.ModelIDs {
		if id == changedID && !newDefault || id != changedID && newDefault {
			changed = true
		}
	}
	if !changed {
		return
	}
	inherited.Revision = uuid.NewString()
	if s.chatAccess == nil {
		s.chatAccess = map[string]ChatModelAccess{}
	}
	s.chatAccess[tenantID] = inherited
}

// ResolveChatManagedAPI 每次请求重新检查开放范围和模型资格；选定连接贯穿规划、工具和最终回答，不在供应商失败时偷换模型。
func ResolveChatManagedAPI(ctx context.Context, store ManagedAPIConfigStore, tenantID, modelID string) (*ManagedAPIConnection, error) {
	accessStore, ok := store.(ChatModelAccessStore)
	if !ok {
		if modelID != "" {
			return nil, ErrManagedConfigUnavailable
		}
		return ResolveDefaultManagedAPI(ctx, store, tenantID)
	}
	access, err := accessStore.GetChatModelAccess(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	if modelID == "" {
		configs, err := store.ListManagedAPIConfigs(ctx, tenantID)
		if err != nil {
			return nil, err
		}
		for _, config := range configs {
			if config.IsDefault && contains(access.ModelIDs, config.ID) && chatConfigEligible(config) {
				modelID = config.ID
				break
			}
		}
		if modelID == "" {
			for _, config := range configs {
				if contains(access.ModelIDs, config.ID) && chatConfigEligible(config) {
					modelID = config.ID
					break
				}
			}
		}
		if modelID == "" {
			return nil, nil
		}
	}
	if !contains(access.ModelIDs, modelID) {
		return nil, ErrManagedConfigUnavailable
	}
	connection, err := store.GetManagedAPIConnection(ctx, tenantID, modelID)
	if err != nil {
		return nil, err
	}
	if connection.Config.TenantID != tenantID || !chatConfigEligible(connection.Config) {
		return nil, ErrManagedConfigUnavailable
	}
	return &connection, nil
}
