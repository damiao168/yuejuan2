package questionbank

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"github.com/google/uuid"
)

func copySchema(schema MetadataSchema) MetadataSchema {
	fields, taxonomies, _ := normalizeSchema(schema.Fields, schema.Taxonomies)
	schema.Fields, schema.Taxonomies = fields, taxonomies
	if schema.Fields == nil {
		schema.Fields = []MetadataFieldDefinition{}
	}
	if schema.Taxonomies == nil {
		schema.Taxonomies = []TaxonomyDefinition{}
	}
	return schema
}

func (s *MemoryStore) metadataSchema(scope auth.AccessScope, bankID string, version int, action string) (MetadataSchema, error) {
	b, err := s.bank(scope, bankID, action)
	if err != nil {
		return MetadataSchema{}, err
	}
	if version <= 0 {
		version = b.MetadataSchemaVersion
	}
	schema, ok := s.schemas[bankID+":"+strconv.Itoa(version)]
	if !ok {
		return MetadataSchema{}, ErrNotFound
	}
	return copySchema(schema), nil
}

func (s *MemoryStore) GetMetadataSchema(_ context.Context, scope auth.AccessScope, bankID string, version int) (MetadataSchema, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	schema, err := s.metadataSchema(scope, bankID, version, "read")
	if err == ErrNotFound {
		return s.metadataSchema(scope, bankID, version, "manage")
	}
	return schema, err
}

func (s *MemoryStore) UpdateMetadataSchema(ctx context.Context, scope auth.AccessScope, bankID string, in UpdateMetadataSchemaInput) (MetadataSchema, error) {
	fields, taxonomies, err := normalizeSchema(in.Fields, in.Taxonomies)
	if err != nil || in.ExpectedRevision <= 0 {
		if err != nil {
			return MetadataSchema{}, err
		}
		return MetadataSchema{}, ErrInvalidInput
	}
	in.Fields, in.Taxonomies = fields, taxonomies
	return memoryMutation(ctx, s, scope, memoryMutationCommand{op: "question_bank.metadata_schema.update", target: bankID, input: in, action: "manage"}, func() (MetadataSchema, error) {
		b, err := s.bank(scope, bankID, "manage")
		if err != nil {
			return MetadataSchema{}, err
		}
		if b.Revision != in.ExpectedRevision {
			return MetadataSchema{}, ErrConflict
		}
		if b.Status != "active" {
			return MetadataSchema{}, ErrLocked
		}
		b.MetadataSchemaVersion++
		// 新规则另存一版；已有题目继续绑定原 schema，新建版本才采用当前规则。
		b.Revision++
		b.UpdatedAt = time.Now().UTC()
		s.banks[bankID] = b
		schema := MetadataSchema{BankID: bankID, Version: b.MetadataSchemaVersion, Fields: fields, Taxonomies: taxonomies, CreatedBy: scope.ActorID, CreatedAt: b.UpdatedAt}
		s.schemas[bankID+":"+strconv.Itoa(schema.Version)] = copySchema(schema)
		return schema, nil
	})
}

func (s *MemoryStore) ValidateMetadata(_ context.Context, scope auth.AccessScope, bankID string, in ValidateMetadataInput) (MetadataValidationResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	schema, err := s.metadataSchema(scope, bankID, in.SchemaVersion, "read")
	if err != nil {
		schema, err = s.metadataSchema(scope, bankID, in.SchemaVersion, "manage")
	}
	if err != nil {
		return MetadataValidationResult{}, err
	}
	return validateMetadataValues(schema, copyMetadataValues(in.Values), true), nil
}

func copyACL(doc ACLDocument) ACLDocument {
	doc.Bindings = append([]ACLBinding{}, doc.Bindings...)
	doc.Groups = append([]ACLGroup{}, doc.Groups...)
	for i := range doc.Bindings {
		doc.Bindings[i].Actions = append([]string{}, doc.Bindings[i].Actions...)
	}
	for i := range doc.Groups {
		doc.Groups[i].Actions = append([]string{}, doc.Groups[i].Actions...)
		doc.Groups[i].MemberIDs = append([]string{}, doc.Groups[i].MemberIDs...)
	}
	return doc
}

func (s *MemoryStore) GetACL(_ context.Context, scope auth.AccessScope, bankID string) (ACLDocument, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := s.bank(scope, bankID, "manage")
	if err != nil {
		return ACLDocument{}, err
	}
	doc := copyACL(s.aclDocs[bankID])
	doc.BankID, doc.Revision = bankID, b.Revision
	creatorActions := []string{}
	for _, action := range []string{"read", "create", "edit", "review", "publish", "retire", "statistics", "manage"} {
		if s.acl[aclKey(scope.TenantID, bankID, b.CreatedBy, action)] {
			creatorActions = append(creatorActions, action)
		}
	}
	doc.Bindings = append([]ACLBinding{{UserID: b.CreatedBy, Preset: presetForActions(append([]string{}, creatorActions...)), Actions: creatorActions}}, doc.Bindings...)
	return doc, nil
}

func (s *MemoryStore) UpdateACL(ctx context.Context, scope auth.AccessScope, bankID string, in UpdateACLInput) (ACLDocument, error) {
	if err := validateACLInput(in); err != nil {
		return ACLDocument{}, err
	}
	return memoryMutation(ctx, s, scope, memoryMutationCommand{op: "question_bank.acl.update", target: bankID, input: in, action: "manage"}, func() (ACLDocument, error) {
		b, err := s.bank(scope, bankID, "manage")
		if err != nil {
			return ACLDocument{}, err
		}
		if b.Revision != in.ExpectedRevision {
			return ACLDocument{}, ErrConflict
		}
		if b.Status != "active" {
			return ACLDocument{}, ErrLocked
		}
		retained, err := retainedCustomACL(s.aclDocs[bankID], in)
		if err != nil {
			return ACLDocument{}, err
		}
		operatorActions := s.protectedOperatorActions(scope, bankID)
		s.clearReplaceableACL(scope, bankID, b.CreatedBy)
		s.acl[aclKey(scope.TenantID, bankID, scope.ActorID, "manage")] = true
		doc := ACLDocument{BankID: bankID, Bindings: []ACLBinding{}, Groups: []ACLGroup{}}
		s.restoreRetainedACLGrants(scope.TenantID, bankID, retained)
		doc.Bindings = append(retained.Bindings, s.applyACLBindings(scope, bankID, in.Bindings)...)
		if scope.ActorID != b.CreatedBy {
			doc.Bindings = mergeProtectedOperatorBinding(doc.Bindings, scope.ActorID, operatorActions)
		}
		doc.Groups = append(retained.Groups, s.applyACLGroups(scope, bankID, in.Groups)...)
		b.Revision++
		b.UpdatedAt = time.Now().UTC()
		s.banks[bankID] = b
		doc.Revision = b.Revision
		s.aclDocs[bankID] = copyACL(doc)
		return doc, nil
	})
}

// 调用方已持锁；组合权限的动作和成员仅来自旧文档，新请求不能用 actions 扩大预设职责。
func (s *MemoryStore) restoreRetainedACLGrants(tenantID, bankID string, retained ACLDocument) {
	for _, binding := range retained.Bindings {
		for _, action := range binding.Actions {
			s.acl[aclKey(tenantID, bankID, binding.UserID, action)] = true
		}
	}
	for _, group := range retained.Groups {
		for _, member := range group.MemberIDs {
			for _, action := range group.Actions {
				s.acl[aclKey(tenantID, bankID, member, action)] = true
			}
		}
	}
}

func mergeProtectedOperatorBinding(bindings []ACLBinding, actorID string, operatorActions []string) []ACLBinding {
	operator := ACLBinding{UserID: actorID, Actions: operatorActions}
	found := false
	// 同用户绑定须逐个合并保留动作，不能找到首个后就停止。
	for i := range bindings {
		if bindings[i].UserID == actorID {
			bindings[i].Actions = uniqueStrings(append(bindings[i].Actions, operatorActions...))
			bindings[i].Preset = presetForActions(append([]string{}, bindings[i].Actions...))
			found = true
		}
	}
	if !found {
		operator.Preset = presetForActions(append([]string{}, operator.Actions...))
		bindings = append(bindings, operator)
	}
	return bindings
}

// 调用方已持有 memoryMutation 的锁；与 PostgreSQL 一样保留创建者和操作者的直接授权。
func (s *MemoryStore) clearReplaceableACL(scope auth.AccessScope, bankID, createdBy string) {
	operatorActions := s.protectedOperatorActions(scope, bankID)
	prefix := scope.TenantID + ":" + bankID + ":"
	for key := range s.acl {
		parts := strings.Split(key, ":")
		if strings.HasPrefix(key, prefix) && len(parts) == 4 && parts[2] != createdBy {
			delete(s.acl, key)
		}
	}
	for _, action := range operatorActions {
		s.acl[aclKey(scope.TenantID, bankID, scope.ActorID, action)] = true
	}
}

func (s *MemoryStore) protectedOperatorActions(scope auth.AccessScope, bankID string) []string {
	direct := directACLActionSet(s.aclDocs[bankID].Bindings, scope.ActorID)
	grouped := groupACLActionSet(s.aclDocs[bankID].Groups, scope.ActorID)
	out := []string{"manage"}
	// 内存权限索引已展开组权限；保留操作者直接授权时须排除纯组贡献，避免撤组后把它变成永久直接授权。
	for _, action := range []string{"read", "create", "edit", "review", "publish", "retire", "statistics"} {
		if s.acl[aclKey(scope.TenantID, bankID, scope.ActorID, action)] && (direct[action] || !grouped[action]) {
			out = append(out, action)
		}
	}
	return out
}

func directACLActionSet(bindings []ACLBinding, actorID string) map[string]bool {
	direct := map[string]bool{}
	for _, binding := range bindings {
		if binding.UserID == actorID {
			for _, action := range binding.Actions {
				direct[action] = true
			}
		}
	}
	return direct
}

func groupACLActionSet(groups []ACLGroup, actorID string) map[string]bool {
	grouped := map[string]bool{}
	for _, group := range groups {
		for _, member := range group.MemberIDs {
			if member == actorID {
				for _, action := range group.Actions {
					grouped[action] = true
				}
			}
		}
	}
	return grouped
}

// 两种授权展开都由 memoryMutation 持锁调用，不自行获取锁。
func (s *MemoryStore) applyACLBindings(scope auth.AccessScope, bankID string, bindings []ACLBinding) []ACLBinding {
	out := []ACLBinding{}
	for _, binding := range bindings {
		actions, _ := presetActions(binding.Preset)
		for _, action := range actions {
			s.acl[aclKey(scope.TenantID, bankID, binding.UserID, action)] = true
		}
		binding.Actions = actions
		out = append(out, binding)
	}
	return out
}

func (s *MemoryStore) applyACLGroups(scope auth.AccessScope, bankID string, groups []ACLGroup) []ACLGroup {
	out := []ACLGroup{}
	for _, group := range groups {
		if group.ID == "" {
			group.ID = uuid.NewString()
		}
		actions, _ := presetActions(group.Preset)
		group.Actions = actions
		for _, member := range group.MemberIDs {
			for _, action := range actions {
				s.acl[aclKey(scope.TenantID, bankID, member, action)] = true
			}
		}
		out = append(out, group)
	}
	return out
}

func (s *MemoryStore) SearchItems(_ context.Context, scope auth.AccessScope, filter SearchFilter) (SearchPage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := cleanSearchFilter(filter)
	page := SearchPage{Items: []SearchItem{}, Limit: f.Limit, Offset: f.Offset}
	if err != nil {
		return page, err
	}
	for _, v := range s.versions {
		i := s.items[v.ItemID]
		if i.Kind != "question" || i.Status != "active" || f.BankID != "" && i.BankID != f.BankID {
			continue
		}
		if _, err = s.bank(scope, i.BankID, "read"); err != nil {
			continue
		}
		if !searchVersionVisible(f.Mode, i, v, scope.ActorID) || !matchesSearchVersion(i, v, f) {
			continue
		}
		page.Items = append(page.Items, SearchItem{Item: i, Version: copyVersion(v), StatisticsAvailable: false})
	}
	sort.Slice(page.Items, func(i, j int) bool {
		return searchItemLess(page.Items[i], page.Items[j], f.Sort)
	})
	// Total 统计完整可见集合，再分页；不能用当前页长度替代总数。
	page.Total = len(page.Items)
	start, end := bounds(page.Total, Filter{Limit: f.Limit, Offset: f.Offset})
	page.Items = page.Items[start:end]
	return page, nil
}

func searchVersionVisible(mode string, i Item, v Version, actorID string) bool {
	switch mode {
	case "all":
		return true
	case "published":
		return i.CurrentPublishedVersionID != nil && *i.CurrentPublishedVersionID == v.ID
	case "my_drafts":
		return v.WorkflowStatus == "draft" && v.AuthorID == actorID
	case "default":
		return i.CurrentPublishedVersionID != nil && *i.CurrentPublishedVersionID == v.ID || v.WorkflowStatus == "draft" && v.AuthorID == actorID
	default:
		return false
	}
}

func matchesSearchVersion(i Item, v Version, f SearchFilter) bool {
	if !matchesSearchContent(i, v, f) || !matchesSearchMetadata(v, f) {
		return false
	}
	if f.MetadataKey != "" && fmt.Sprint(v.CustomMetadata[f.MetadataKey]) != f.MetadataValue {
		return false
	}
	if f.StatisticsAvailable != nil && *f.StatisticsAvailable {
		return false
	}
	return true
}

func matchesSearchContent(i Item, v Version, f SearchFilter) bool {
	if f.Query != "" && !strings.Contains(strings.ToLower(i.ItemCode+" "+v.Stem), strings.ToLower(f.Query)) || f.SubjectCode != "" && i.SubjectCode != f.SubjectCode || f.KnowledgePoint != "" && !contains(v.KnowledgePoints, f.KnowledgePoint) || f.QuestionType != "" && v.QuestionType != f.QuestionType || f.Archetype != "" && v.AssessmentArchetype != f.Archetype || f.WorkflowStatus != "" && v.WorkflowStatus != f.WorkflowStatus {
		return false
	}
	return true
}

func matchesSearchMetadata(v Version, f SearchFilter) bool {
	if f.DifficultyBand != "" && v.Metadata.DifficultyBand != f.DifficultyBand || f.CognitiveLevel != "" && v.Metadata.CognitiveLevel != f.CognitiveLevel || f.Copyright != "" && v.Metadata.Copyright != f.Copyright || f.IntendedUse != "" && v.Metadata.IntendedUse != f.IntendedUse || f.UsePolicy != "" && v.Scoring.UsePolicy != f.UsePolicy {
		return false
	}
	return true
}

func searchItemLess(a, b SearchItem, order string) bool {
	if order == "item_code_asc" {
		if a.Item.ItemCode != b.Item.ItemCode {
			return a.Item.ItemCode < b.Item.ItemCode
		}
		if a.Item.ID != b.Item.ID {
			return a.Item.ID < b.Item.ID
		}
		return a.Version.VersionNo > b.Version.VersionNo
	}
	at, bt := a.Version.UpdatedAt, b.Version.UpdatedAt
	if order == "created_desc" {
		at, bt = a.Version.CreatedAt, b.Version.CreatedAt
	}
	if at.Equal(bt) {
		return a.Version.ID < b.Version.ID
	}
	return at.After(bt)
}

func contains(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func (s *MemoryStore) RetireItem(ctx context.Context, scope auth.AccessScope, itemID string, in RetireItemInput) (Item, error) {
	if in.ExpectedRevision <= 0 {
		return Item{}, ErrInvalidInput
	}
	return memoryMutation(ctx, s, scope, memoryMutationCommand{op: "question_bank.item.retire", target: itemID, input: in, action: "retire"}, func() (Item, error) {
		i, err := s.item(scope, itemID, "retire")
		if err != nil {
			return i, err
		}
		if i.Revision != in.ExpectedRevision {
			return i, ErrConflict
		}
		if i.Status != "active" {
			return i, ErrLocked
		}
		i.Status = "retired"
		i.Revision++
		s.items[itemID] = i
		return i, nil
	})
}
