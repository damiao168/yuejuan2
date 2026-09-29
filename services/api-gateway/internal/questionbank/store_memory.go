package questionbank

import (
	"context"
	"errors"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/commandreceipt"
	"github.com/google/uuid"
)

// MemoryStore is an explicit development/test implementation, not persistence.
type MemoryStore struct {
	mu       sync.Mutex
	banks    map[string]Bank
	items    map[string]Item
	versions map[string]Version
	acl      map[string]bool
	reviews  map[string][]Review
	schemas  map[string]MetadataSchema
	aclDocs  map[string]ACLDocument
	receipts commandreceipt.Memory
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{banks: map[string]Bank{}, items: map[string]Item{}, versions: map[string]Version{}, acl: map[string]bool{}, reviews: map[string][]Review{}, schemas: map[string]MetadataSchema{}, aclDocs: map[string]ACLDocument{}}
}
func aclKey(tenant, bank, actor, action string) string {
	return tenant + ":" + bank + ":" + actor + ":" + action
}
func copyVersion(v Version) Version {
	v.Scoring = copyScoring(v.Scoring)
	v.Options = append([]string{}, v.Options...)
	v.KnowledgePoints = append([]string{}, v.KnowledgePoints...)
	v.CustomMetadata = copyMetadataValues(v.CustomMetadata)
	if v.SourceVersionID != nil {
		source := *v.SourceVersionID
		v.SourceVersionID = &source
	}
	return v
}
func (s *MemoryStore) bank(scope auth.AccessScope, id, action string) (Bank, error) {
	b, ok := s.banks[id]
	if !validScope(scope) || !ok || b.TenantID != scope.TenantID || !scope.AllowsSchool(b.SchoolID) || !s.acl[aclKey(scope.TenantID, id, scope.ActorID, action)] || (action != "manage" && !s.acl[aclKey(scope.TenantID, id, scope.ActorID, "read")]) {
		return Bank{}, ErrNotFound
	}
	return b, nil
}
func (s *MemoryStore) item(scope auth.AccessScope, id, action string) (Item, error) {
	i, ok := s.items[id]
	if !ok || i.TenantID != scope.TenantID {
		return Item{}, ErrNotFound
	}
	if _, err := s.bank(scope, i.BankID, action); err != nil {
		return Item{}, err
	}
	return i, nil
}
func (s *MemoryStore) version(scope auth.AccessScope, id, action string) (Version, error) {
	v, ok := s.versions[id]
	if !ok || v.TenantID != scope.TenantID {
		return Version{}, ErrNotFound
	}
	if _, err := s.item(scope, v.ItemID, action); err != nil {
		return Version{}, err
	}
	return copyVersion(v), nil
}
func memoryMutation[T any](ctx context.Context, s *MemoryStore, scope auth.AccessScope, op, target string, input any, action string, fn func() (T, error)) (out T, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !validScope(scope) {
		return out, auth.ErrForbidden
	}
	replay, err := s.receipts.Load(ctx, scope.TenantID, scope.ActorID, op, target, input, &out)
	if err != nil {
		return out, err
	}
	if replay {
		// 命令回执也可能在撤权后被再次读取，返回旧结果前仍要检查当前权限。
		switch v := any(out).(type) {
		case Bank:
			_, err = s.bank(scope, v.ID, action)
		case ItemResult:
			_, err = s.bank(scope, v.Item.BankID, action)
		case Version:
			_, err = s.item(scope, v.ItemID, action)
		case MetadataSchema:
			_, err = s.bank(scope, v.BankID, "manage")
		case ACLDocument:
			_, err = s.bank(scope, v.BankID, "manage")
		case Item:
			_, err = s.item(scope, v.ID, action)
		}
		if err != nil {
			var zero T
			return zero, err
		}
		return out, nil
	}
	out, err = fn()
	if err != nil {
		return out, err
	}
	err = s.receipts.Save(ctx, scope.TenantID, scope.ActorID, op, target, input, out)
	return
}
func (s *MemoryStore) GetBank(_ context.Context, scope auth.AccessScope, id string) (Bank, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := s.bank(scope, id, "read")
	if errors.Is(err, ErrNotFound) {
		return s.bank(scope, id, "manage")
	}
	return b, err
}
func (s *MemoryStore) GetItem(_ context.Context, scope auth.AccessScope, id string) (Item, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.item(scope, id, "read")
}
func (s *MemoryStore) GetVersion(_ context.Context, scope auth.AccessScope, id string) (Version, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.version(scope, id, "read")
}
func (s *MemoryStore) CreateBank(ctx context.Context, scope auth.AccessScope, input CreateBankInput) (Bank, error) {
	in, err := normalizeBank(input)
	if err != nil {
		return Bank{}, err
	}
	if !scope.AllowsSchool(in.SchoolID) {
		return Bank{}, auth.ErrForbidden
	}
	return memoryMutation(ctx, s, scope, "question_bank.create", "", input, "read", func() (Bank, error) {
		now := time.Now().UTC()
		b := Bank{ID: uuid.NewString(), TenantID: scope.TenantID, SchoolID: in.SchoolID, Name: in.Name, Description: in.Description, Status: "active", Revision: 1, MetadataSchemaVersion: 1, CreatedBy: scope.ActorID, CreatedAt: now, UpdatedAt: now}
		s.banks[b.ID] = b
		s.schemas[b.ID+":1"] = MetadataSchema{BankID: b.ID, Version: 1, Fields: []MetadataFieldDefinition{}, Taxonomies: []TaxonomyDefinition{}, CreatedBy: scope.ActorID, CreatedAt: now}
		for _, action := range []string{"read", "create", "edit", "manage"} {
			s.acl[aclKey(scope.TenantID, b.ID, scope.ActorID, action)] = true
		}
		return b, nil
	})
}
func (s *MemoryStore) UpdateBank(ctx context.Context, scope auth.AccessScope, id string, in UpdateBankInput) (Bank, error) {
	normalized, err := normalizeBank(CreateBankInput{SchoolID: "00000000-0000-0000-0000-000000000001", Name: in.Name, Description: in.Description})
	if err != nil || in.ExpectedRevision <= 0 || !oneOf(in.Status, "active", "archived") {
		return Bank{}, ErrInvalidInput
	}
	return memoryMutation(ctx, s, scope, "question_bank.update", id, in, "manage", func() (Bank, error) {
		b, err := s.bank(scope, id, "manage")
		if err != nil {
			return b, err
		}
		if b.Revision != in.ExpectedRevision {
			return Bank{}, ErrConflict
		}
		b.Name = normalized.Name
		b.Description = normalized.Description
		b.Status = in.Status
		b.Revision++
		b.UpdatedAt = time.Now().UTC()
		s.banks[id] = b
		return b, nil
	})
}
func (s *MemoryStore) newVersion(scope auth.AccessScope, itemID string, no, schemaVersion int, source *string, content Content) Version {
	now := time.Now().UTC()
	v := Version{ID: uuid.NewString(), TenantID: scope.TenantID, ItemID: itemID, VersionNo: no, SchemaVersion: schemaVersion, Revision: 1, WorkflowStatus: "draft", SourceVersionID: source, AuthorID: scope.ActorID, ContentHash: contentHash(content), HashScope: "draft_content_v1", Content: content, CreatedAt: now, UpdatedAt: now}
	v.Scoring = emptyScoring()
	v.BundleSchemaVersion = 2
	if source != nil {
		v.Scoring = copyScoring(s.versions[*source].Scoring)
	}
	v.BundleHash = bundleHash(content, v.Scoring)
	s.versions[v.ID] = copyVersion(v)
	return copyVersion(v)
}
func (s *MemoryStore) CreateItem(ctx context.Context, scope auth.AccessScope, bankID string, input CreateItemInput) (ItemResult, error) {
	if input.Kind == "" {
		input.Kind = "question"
	}
	if !oneOf(input.Kind, "question", "rubric_template") {
		return ItemResult{}, ErrInvalidInput
	}
	content, err := normalizeContent(input.Content)
	if err != nil || !codePattern.MatchString(input.ItemCode) {
		return ItemResult{}, ErrInvalidInput
	}
	op := "question_bank.item.create"
	if input.Kind == "rubric_template" {
		op = "question_bank.template.create"
	}
	return memoryMutation(ctx, s, scope, op, bankID, input, "create", func() (ItemResult, error) {
		b, err := s.bank(scope, bankID, "create")
		if err != nil {
			return ItemResult{}, err
		}
		if b.Status != "active" {
			return ItemResult{}, ErrLocked
		}
		schema := s.schemas[bankID+":"+strconv.Itoa(b.MetadataSchemaVersion)]
		if err := validationErr(validateContentMetadata(schema, content, true)); err != nil {
			return ItemResult{}, err
		}
		for _, i := range s.items {
			if i.TenantID == scope.TenantID && i.BankID == bankID && i.ItemCode == input.ItemCode {
				return ItemResult{}, ErrConflict
			}
		}
		i := Item{ID: uuid.NewString(), TenantID: scope.TenantID, BankID: bankID, ItemCode: input.ItemCode, SubjectCode: content.Metadata.SubjectCode, GradeScope: content.Metadata.GradeScope, Status: "active", Revision: 1, CreatedBy: scope.ActorID, CreatedAt: time.Now().UTC()}
		i.Kind = input.Kind
		s.items[i.ID] = i
		return ItemResult{Item: i, Version: s.newVersion(scope, i.ID, 1, b.MetadataSchemaVersion, nil, content)}, nil
	})
}
func (s *MemoryStore) CreateVersion(ctx context.Context, scope auth.AccessScope, itemID string, in CreateVersionInput) (Version, error) {
	if !validID(in.SourceVersionID) {
		return Version{}, ErrInvalidInput
	}
	return memoryMutation(ctx, s, scope, "question_bank.version.create", itemID, in, "edit", func() (Version, error) {
		i, err := s.item(scope, itemID, "edit")
		if err != nil {
			return Version{}, err
		}
		b, _ := s.bank(scope, i.BankID, "edit")
		if b.Status != "active" || i.Status != "active" {
			return Version{}, ErrLocked
		}
		source, err := s.version(scope, in.SourceVersionID, "edit")
		if err != nil {
			return Version{}, err
		}
		if source.ItemID != itemID {
			return Version{}, ErrInvalidInput
		}
		no := 1
		for _, v := range s.versions {
			if v.ItemID == itemID && v.VersionNo >= no {
				no = v.VersionNo + 1
			}
		}
		content, err := normalizeContent(source.Content)
		if err != nil {
			return Version{}, err
		}
		return s.newVersion(scope, itemID, no, b.MetadataSchemaVersion, &source.ID, content), nil
	})
}
func (s *MemoryStore) UpdateVersion(ctx context.Context, scope auth.AccessScope, id string, in UpdateVersionInput) (Version, error) {
	content, err := normalizeContent(in.Content)
	if err != nil || in.ExpectedRevision <= 0 {
		return Version{}, ErrInvalidInput
	}
	return memoryMutation(ctx, s, scope, "question_bank.version.update", id, in, "edit", func() (Version, error) {
		v, err := s.version(scope, id, "edit")
		if err != nil {
			return v, err
		}
		i, _ := s.item(scope, v.ItemID, "edit")
		b, _ := s.bank(scope, i.BankID, "edit")
		if b.Status != "active" || i.Status != "active" || v.WorkflowStatus != "draft" {
			return Version{}, ErrLocked
		}
		if err := validationErr(validateContentMetadata(s.schemas[i.BankID+":"+strconv.Itoa(v.SchemaVersion)], content, true)); err != nil {
			return Version{}, err
		}
		if v.Revision != in.ExpectedRevision {
			return Version{}, ErrConflict
		}
		v.Content = content
		v.ContentHash = contentHash(content)
		v.BundleHash = bundleHash(content, v.Scoring)
		v.Revision++
		v.UpdatedAt = time.Now().UTC()
		s.versions[id] = copyVersion(v)
		return copyVersion(v), nil
	})
}
func bounds(total int, f Filter) (int, int) {
	start := min(f.Offset, total)
	return start, min(start+f.Limit, total)
}
func (s *MemoryStore) ListBanks(_ context.Context, scope auth.AccessScope, filter Filter) (BankPage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f := cleanFilter(filter)
	page := BankPage{Banks: []Bank{}, Limit: f.Limit, Offset: f.Offset}
	if !validScope(scope) {
		return page, auth.ErrForbidden
	}
	for _, b := range s.banks {
		_, readErr := s.bank(scope, b.ID, "read")
		_, manageErr := s.bank(scope, b.ID, "manage")
		if (readErr == nil || manageErr == nil) && strings.Contains(strings.ToLower(b.Name+" "+b.Description), strings.ToLower(f.Query)) {
			page.Banks = append(page.Banks, b)
		}
	}
	sort.Slice(page.Banks, func(i, j int) bool {
		if page.Banks[i].CreatedAt.Equal(page.Banks[j].CreatedAt) {
			return page.Banks[i].ID < page.Banks[j].ID
		}
		return page.Banks[i].CreatedAt.After(page.Banks[j].CreatedAt)
	})
	page.Total = len(page.Banks)
	start, end := bounds(page.Total, f)
	page.Banks = page.Banks[start:end]
	return page, nil
}
func (s *MemoryStore) ListItems(_ context.Context, scope auth.AccessScope, bankID string, filter Filter) (ItemPage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f := cleanFilter(filter)
	page := ItemPage{Items: []Item{}, Limit: f.Limit, Offset: f.Offset}
	if _, err := s.bank(scope, bankID, "read"); err != nil {
		return page, err
	}
	for _, i := range s.items {
		kind := f.Kind
		if kind == "" {
			kind = "question"
		}
		if i.Kind == kind && i.BankID == bankID && strings.Contains(strings.ToLower(i.ItemCode), strings.ToLower(f.Query)) {
			page.Items = append(page.Items, i)
		}
	}
	sort.Slice(page.Items, func(i, j int) bool {
		if page.Items[i].CreatedAt.Equal(page.Items[j].CreatedAt) {
			return page.Items[i].ID < page.Items[j].ID
		}
		return page.Items[i].CreatedAt.After(page.Items[j].CreatedAt)
	})
	page.Total = len(page.Items)
	start, end := bounds(page.Total, f)
	page.Items = page.Items[start:end]
	return page, nil
}
func (s *MemoryStore) ListVersions(_ context.Context, scope auth.AccessScope, itemID string, filter Filter) (VersionPage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f := cleanFilter(filter)
	page := VersionPage{Versions: []Version{}, Limit: f.Limit, Offset: f.Offset}
	if _, err := s.item(scope, itemID, "read"); err != nil {
		return page, err
	}
	for _, v := range s.versions {
		if v.ItemID == itemID {
			page.Versions = append(page.Versions, copyVersion(v))
		}
	}
	sort.Slice(page.Versions, func(i, j int) bool { return page.Versions[i].VersionNo > page.Versions[j].VersionNo })
	page.Total = len(page.Versions)
	start, end := bounds(page.Total, f)
	page.Versions = page.Versions[start:end]
	return page, nil
}
