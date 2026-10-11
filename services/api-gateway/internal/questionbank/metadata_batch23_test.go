package questionbank

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/commandreceipt"
	"github.com/google/uuid"
)

func TestBatch23SchemaPreservesFirstErrorBeforeSorting(t *testing.T) {
	validTaxonomy := TaxonomyDefinition{ID: "topics", Label: "Topics", Terms: []TaxonomyTerm{{ID: "term", Label: "Term"}}}
	for _, tc := range []struct {
		name       string
		fields     []MetadataFieldDefinition
		taxonomies []TaxonomyDefinition
		path, text string
	}{
		{"taxonomy_before_field", []MetadataFieldDefinition{{Key: "subject_code"}}, []TaxonomyDefinition{{ID: "topics"}}, "taxonomies.0", "taxonomy id, label, and terms are required"},
		{"duplicate_taxonomy_before_terms", nil, []TaxonomyDefinition{validTaxonomy, {ID: "topics", Label: "Duplicate", Terms: []TaxonomyTerm{{ID: "invalid-id"}}}}, "taxonomies.topics", "taxonomy id must be unique"},
		{"input_field_before_sorted_field", []MetadataFieldDefinition{{Key: "z", Label: "Z", Type: "string"}, {Key: "a", Label: "A", Type: "invalid"}}, nil, "fields.z", "string max_length must be between 1 and 2000"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fields, taxonomies, err := normalizeSchema(tc.fields, tc.taxonomies)
			var validation *MetadataValidationError
			want := map[string][]string{tc.path: {tc.text}}
			if fields != nil || taxonomies != nil || !errors.As(err, &validation) || !reflect.DeepEqual(validation.FieldErrors, want) {
				t.Fatalf("fields=%#v taxonomies=%#v error=%#v", fields, taxonomies, err)
			}
		})
	}
}

func TestBatch23SchemaSortsNormalizedOuterCopies(t *testing.T) {
	fields := []MetadataFieldDefinition{{Key: " z ", Label: " Z ", Type: "boolean"}, {Key: "a", Label: "A", Type: "boolean"}}
	taxonomies := []TaxonomyDefinition{
		{ID: " z ", Label: " Z ", Terms: []TaxonomyTerm{{ID: " term ", Label: " Term "}}},
		{ID: "a", Label: "A", Terms: []TaxonomyTerm{{ID: "term", Label: "Term"}}},
	}
	gotFields, gotTaxonomies, err := normalizeSchema(fields, taxonomies)
	if err != nil {
		t.Fatal(err)
	}
	if gotFields[0].Key != "a" || gotFields[1].Key != "z" || gotTaxonomies[0].ID != "a" || gotTaxonomies[1].ID != "z" || gotTaxonomies[1].Terms[0].ID != "term" {
		t.Fatalf("fields=%#v taxonomies=%#v", gotFields, gotTaxonomies)
	}
	// 只核对原有外层复制边界，不把内嵌 Options/Terms 描述为深拷贝。
	if fields[0].Key != " z " || taxonomies[0].ID != " z " {
		t.Fatal("normalization changed caller outer definitions")
	}
}

func TestBatch23MetadataAccumulatesUnknownRequiredAndTypedErrors(t *testing.T) {
	maxLength, min, max := 3, 1.0, 5.0
	schema := MetadataSchema{
		Fields: []MetadataFieldDefinition{
			{Key: "note", Type: "string", MaxLength: &maxLength},
			{Key: "demand", Type: "number", Min: &min, Max: &max},
			{Key: "flag", Type: "boolean"},
			{Key: "choice", Type: "enum", Options: []MetadataOption{{Value: "disabled"}, {Value: "enabled", Active: true}}},
			{Key: "topic", Type: "taxonomy", TaxonomyID: "topics"},
			{Key: "missing", Type: "string", Required: true, MaxLength: &maxLength},
		},
		Taxonomies: []TaxonomyDefinition{{ID: "topics", Terms: []TaxonomyTerm{{ID: "hidden"}, {ID: "active", Active: true}}}},
	}
	values := map[string]any{"unknown": true, "note": 123, "demand": 6, "flag": "yes", "choice": "disabled", "topic": "hidden"}
	got := validateMetadataValues(schema, values, true)
	want := map[string][]string{
		"custom_metadata.unknown": {"field is not declared by the bound metadata schema"},
		"custom_metadata.note":    {"value must be a bounded string"},
		"custom_metadata.demand":  {"value must be a number within the declared range"},
		"custom_metadata.flag":    {"value must be a boolean"},
		"custom_metadata.choice":  {"value must be an active declared option"},
		"custom_metadata.topic":   {"value must be an active stable taxonomy id"},
		"custom_metadata.missing": {"value is required"},
	}
	if got.Valid || !reflect.DeepEqual(got.FieldErrors, want) {
		t.Fatalf("validation=%#v, want=%#v", got, want)
	}
	if optional := validateMetadataValues(schema, map[string]any{"note": ""}, false); !optional.Valid {
		t.Fatalf("optional missing values=%#v", optional)
	}
	valid := map[string]any{"note": "ok", "demand": 3, "flag": false, "choice": "enabled", "topic": "active", "missing": "ok"}
	if got = validateMetadataValues(schema, valid, true); !got.Valid || len(got.FieldErrors) != 0 {
		t.Fatalf("valid values=%#v", got)
	}
}

func batch23MemoryBank() (*MemoryStore, auth.AccessScope, Bank) {
	s := NewMemoryStore()
	scope := auth.AccessScope{TenantID: uuid.NewString(), ActorID: uuid.NewString(), TenantWide: true}
	bank := Bank{ID: uuid.NewString(), TenantID: scope.TenantID, SchoolID: uuid.NewString(), CreatedBy: uuid.NewString(), Revision: 3, Status: "active"}
	s.banks[bank.ID] = bank
	s.acl[aclKey(scope.TenantID, bank.ID, scope.ActorID, "manage")] = true
	s.acl[aclKey(scope.TenantID, bank.ID, scope.ActorID, "read")] = true
	return s, scope, bank
}

func TestBatch23ACLReplacementPreservesProtectedAndUnrelatedGrants(t *testing.T) {
	s, scope, bank := batch23MemoryBank()
	creatorKeys := []string{}
	for _, action := range []string{"read", "edit", "manage"} {
		key := aclKey(scope.TenantID, bank.ID, bank.CreatedBy, action)
		creatorKeys = append(creatorKeys, key)
		s.acl[key] = true
	}
	revokedKey := aclKey(scope.TenantID, bank.ID, uuid.NewString(), "read")
	s.acl[revokedKey] = true
	unrelatedKeys := []string{
		aclKey(scope.TenantID, uuid.NewString(), scope.ActorID, "read"),
		aclKey(uuid.NewString(), bank.ID, scope.ActorID, "read"),
		scope.TenantID + ":" + bank.ID + ":legacy",
	}
	for _, key := range unrelatedKeys {
		s.acl[key] = true
	}
	viewer, member := uuid.NewString(), uuid.NewString()
	in := UpdateACLInput{ExpectedRevision: bank.Revision, Bindings: []ACLBinding{{UserID: viewer, Preset: "Viewer"}}, Groups: []ACLGroup{{Name: "authors", Preset: "Author", MemberIDs: []string{member}}}}
	ctx := commandreceipt.WithID(context.Background(), "batch23-acl")
	got, err := s.UpdateACL(ctx, scope, bank.ID, in)
	if err != nil || got.Revision != 4 || len(got.Bindings) != 2 || len(got.Groups) != 1 || got.Groups[0].ID == "" {
		t.Fatalf("ACL=%#v error=%v", got, err)
	}
	for _, key := range append(creatorKeys, unrelatedKeys...) {
		if !s.acl[key] {
			t.Fatalf("protected grant removed: %s", key)
		}
	}
	if !s.acl[aclKey(scope.TenantID, bank.ID, scope.ActorID, "manage")] || !s.acl[aclKey(scope.TenantID, bank.ID, scope.ActorID, "read")] || s.acl[revokedKey] {
		t.Fatal("replacement changed operator protection or retained another user's revoked read")
	}
	if !s.acl[aclKey(scope.TenantID, bank.ID, viewer, "statistics")] || !s.acl[aclKey(scope.TenantID, bank.ID, member, "edit")] {
		t.Fatal("binding or group did not apply preset actions")
	}
	if replay, err := s.UpdateACL(ctx, scope, bank.ID, in); err != nil || !reflect.DeepEqual(replay, got) {
		t.Fatalf("replay=%#v error=%v", replay, err)
	}
	// 重放发生在 manage 撤权之后；已保存的 ACL 结果也不能再次返回。
	delete(s.acl, aclKey(scope.TenantID, bank.ID, scope.ActorID, "manage"))
	if replay, err := s.UpdateACL(ctx, scope, bank.ID, in); !errors.Is(err, ErrNotFound) || !reflect.DeepEqual(replay, ACLDocument{}) {
		t.Fatalf("revoked replay=%#v error=%v", replay, err)
	}
}

func TestBatch23ACLRevisionConflictPrecedesArchivedStatus(t *testing.T) {
	s, scope, bank := batch23MemoryBank()
	bank.Status = "archived"
	s.banks[bank.ID] = bank
	for _, tc := range []struct {
		revision int64
		want     error
	}{{2, ErrConflict}, {3, ErrLocked}} {
		got, err := s.UpdateACL(context.Background(), scope, bank.ID, UpdateACLInput{ExpectedRevision: tc.revision})
		if !errors.Is(err, tc.want) || !reflect.DeepEqual(got, ACLDocument{}) || s.banks[bank.ID].Revision != 3 {
			t.Fatalf("ACL=%#v error=%v revision=%d", got, err, s.banks[bank.ID].Revision)
		}
	}
}

func batch23SearchIDs(page SearchPage) []string {
	ids := []string{}
	for _, item := range page.Items {
		ids = append(ids, item.Version.ID)
	}
	return ids
}

func TestBatch23SearchPreservesVisibilityFiltersOrderingAndTotal(t *testing.T) {
	fixture := questionBankVisibilitySearchFixture()
	s, scope := fixture.store, fixture.scope
	publishedID, draftID, otherDraftID, secondItemID := fixture.publishedID, fixture.draftID, fixture.otherDraftID, fixture.secondItemID
	for _, tc := range []struct {
		mode string
		want []string
	}{
		{"default", []string{publishedID, draftID, secondItemID}},
		{"published", []string{publishedID}},
		{"my_drafts", []string{draftID, secondItemID}},
		{"all", []string{publishedID, draftID, otherDraftID, secondItemID}},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			page, err := s.SearchItems(context.Background(), scope, SearchFilter{Mode: tc.mode, Limit: 20})
			if err != nil || page.Total != len(tc.want) || !reflect.DeepEqual(batch23SearchIDs(page), tc.want) {
				t.Fatalf("page=%#v error=%v", page, err)
			}
		})
	}
	assertQuestionBankSearchOrderingFiltersAndCopy(t, fixture)
}

type questionBankSearchFixture struct {
	store                                            *MemoryStore
	scope                                            auth.AccessScope
	bank                                             Bank
	publishedID, draftID, otherDraftID, secondItemID string
}

func questionBankVisibilitySearchFixture() questionBankSearchFixture {
	s, scope, bank := batch23MemoryBank()
	publishedID := "00000000-0000-0000-0000-000000000201"
	draftID := "00000000-0000-0000-0000-000000000202"
	otherDraftID := "00000000-0000-0000-0000-000000000203"
	secondItemID := "00000000-0000-0000-0000-000000000204"
	item := Item{ID: uuid.NewString(), TenantID: scope.TenantID, BankID: bank.ID, Kind: "question", Status: "active", ItemCode: "B", SubjectCode: "mathematics", CurrentPublishedVersionID: &publishedID}
	second := Item{ID: uuid.NewString(), TenantID: scope.TenantID, BankID: bank.ID, Kind: "question", Status: "active", ItemCode: "A", SubjectCode: "physics"}
	privateBank := bank
	privateBank.ID = uuid.NewString()
	s.banks[privateBank.ID] = privateBank
	s.acl[aclKey(scope.TenantID, privateBank.ID, scope.ActorID, "manage")] = true
	private := Item{ID: uuid.NewString(), TenantID: scope.TenantID, BankID: privateBank.ID, Kind: "question", Status: "active", ItemCode: "0"}
	retired := Item{ID: uuid.NewString(), TenantID: scope.TenantID, BankID: bank.ID, Kind: "question", Status: "retired", ItemCode: "0"}
	for _, i := range []Item{item, second, private, retired} {
		s.items[i.ID] = i
	}
	// 同一题目混合三种作者/发布状态，另放仅 manage 的题库和已停用条目，检查筛选后总数。
	versions := []Version{
		{ID: publishedID, ItemID: item.ID, VersionNo: 1, WorkflowStatus: "published", AuthorID: "other"},
		{ID: draftID, ItemID: item.ID, VersionNo: 2, WorkflowStatus: "draft", AuthorID: scope.ActorID},
		{ID: otherDraftID, ItemID: item.ID, VersionNo: 3, WorkflowStatus: "draft", AuthorID: "other"},
		{ID: secondItemID, ItemID: second.ID, VersionNo: 1, WorkflowStatus: "draft", AuthorID: scope.ActorID},
		{ID: uuid.NewString(), ItemID: private.ID, VersionNo: 1, WorkflowStatus: "draft", AuthorID: scope.ActorID},
		{ID: uuid.NewString(), ItemID: retired.ID, VersionNo: 1, WorkflowStatus: "draft", AuthorID: scope.ActorID},
	}
	for index, v := range versions {
		v.TenantID, v.Content = scope.TenantID, fixtureContent()
		v.UpdatedAt, v.CreatedAt = time.Unix(100, 0), time.Unix(int64(100+index), 0)
		v.Metadata.DifficultyBand, v.Metadata.CognitiveLevel = "medium", "apply"
		v.Metadata.Copyright, v.Metadata.IntendedUse = "owned", "practice"
		v.CustomMetadata, v.Scoring.UsePolicy = map[string]any{"tier": 2}, "reusable"
		if v.ID == draftID {
			v.Stem = "Target rule"
		}
		s.versions[v.ID] = v
	}
	return questionBankSearchFixture{s, scope, bank, publishedID, draftID, otherDraftID, secondItemID}
}

func assertQuestionBankSearchOrderingFiltersAndCopy(t *testing.T, fixture questionBankSearchFixture) {
	t.Helper()
	s, scope, bank := fixture.store, fixture.scope, fixture.bank
	publishedID, draftID, otherDraftID, secondItemID := fixture.publishedID, fixture.draftID, fixture.otherDraftID, fixture.secondItemID
	page, err := s.SearchItems(context.Background(), scope, SearchFilter{Mode: "all", Sort: "item_code_asc", Limit: 2, Offset: 1})
	if err != nil || page.Total != 4 || !reflect.DeepEqual(batch23SearchIDs(page), []string{otherDraftID, draftID}) {
		t.Fatalf("paged item-code order=%#v error=%v", page, err)
	}
	page.Items[1].Version.Options[0] = "changed"
	if s.versions[draftID].Options[0] != "2" {
		t.Fatal("search result aliases stored version")
	}
	page, err = s.SearchItems(context.Background(), scope, SearchFilter{Mode: "all", Sort: "created_desc", Limit: 20})
	if err != nil || !reflect.DeepEqual(batch23SearchIDs(page), []string{secondItemID, otherDraftID, draftID, publishedID}) {
		t.Fatalf("created order=%#v error=%v", page, err)
	}
	f := SearchFilter{Mode: "all", BankID: bank.ID, Query: "TARGET", SubjectCode: "mathematics", KnowledgePoint: "integers", QuestionType: "single_choice", Archetype: "selected_response", WorkflowStatus: "draft", DifficultyBand: "medium", CognitiveLevel: "apply", Copyright: "owned", IntendedUse: "practice", UsePolicy: "reusable", MetadataKey: "tier", MetadataValue: "2", Limit: 20}
	page, err = s.SearchItems(context.Background(), scope, f)
	if err != nil || page.Total != 1 || !reflect.DeepEqual(batch23SearchIDs(page), []string{draftID}) {
		t.Fatalf("combined filters=%#v error=%v", page, err)
	}
	available := true
	f.StatisticsAvailable = &available
	page, err = s.SearchItems(context.Background(), scope, f)
	if err != nil || page.Total != 0 {
		t.Fatalf("statistics filter=%#v error=%v", page, err)
	}
	page, err = s.SearchItems(context.Background(), scope, SearchFilter{Offset: -1})
	if !errors.Is(err, ErrInvalidInput) || page.Limit != 50 || page.Offset != -1 || page.Items == nil {
		t.Fatalf("invalid filter page=%#v error=%v", page, err)
	}
}
