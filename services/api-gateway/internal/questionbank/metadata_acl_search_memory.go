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
	return memoryMutation(ctx, s, scope, "question_bank.metadata_schema.update", bankID, in, "manage", func() (MetadataSchema, error) {
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
	return memoryMutation(ctx, s, scope, "question_bank.acl.update", bankID, in, "manage", func() (ACLDocument, error) {
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
		prefix := scope.TenantID + ":" + bankID + ":"
		// 替换授权时保留创建者权限及当前操作者的 manage，避免把题库管理入口一并撤掉。
		for key := range s.acl {
			parts := strings.Split(key, ":")
			if strings.HasPrefix(key, prefix) && len(parts) == 4 && parts[2] != b.CreatedBy {
				if parts[2] != scope.ActorID || parts[3] != "manage" {
					delete(s.acl, key)
				}
			}
		}
		s.acl[aclKey(scope.TenantID, bankID, scope.ActorID, "manage")] = true
		doc := ACLDocument{BankID: bankID, Bindings: []ACLBinding{}, Groups: []ACLGroup{}}
		for _, binding := range in.Bindings {
			actions, _ := presetActions(binding.Preset)
			for _, action := range actions {
				s.acl[aclKey(scope.TenantID, bankID, binding.UserID, action)] = true
			}
			binding.Actions = actions
			doc.Bindings = append(doc.Bindings, binding)
		}
		for _, group := range in.Groups {
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
			doc.Groups = append(doc.Groups, group)
		}
		b.Revision++
		b.UpdatedAt = time.Now().UTC()
		s.banks[bankID] = b
		doc.Revision = b.Revision
		s.aclDocs[bankID] = copyACL(doc)
		return doc, nil
	})
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
		visible := f.Mode == "all" || f.Mode == "published" && i.CurrentPublishedVersionID != nil && *i.CurrentPublishedVersionID == v.ID || f.Mode == "my_drafts" && v.WorkflowStatus == "draft" && v.AuthorID == scope.ActorID || f.Mode == "default" && (i.CurrentPublishedVersionID != nil && *i.CurrentPublishedVersionID == v.ID || v.WorkflowStatus == "draft" && v.AuthorID == scope.ActorID)
		if !visible || f.Query != "" && !strings.Contains(strings.ToLower(i.ItemCode+" "+v.Stem), strings.ToLower(f.Query)) || f.SubjectCode != "" && i.SubjectCode != f.SubjectCode || f.KnowledgePoint != "" && !contains(v.KnowledgePoints, f.KnowledgePoint) || f.QuestionType != "" && v.QuestionType != f.QuestionType || f.Archetype != "" && v.AssessmentArchetype != f.Archetype || f.WorkflowStatus != "" && v.WorkflowStatus != f.WorkflowStatus || f.DifficultyBand != "" && v.Metadata.DifficultyBand != f.DifficultyBand || f.CognitiveLevel != "" && v.Metadata.CognitiveLevel != f.CognitiveLevel || f.Copyright != "" && v.Metadata.Copyright != f.Copyright || f.IntendedUse != "" && v.Metadata.IntendedUse != f.IntendedUse || f.UsePolicy != "" && v.Scoring.UsePolicy != f.UsePolicy {
			continue
		}
		if f.MetadataKey != "" && fmt.Sprint(v.CustomMetadata[f.MetadataKey]) != f.MetadataValue {
			continue
		}
		if f.StatisticsAvailable != nil && *f.StatisticsAvailable {
			continue
		}
		page.Items = append(page.Items, SearchItem{Item: i, Version: copyVersion(v), StatisticsAvailable: false})
	}
	sort.Slice(page.Items, func(i, j int) bool {
		a, b := page.Items[i], page.Items[j]
		if f.Sort == "item_code_asc" {
			if a.Item.ItemCode != b.Item.ItemCode {
				return a.Item.ItemCode < b.Item.ItemCode
			}
			if a.Item.ID != b.Item.ID {
				return a.Item.ID < b.Item.ID
			}
			return a.Version.VersionNo > b.Version.VersionNo
		}
		at, bt := a.Version.UpdatedAt, b.Version.UpdatedAt
		if f.Sort == "created_desc" {
			at, bt = a.Version.CreatedAt, b.Version.CreatedAt
		}
		if at.Equal(bt) {
			return a.Version.ID < b.Version.ID
		}
		return at.After(bt)
	})
	page.Total = len(page.Items)
	start, end := bounds(page.Total, Filter{Limit: f.Limit, Offset: f.Offset})
	page.Items = page.Items[start:end]
	return page, nil
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
	return memoryMutation(ctx, s, scope, "question_bank.item.retire", itemID, in, "retire", func() (Item, error) {
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
