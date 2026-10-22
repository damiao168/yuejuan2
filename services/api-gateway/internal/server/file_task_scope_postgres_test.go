package server

import (
	"database/sql"
	"errors"
	"os"
	"strings"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/commandreceipt"
	"edugrade-enterprise/services/api-gateway/internal/files"
	"edugrade-enterprise/services/api-gateway/internal/org"
	"edugrade-enterprise/services/api-gateway/internal/questionbank"
	"github.com/google/uuid"
)

// 任务按文件清单读取时没有学校用户 Actor；真实 PostgreSQL 同时验证空 UUID 和题库 ACL 的拒绝边界。
func TestE2EPostgresAssignedFileScopeWithoutActorKeepsBankACL(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("EDUGRADE_E2E_DATABASE_URL"))
	if dsn == "" {
		t.Skip("EDUGRADE_E2E_DATABASE_URL is required for the isolated PostgreSQL fixture")
	}
	db := e2eOpenPostgresTestDB(t, dsn)
	e2eApplyPostgresMigrations(t, db)
	e2eActivatePostgresDemoUsers(t, db, []string{"tenant_admin"})
	ctx := t.Context()
	actor := e2eLookupUserID(t, db, "demo", "tenant_admin")
	var tenant string
	if err := db.QueryRowContext(ctx, `SELECT tenant_id::text FROM app_user WHERE id=$1::uuid`, actor).Scan(&tenant); err != nil {
		t.Fatal(err)
	}
	school, err := org.NewPostgresStore(db).CreateSchool(ctx, tenant, org.School{Name: "Task file scope fixture", Code: "task-file-scope"})
	if err != nil {
		t.Fatal(err)
	}
	store := files.NewPostgresStore(db)
	asset, err := store.Create(ctx, files.CreateAssetInput{
		TenantID: tenant, SchoolID: school.ID, OwnerType: "capture_batch", OwnerID: uuid.NewString(),
		OriginalName: "capture.png", ContentType: "image/png", SizeBytes: 7, HashSHA256: strings.Repeat("c", 64),
		StorageBucket: "fixture", StorageKey: "fixture/capture.png", Visibility: "private", UploadedBy: actor,
	})
	if err != nil {
		t.Fatal(err)
	}
	taskScope := auth.AccessScope{TenantID: tenant, AssignedOnly: true, FileIDs: []string{asset.ID}}
	readers := []struct {
		name string
		read func(auth.AccessScope) (files.FileAsset, error)
	}{
		{"direct", func(scope auth.AccessScope) (files.FileAsset, error) { return store.GetScoped(ctx, scope, asset.ID) }},
		{"transaction", func(scope auth.AccessScope) (files.FileAsset, error) {
			tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: false})
			if err != nil {
				return files.FileAsset{}, err
			}
			defer tx.Rollback()
			return store.GetScopedTx(ctx, tx, scope, asset.ID)
		}},
	}
	for _, reader := range readers {
		t.Run(reader.name+"/ordinary_capture_without_actor", func(t *testing.T) {
			got, err := reader.read(taskScope)
			if err != nil || got.ID != asset.ID {
				t.Fatalf("assigned capture read: asset=%+v error=%v", got, err)
			}
			unassigned := taskScope
			unassigned.FileIDs = nil
			if _, err := reader.read(unassigned); !errors.Is(err, files.ErrForbidden) {
				t.Fatalf("unassigned capture read must be forbidden, got %v", err)
			}
		})
	}

	ownerScope := auth.AccessScope{TenantID: tenant, ActorID: actor, TenantWide: true}
	bankStore := questionbank.NewPostgresStore(db)
	bank, err := bankStore.CreateBank(commandreceipt.WithID(ctx, "task-file-bank"), ownerScope, questionbank.CreateBankInput{SchoolID: school.ID, Name: "Task file ACL fixture"})
	if err != nil {
		t.Fatal(err)
	}
	item, err := bankStore.CreateItem(commandreceipt.WithID(ctx, "task-file-item"), ownerScope, bank.ID, questionbank.CreateItemInput{
		ItemCode: "TASK-1", Content: questionbank.Content{
			QuestionType: "short_answer", AssessmentArchetype: "short_constructed", Stem: "Synthetic file ACL question", DefaultScore: 1,
			Options: []string{}, KnowledgePoints: []string{"test"},
			Metadata: questionbank.Metadata{SubjectCode: "mathematics", EducationStage: "junior", GradeScope: "grade_8", Copyright: "owned"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	// 同一个采集文件被题库版本固定引用后，任务中的 FileIDs 仍不能代替题库读取授权。
	_, err = bankStore.UpdateScoring(commandreceipt.WithID(ctx, "task-file-pin"), ownerScope, item.Version.ID, questionbank.UpdateScoringInput{
		ExpectedRevision: item.Version.Revision,
		Scoring:          questionbank.Scoring{UsePolicy: "practice_only", Assets: []questionbank.Asset{{FileAssetID: asset.ID, SHA256: asset.HashSHA256, Name: asset.OriginalName, ContentType: asset.ContentType}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, reader := range readers {
		t.Run(reader.name+"/bank_reference_requires_actor_acl", func(t *testing.T) {
			if _, err := reader.read(taskScope); !errors.Is(err, files.ErrForbidden) {
				t.Fatalf("assigned file cannot bypass bank ACL without actor, got %v", err)
			}
			got, err := reader.read(ownerScope)
			if err != nil || got.ID != asset.ID {
				t.Fatalf("bank owner read: asset=%+v error=%v", got, err)
			}
		})
	}
}
