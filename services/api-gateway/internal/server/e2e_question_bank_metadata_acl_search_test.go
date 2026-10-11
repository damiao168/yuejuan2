package server

import (
	"context"
	"errors"
	"net/http"
	"os"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/commandreceipt"
	"edugrade-enterprise/services/api-gateway/internal/files"
	"edugrade-enterprise/services/api-gateway/internal/questionbank"
)

func TestE2EPostgresQuestionBankMetadataACLSearch(t *testing.T) {
	dsn := os.Getenv("EDUGRADE_E2E_DATABASE_URL")
	if dsn == "" {
		t.Skip("requires isolated PostgreSQL")
	}
	db := e2eOpenPostgresTestDB(t, dsn)
	e2eApplyPostgresMigrations(t, db)
	e2eActivatePostgresDemoUsers(t, db, []string{"tenant_admin", "teacher", "school_admin", "grader"})
	router := e2ePostgresRouter(db)
	token := e2eLoginWithTenant(t, router, "demo", "tenant_admin", "ChangeMe123!")
	ownerID := e2eLookupUserID(t, db, "demo", "tenant_admin")
	managerID := e2eLookupUserID(t, db, "demo", "teacher")
	authorID := e2eLookupUserID(t, db, "demo", "school_admin")
	publisherID := e2eLookupUserID(t, db, "demo", "grader")
	var tenantID string
	if err := db.QueryRow(`SELECT tenant_id::text FROM app_user WHERE id=$1`, ownerID).Scan(&tenantID); err != nil {
		t.Fatal(err)
	}
	schoolID := e2eString(t, e2ePostJSON(t, router, http.MethodPost, "/api/v1/schools", token, `{"name":"Metadata ACL school","code":"qb-meta-acl"}`, 201)["school"].(map[string]any), "id")
	store := questionbank.NewPostgresStore(db)
	ctx := context.Background()
	owner := auth.AccessScope{TenantID: tenantID, ActorID: ownerID, TenantWide: true}
	manager := auth.AccessScope{TenantID: tenantID, ActorID: managerID, TenantWide: true}
	author := auth.AccessScope{TenantID: tenantID, ActorID: authorID, TenantWide: true}
	publisher := auth.AccessScope{TenantID: tenantID, ActorID: publisherID, TenantWide: true}
	bank, err := store.CreateBank(commandreceipt.WithID(ctx, "069c-bank"), owner, questionbank.CreateBankInput{SchoolID: schoolID, Name: "Controlled metadata bank"})
	if err != nil {
		t.Fatal(err)
	}
	env := questionBankE2EEnvironment{ctx: ctx, db: db, store: store, scope: owner, router: router, token: token}
	actors := questionBankMetadataActors{manager: manager, author: author, publisher: publisher}
	definition := createQuestionBankMetadataDefinitions(t, env, bank)
	access := assertQuestionBankMetadataVersionAndACL(t, env, bank, actors, definition)
	assetID := assertQuestionBankMetadataSearchAndAsset(t, env, bank, actors, definition, schoolID)
	acl := assertQuestionBankMetadataRevocation(t, env, bank, actors, access, assetID)
	assertQuestionBankMetadataRetirementAndHistory(t, env, bank, actors, acl, definition.second)

}

type questionBankMetadataActors struct{ manager, author, publisher auth.AccessScope }
type questionBankMetadataDefinition struct {
	fields        []questionbank.MetadataFieldDefinition
	taxonomies    []questionbank.TaxonomyDefinition
	first, second questionbank.ItemResult
	oldHash       string
}
type questionBankMetadataAccess struct {
	acl     questionbank.ACLDocument
	derived questionbank.Version
}

func createQuestionBankMetadataDefinitions(t *testing.T, env questionBankE2EEnvironment, bank questionbank.Bank) questionBankMetadataDefinition {
	t.Helper()
	ctx := env.ctx
	store := env.store
	owner := env.scope
	var err error
	min, max, maxLength := 1.0, 5.0, 40
	fields := []questionbank.MetadataFieldDefinition{
		{Key: "curriculum", Label: "课程", Type: "enum", Required: true, Options: []questionbank.MetadataOption{{Value: "national", Label: "国家课程", Active: true}, {Value: "local", Label: "地方课程", Active: true}}},
		{Key: "demand", Label: "认知要求", Type: "number", Required: true, Min: &min, Max: &max},
		{Key: "note", Label: "说明", Type: "string", MaxLength: &maxLength},
		{Key: "topic", Label: "主题", Type: "taxonomy", Required: true, TaxonomyID: "math_topics"},
	}
	taxonomies := []questionbank.TaxonomyDefinition{{ID: "math_topics", Label: "数学主题", Terms: []questionbank.TaxonomyTerm{{ID: "algebra", Label: "代数", Active: true}, {ID: "geometry", Label: "几何", Active: true}}}}
	schema, err := store.UpdateMetadataSchema(commandreceipt.WithID(ctx, "069c-schema-v2"), owner, bank.ID, questionbank.UpdateMetadataSchemaInput{ExpectedRevision: bank.Revision, Fields: fields, Taxonomies: taxonomies})
	if err != nil || schema.Version != 2 {
		t.Fatalf("schema v2: %+v %v", schema, err)
	}
	base := questionbank.Content{QuestionType: "short_answer", AssessmentArchetype: "short_constructed", Stem: "Authorized algebra prompt", Options: []string{}, DefaultScore: 5, KnowledgePoints: []string{"kp.algebra"}, Metadata: questionbank.Metadata{SubjectCode: "mathematics", EducationStage: "junior", GradeScope: "grade_8", DifficultyBand: "medium", CognitiveLevel: "apply", Copyright: "owned", Language: "zh-CN", IntendedUse: "exam"}}
	_, err = store.CreateItem(commandreceipt.WithID(ctx, "069c-invalid"), owner, bank.ID, questionbank.CreateItemInput{ItemCode: "INVALID", Content: base})
	var metadataErr *questionbank.MetadataValidationError
	if !errors.As(err, &metadataErr) || len(metadataErr.FieldErrors) != 3 {
		t.Fatalf("required field errors: %#v %v", metadataErr, err)
	}
	base.CustomMetadata = map[string]any{"curriculum": "national", "demand": 3, "topic": "algebra"}
	first, err := store.CreateItem(commandreceipt.WithID(ctx, "069c-item-1"), owner, bank.ID, questionbank.CreateItemInput{ItemCode: "M-001", Content: base})
	if err != nil {
		t.Fatal(err)
	}
	oldHash := first.Version.ContentHash
	secondContent := base
	secondContent.Stem = "Authorized geometry prompt"
	secondContent.KnowledgePoints = []string{"kp.geometry"}
	secondContent.CustomMetadata = map[string]any{"curriculum": "local", "demand": 4, "topic": "geometry"}
	second, err := store.CreateItem(commandreceipt.WithID(ctx, "069c-item-2"), owner, bank.ID, questionbank.CreateItemInput{ItemCode: "M-002", Content: secondContent})
	if err != nil {
		t.Fatal(err)
	}

	return questionBankMetadataDefinition{fields: fields, taxonomies: taxonomies, first: first, second: second, oldHash: oldHash}

}

func assertQuestionBankMetadataVersionAndACL(t *testing.T, env questionBankE2EEnvironment, bank questionbank.Bank, actors questionBankMetadataActors, definition questionBankMetadataDefinition) questionBankMetadataAccess {
	t.Helper()
	ctx := env.ctx
	store := env.store
	router := env.router
	token := env.token
	owner := env.scope
	manager := actors.manager
	author := actors.author
	managerID := actors.manager.ActorID
	authorID := actors.author.ActorID
	publisherID := actors.publisher.ActorID
	fields := definition.fields
	taxonomies := definition.taxonomies
	first := definition.first
	oldHash := definition.oldHash
	current, err := store.GetBank(ctx, owner, bank.ID)
	if err != nil {
		t.Fatal(err)
	}
	fields = append(fields, questionbank.MetadataFieldDefinition{Key: "term", Label: "学期", Type: "enum", Required: true, Options: []questionbank.MetadataOption{{Value: "first", Label: "第一学期", Active: true}}})
	schema, err := store.UpdateMetadataSchema(commandreceipt.WithID(ctx, "069c-schema-v3"), owner, bank.ID, questionbank.UpdateMetadataSchemaInput{ExpectedRevision: current.Revision, Fields: fields, Taxonomies: taxonomies})
	if err != nil || schema.Version != 3 {
		t.Fatalf("schema v3: %+v %v", schema, err)
	}
	old, err := store.GetVersion(ctx, owner, first.Version.ID)
	if err != nil || old.SchemaVersion != 2 || old.ContentHash != oldHash {
		t.Fatalf("old schema/hash changed: %+v %v", old, err)
	}
	current, err = store.GetBank(ctx, owner, bank.ID)
	if err != nil {
		t.Fatal(err)
	}
	acl, err := store.UpdateACL(commandreceipt.WithID(ctx, "069c-acl"), owner, bank.ID, questionbank.UpdateACLInput{ExpectedRevision: current.Revision, Bindings: []questionbank.ACLBinding{{UserID: publisherID, Preset: "Publisher"}}, Groups: []questionbank.ACLGroup{{Name: "Authors", Preset: "Author", MemberIDs: []string{authorID}}, {Name: "Managers", Preset: "Manager", MemberIDs: []string{managerID}}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.GetBank(ctx, manager, bank.ID); err != nil {
		t.Fatalf("manager cannot read bank structure: %v", err)
	}
	if _, err = store.GetACL(ctx, manager, bank.ID); err != nil {
		t.Fatalf("manager structure access: %v", err)
	}
	if _, err = store.GetVersion(ctx, manager, first.Version.ID); !errors.Is(err, questionbank.ErrNotFound) {
		t.Fatalf("manager inherited content read: %v", err)
	}
	derived, err := store.CreateVersion(commandreceipt.WithID(ctx, "069c-derived"), author, first.Item.ID, questionbank.CreateVersionInput{SourceVersionID: first.Version.ID})
	if err != nil || derived.SchemaVersion != 3 {
		t.Fatalf("derived version: %+v %v", derived, err)
	}
	invalidSubmit := qbRequest(t, router, questionBankRequest{Method: http.MethodPost, Path: "/api/v1/question-bank/versions/" + derived.ID + "/submit-review", Token: token, Key: "069c-invalid-submit", Body: qbJSON(t, questionbank.ReviewInput{ExpectedRevision: derived.Revision, BundleHash: derived.BundleHash}), Want: 400})
	if invalidSubmit["field_errors"].(map[string]any)["custom_metadata.term"] == nil {
		t.Fatalf("publish validation lacks field path: %#v", invalidSubmit)
	}
	derived.CustomMetadata["term"] = "first"
	derived, err = store.UpdateVersion(commandreceipt.WithID(ctx, "069c-fill-required"), author, derived.ID, questionbank.UpdateVersionInput{ExpectedRevision: derived.Revision, Content: derived.Content})
	if err != nil {
		t.Fatal(err)
	}

	return questionBankMetadataAccess{acl: acl, derived: derived}

}

func assertQuestionBankMetadataSearchAndAsset(t *testing.T, env questionBankE2EEnvironment, bank questionbank.Bank, actors questionBankMetadataActors, definition questionBankMetadataDefinition, schoolID string) string {
	t.Helper()
	ctx := env.ctx
	db := env.db
	store := env.store
	owner := env.scope
	tenantID := env.scope.TenantID
	ownerID := env.scope.ActorID
	publisher := actors.publisher
	first := definition.first
	page, err := store.SearchItems(ctx, owner, questionbank.SearchFilter{BankID: bank.ID, SubjectCode: "mathematics", KnowledgePoint: "kp.algebra", QuestionType: "short_answer", WorkflowStatus: "draft", DifficultyBand: "medium", IntendedUse: "exam", MetadataKey: "curriculum", MetadataValue: "national", Mode: "all", Sort: "item_code_asc", Limit: 1})
	if err != nil || page.Total != 2 || len(page.Items) != 1 || page.Items[0].Item.ItemCode != "M-001" {
		t.Fatalf("search page one: %+v %v", page, err)
	}
	page2, err := store.SearchItems(ctx, owner, questionbank.SearchFilter{BankID: bank.ID, KnowledgePoint: "kp.algebra", Mode: "all", Sort: "item_code_asc", Limit: 1, Offset: 1})
	if err != nil || page2.Total != 2 || len(page2.Items) != 1 || page2.Items[0].Version.ID == page.Items[0].Version.ID {
		t.Fatalf("stable page two: %+v %v", page2, err)
	}
	var assetID string
	if err = db.QueryRow(`INSERT INTO file_asset(tenant_id,school_id,owner_type,owner_id,original_name,content_type,size_bytes,hash_sha256,storage_bucket,storage_key,visibility,uploaded_by) VALUES($1,$2,'paper',$2,'metadata.txt','text/plain',7,repeat('c',64),'fixture','fixture/metadata.txt','private',$3) RETURNING id::text`, tenantID, schoolID, ownerID).Scan(&assetID); err != nil {
		t.Fatal(err)
	}
	scoring := questionbank.Scoring{Assets: []questionbank.Asset{{FileAssetID: assetID, SHA256: "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc", Name: "metadata.txt", ContentType: "text/plain"}}, UsePolicy: "practice_only"}
	first.Version, err = store.UpdateScoring(commandreceipt.WithID(ctx, "069c-asset"), owner, first.Version.ID, questionbank.UpdateScoringInput{ExpectedRevision: first.Version.Revision, Scoring: scoring})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = files.NewPostgresStore(db).GetScoped(ctx, publisher, assetID); err != nil {
		t.Fatalf("authorized asset: %v", err)
	}

	return assetID

}

func assertQuestionBankMetadataRevocation(t *testing.T, env questionBankE2EEnvironment, bank questionbank.Bank, actors questionBankMetadataActors, access questionBankMetadataAccess, assetID string) questionbank.ACLDocument {
	t.Helper()
	ctx := env.ctx
	db := env.db
	store := env.store
	manager := actors.manager
	author := actors.author
	publisher := actors.publisher
	managerID := actors.manager.ActorID
	acl := access.acl
	derived := access.derived
	var err error
	acl, err = store.UpdateACL(commandreceipt.WithID(ctx, "069c-revoke"), manager, bank.ID, questionbank.UpdateACLInput{ExpectedRevision: acl.Revision, Groups: []questionbank.ACLGroup{{Name: "Managers", Preset: "Manager", MemberIDs: []string{managerID}}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.GetVersion(ctx, author, derived.ID); !errors.Is(err, questionbank.ErrNotFound) {
		t.Fatalf("group revocation not immediate: %v", err)
	}
	if _, err = files.NewPostgresStore(db).GetScoped(ctx, publisher, assetID); !errors.Is(err, files.ErrForbidden) {
		t.Fatalf("asset revocation not immediate: %v", err)
	}
	page, err := store.SearchItems(ctx, publisher, questionbank.SearchFilter{BankID: bank.ID, Mode: "all", Limit: 20})
	if err != nil || page.Total != 0 {
		t.Fatalf("revoked search leaked count: %+v %v", page, err)
	}

	return acl

}

func assertQuestionBankMetadataRetirementAndHistory(t *testing.T, env questionBankE2EEnvironment, bank questionbank.Bank, actors questionBankMetadataActors, acl questionbank.ACLDocument, second questionbank.ItemResult) {
	t.Helper()
	ctx := env.ctx
	db := env.db
	store := env.store
	tenantID := env.scope.TenantID
	manager := actors.manager
	publisher := actors.publisher
	managerID := actors.manager.ActorID
	publisherID := actors.publisher.ActorID
	var err error
	_, err = store.UpdateACL(commandreceipt.WithID(ctx, "069c-regrant"), manager, bank.ID, questionbank.UpdateACLInput{ExpectedRevision: acl.Revision, Bindings: []questionbank.ACLBinding{{UserID: publisherID, Preset: "Publisher"}}, Groups: []questionbank.ACLGroup{{Name: "Managers", Preset: "Manager", MemberIDs: []string{managerID}}}})
	if err != nil {
		t.Fatal(err)
	}
	retired, err := store.RetireItem(commandreceipt.WithID(ctx, "069c-retire"), publisher, second.Item.ID, questionbank.RetireItemInput{ExpectedRevision: second.Item.Revision})
	if err != nil || retired.Status != "retired" {
		t.Fatalf("retire: %+v %v", retired, err)
	}
	var auditCount int
	if err = db.QueryRow(`SELECT count(*) FROM audit_log WHERE tenant_id=$1 AND action IN ('question_bank.metadata_schema.update','question_bank.acl.update','question_bank.item.retire')`, tenantID).Scan(&auditCount); err != nil || auditCount < 5 {
		t.Fatalf("audit trail count=%d err=%v", auditCount, err)
	}
	if _, err = db.Exec(`DELETE FROM question_bank_metadata_schema WHERE tenant_id=$1 AND bank_id=$2 AND version=2`, tenantID, bank.ID); err == nil {
		t.Fatal("historical schema deletion accepted")
	}
}
