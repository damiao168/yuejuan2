package questionbank

import (
	"context"
	"errors"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/commandreceipt"
	"github.com/google/uuid"
)

func TestControlledMetadataSchemaValidationAndHistory(t *testing.T) {
	s := NewMemoryStore()
	ctx := context.Background()
	owner := auth.AccessScope{TenantID: uuid.NewString(), ActorID: uuid.NewString(), TenantWide: true}
	bank, err := s.CreateBank(commandreceipt.WithID(ctx, "bank"), owner, CreateBankInput{SchoolID: uuid.NewString(), Name: "Controlled bank"})
	if err != nil {
		t.Fatal(err)
	}
	item, err := s.CreateItem(commandreceipt.WithID(ctx, "item"), owner, bank.ID, CreateItemInput{ItemCode: "M1", Content: fixtureContent()})
	if err != nil {
		t.Fatal(err)
	}
	oldHash := item.Version.ContentHash
	maxLength := 20
	min, max := 1.0, 5.0
	fields := []MetadataFieldDefinition{
		{Key: "curriculum", Label: "课程", Type: "enum", Required: true, Options: []MetadataOption{{Value: "national", Label: "国家课程", Active: true}}},
		{Key: "demand", Label: "认知要求", Type: "number", Required: true, Min: &min, Max: &max},
		{Key: "note", Label: "说明", Type: "string", MaxLength: &maxLength},
		{Key: "topic", Label: "主题", Type: "taxonomy", Required: true, TaxonomyID: "math_topics"},
	}
	taxonomies := []TaxonomyDefinition{{ID: "math_topics", Label: "数学主题", Terms: []TaxonomyTerm{{ID: "algebra", Label: "代数", Active: true}}}}
	schema, err := s.UpdateMetadataSchema(commandreceipt.WithID(ctx, "schema"), owner, bank.ID, UpdateMetadataSchemaInput{ExpectedRevision: bank.Revision, Fields: fields, Taxonomies: taxonomies})
	if err != nil || schema.Version != 2 {
		t.Fatalf("schema: %+v %v", schema, err)
	}
	old, err := s.GetVersion(ctx, owner, item.Version.ID)
	if err != nil || old.SchemaVersion != 1 || old.ContentHash != oldHash {
		t.Fatalf("old version changed: %+v %v", old, err)
	}
	draft, err := s.CreateVersion(commandreceipt.WithID(ctx, "derive"), owner, item.Item.ID, CreateVersionInput{SourceVersionID: old.ID})
	if err != nil || draft.SchemaVersion != 2 {
		t.Fatalf("derived schema: %+v %v", draft, err)
	}
	_, err = s.UpdateVersion(commandreceipt.WithID(ctx, "invalid"), owner, draft.ID, UpdateVersionInput{ExpectedRevision: draft.Revision, Content: draft.Content})
	var validation *MetadataValidationError
	if !errors.As(err, &validation) || len(validation.FieldErrors) != 3 {
		t.Fatalf("field errors: %#v %v", validation, err)
	}
	draft.CustomMetadata = map[string]any{"curriculum": "national", "demand": 6, "topic": "missing"}
	_, err = s.UpdateVersion(commandreceipt.WithID(ctx, "invalid-values"), owner, draft.ID, UpdateVersionInput{ExpectedRevision: draft.Revision, Content: draft.Content})
	if !errors.As(err, &validation) || validation.FieldErrors["custom_metadata.demand"] == nil || validation.FieldErrors["custom_metadata.topic"] == nil {
		t.Fatalf("typed errors: %#v %v", validation, err)
	}
	draft.CustomMetadata = map[string]any{"curriculum": "national", "demand": 3, "topic": "algebra"}
	updated, err := s.UpdateVersion(commandreceipt.WithID(ctx, "valid"), owner, draft.ID, UpdateVersionInput{ExpectedRevision: draft.Revision, Content: draft.Content})
	if err != nil || updated.CustomMetadata["topic"] != "algebra" {
		t.Fatalf("valid metadata: %+v %v", updated, err)
	}
	result, err := s.ValidateMetadata(ctx, owner, bank.ID, ValidateMetadataInput{SchemaVersion: 2, Values: map[string]any{"curriculum": "national", "demand": 2, "topic": "algebra"}})
	if err != nil || !result.Valid {
		t.Fatalf("validate endpoint: %+v %v", result, err)
	}
}

func TestKnowledgePointsUseStableTaxonomyIDsWhenConfigured(t *testing.T) {
	schema := MetadataSchema{Taxonomies: []TaxonomyDefinition{{ID: "knowledge_points", Label: "知识点", Terms: []TaxonomyTerm{{ID: "algebra.linear", Label: "一次方程", Active: true}, {ID: "legacy.hidden", Label: "旧知识点", Active: false}}}}}
	content := fixtureContent()
	content.KnowledgePoints = []string{"free-text"}
	result := validateContentMetadata(schema, content, true)
	if result.Valid || result.FieldErrors["knowledge_points"] == nil {
		t.Fatalf("free knowledge point accepted: %+v", result)
	}
	content.KnowledgePoints = []string{"algebra.linear"}
	if result = validateContentMetadata(schema, content, true); !result.Valid {
		t.Fatalf("stable taxonomy id rejected: %+v", result)
	}
}

func TestACLPresetsGroupRevocationSearchAndRetire(t *testing.T) {
	s := NewMemoryStore()
	ctx := context.Background()
	owner := auth.AccessScope{TenantID: uuid.NewString(), ActorID: uuid.NewString(), TenantWide: true}
	managerID, authorID, publisherID := uuid.NewString(), uuid.NewString(), uuid.NewString()
	bank, err := s.CreateBank(commandreceipt.WithID(ctx, "bank"), owner, CreateBankInput{SchoolID: uuid.NewString(), Name: "Private"})
	if err != nil {
		t.Fatal(err)
	}
	item, err := s.CreateItem(commandreceipt.WithID(ctx, "item"), owner, bank.ID, CreateItemInput{ItemCode: "A1", Content: fixtureContent()})
	if err != nil {
		t.Fatal(err)
	}
	doc, err := s.UpdateACL(commandreceipt.WithID(ctx, "acl"), owner, bank.ID, UpdateACLInput{ExpectedRevision: bank.Revision, Bindings: []ACLBinding{{UserID: publisherID, Preset: "Publisher"}}, Groups: []ACLGroup{{Name: "authors", Preset: "Author", MemberIDs: []string{authorID}}, {Name: "managers", Preset: "Manager", MemberIDs: []string{managerID}}}})
	if err != nil {
		t.Fatal(err)
	}
	manager := auth.AccessScope{TenantID: owner.TenantID, ActorID: managerID, TenantWide: true}
	if _, err = s.GetBank(ctx, manager, bank.ID); err != nil {
		t.Fatalf("manager cannot read bank structure: %v", err)
	}
	if _, err = s.GetACL(ctx, manager, bank.ID); err != nil {
		t.Fatalf("manager cannot manage ACL: %v", err)
	}
	if _, err = s.GetVersion(ctx, manager, item.Version.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("manager inherited content read: %v", err)
	}
	author := auth.AccessScope{TenantID: owner.TenantID, ActorID: authorID, TenantWide: true}
	page, err := s.SearchItems(ctx, author, SearchFilter{BankID: bank.ID, Mode: "my_drafts", Limit: 1})
	if err != nil || page.Total != 0 {
		t.Fatalf("authors only see their own draft: %+v %v", page, err)
	}
	if _, err = s.GetVersion(ctx, author, item.Version.ID); err != nil {
		t.Fatalf("group read: %v", err)
	}
	_, err = s.UpdateACL(commandreceipt.WithID(ctx, "revoke"), manager, bank.ID, UpdateACLInput{ExpectedRevision: doc.Revision, Bindings: []ACLBinding{{UserID: publisherID, Preset: "Publisher"}}, Groups: []ACLGroup{{Name: "managers", Preset: "Manager", MemberIDs: []string{managerID}}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.GetVersion(ctx, author, item.Version.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("revocation not immediate: %v", err)
	}
	publisher := auth.AccessScope{TenantID: owner.TenantID, ActorID: publisherID, TenantWide: true}
	retired, err := s.RetireItem(commandreceipt.WithID(ctx, "retire"), publisher, item.Item.ID, RetireItemInput{ExpectedRevision: item.Item.Revision})
	if err != nil || retired.Status != "retired" {
		t.Fatalf("retire: %+v %v", retired, err)
	}
	page, err = s.SearchItems(ctx, publisher, SearchFilter{BankID: bank.ID, Mode: "all", Limit: 20})
	if err != nil || page.Total != 0 {
		t.Fatalf("retired item remained searchable: %+v %v", page, err)
	}
}
