package questionbank

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/commandreceipt"
	"edugrade-enterprise/services/api-gateway/internal/files"
	"edugrade-enterprise/services/api-gateway/internal/paper"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

type referenceImportFixture struct {
	db                                                                          *sql.DB
	store                                                                       *PostgresStore
	images                                                                      *paper.ReferenceMaterialsService
	objects                                                                     *files.MemoryObjectStorage
	scope                                                                       auth.AccessScope
	bank, exam, question, readiness, assessment, manifest, source, crop, viewer string
	image                                                                       []byte
	questionFacts                                                               paper.ReadinessImportQuestionSnapshot
}

// 复制临时集群的结构及本链路生产触发器，不复制任何业务记录。
// 214 始终在随机 schema 中真实应用，避免用 SQL driver 替身证明授权事务。
func newReferenceImportFixture(t *testing.T) referenceImportFixture {
	t.Helper()
	dsn := os.Getenv("YUEJUAN_BINDING_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("isolated PostgreSQL test DSN required")
	}
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	admin := stdlib.OpenDB(*cfg)
	schema := "bank_reference_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err = admin.ExecContext(t.Context(), "CREATE SCHEMA "+schema); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	cfg.RuntimeParams["search_path"] = schema + ",public"
	db := stdlib.OpenDB(*cfg)
	t.Cleanup(func() {
		db.Close()
		if _, err := admin.Exec("DROP SCHEMA " + schema + " CASCADE"); err != nil {
			t.Error(err)
		}
		admin.Close()
	})
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(t.Context(), query, args...); err != nil {
			t.Fatalf("fixture SQL: %v", err)
		}
	}
	rows, err := admin.QueryContext(t.Context(), `SELECT tablename FROM pg_tables WHERE schemaname='public' AND tablename NOT IN('question_bank_reference_grant','question_bank_handoff','question_bank_handoff_event') ORDER BY tablename`)
	if err != nil {
		t.Fatal(err)
	}
	var tables []string
	for rows.Next() {
		var name string
		if err = rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		tables = append(tables, name)
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	for _, table := range tables {
		exec("CREATE TABLE " + pgx.Identifier{table}.Sanitize() + " (LIKE public." + pgx.Identifier{table}.Sanitize() + " INCLUDING ALL)")
	}
	// 基线集群将来可能已经应用214；克隆表没有外键，先恢复这一个迁移前列形状。
	exec(`ALTER TABLE question_reference_manifest DROP COLUMN IF EXISTS origin_manifest_id`)
	migration, err := os.ReadFile(filepath.Join("..", "..", "migrations", "000214_question_bank_reference_grants.sql"))
	if err != nil {
		t.Fatal(err)
	}
	exec(string(migration))
	// 版本读取包含独立交接投影；临时结构也应用正式迁移，不能用空列代替生产表与约束。
	handoffMigration, err := os.ReadFile(filepath.Join("..", "..", "migrations", "000217_question_bank_handoff.sql"))
	if err != nil {
		t.Fatal(err)
	}
	exec(string(handoffMigration))
	// 文件引用守卫固定搜索生产 schema；隔离夹具复制同一函数体并只调整搜索路径，不能查询公共业务表。
	var fileReferenceGuard string
	if err = admin.QueryRowContext(t.Context(), `SELECT pg_get_functiondef('public.edugrade_guard_file_reference()'::regprocedure)`).Scan(&fileReferenceGuard); err != nil {
		t.Fatal(err)
	}
	fileReferenceGuard = strings.Replace(fileReferenceGuard, "FUNCTION public.edugrade_guard_file_reference(", "FUNCTION "+schema+".edugrade_guard_file_reference(", 1)
	exec(fileReferenceGuard)
	exec("ALTER FUNCTION " + schema + ".edugrade_guard_file_reference() SET search_path TO " + schema + ",public")
	triggerRows, err := admin.QueryContext(t.Context(), `SELECT pg_get_triggerdef(t.oid) FROM pg_trigger t JOIN pg_class c ON c.oid=t.tgrelid WHERE NOT t.tgisinternal AND c.relnamespace='public'::regnamespace AND (
 c.relname IN('question_bank_item_version','question_bank_import','question_reference_manifest','question_reference_manifest_item')
 OR c.relname='file_asset' AND t.tgname='trg_reference_material_object_pin') AND t.tgname NOT IN('trg_bank_version_reference_grant','trg_reference_manifest_origin') ORDER BY c.relname,t.tgname`)
	if err != nil {
		t.Fatal(err)
	}
	var triggers []string
	for triggerRows.Next() {
		var def string
		if err = triggerRows.Scan(&def); err != nil {
			t.Fatal(err)
		}
		def = strings.ReplaceAll(def, " ON public.", " ON "+schema+".")
		def = strings.ReplaceAll(def, "EXECUTE FUNCTION public.edugrade_guard_file_reference(", "EXECUTE FUNCTION "+schema+".edugrade_guard_file_reference(")
		def = strings.ReplaceAll(def, "EXECUTE FUNCTION edugrade_guard_file_reference(", "EXECUTE FUNCTION "+schema+".edugrade_guard_file_reference(")
		triggers = append(triggers, def)
	}
	if err = triggerRows.Err(); err != nil {
		t.Fatal(err)
	}
	triggerRows.Close()
	for _, def := range triggers {
		exec(def)
	}
	tenant, actor, school := uuid.NewString(), uuid.NewString(), uuid.NewString()
	f := referenceImportFixture{db: db, store: NewPostgresStore(db), objects: files.NewMemoryObjectStorage(), scope: auth.AccessScope{TenantID: tenant, ActorID: actor, SchoolIDs: []string{school}}, bank: uuid.NewString(), exam: uuid.NewString(), question: uuid.NewString(), readiness: uuid.NewString(), assessment: uuid.NewString(), manifest: uuid.NewString(), source: uuid.NewString(), crop: uuid.NewString(), viewer: uuid.NewString()}
	f.scope.ExamIDs = []string{f.exam}
	f.images = paper.NewReferenceMaterialsService(db, files.NewPostgresStore(db), f.objects, "synthetic")
	exec(`INSERT INTO tenant(id,tenant_id,name,code,status) VALUES($1,$1,'synthetic','synthetic-reference','active')`, tenant)
	for _, user := range []string{actor, f.viewer} {
		exec(`INSERT INTO app_user(id,tenant_id,username,display_name,password_hash,status) VALUES($1,$2,$1::uuid::text,'synthetic','not-a-real-password','active')`, user, tenant)
	}
	// 后续复制考试需管理所属学校；可信归属由账号保存，不能靠 SchoolIDs 导航投影取得写权限。
	exec(`UPDATE app_user SET school_id=$3 WHERE tenant_id=$1 AND id=$2`, tenant, actor, school)
	exec(`INSERT INTO question_bank(id,tenant_id,school_id,name,created_by) VALUES($1,$2,$3,'synthetic frozen references',$4)`, f.bank, tenant, school, actor)
	exec(`INSERT INTO question_bank_metadata_schema(tenant_id,bank_id,version,created_by) VALUES($1,$2,1,$3)`, tenant, f.bank, actor)
	for _, action := range []string{"read", "create", "edit", "review", "publish"} {
		exec(`INSERT INTO question_bank_acl(tenant_id,bank_id,user_id,action) VALUES($1,$2,$3,$4)`, tenant, f.bank, actor, action)
	}
	exec(`INSERT INTO question_bank_acl(tenant_id,bank_id,user_id,action) VALUES($1,$2,$3,'read')`, tenant, f.bank, f.viewer)
	exec(`INSERT INTO exam(id,tenant_id,school_id,name,subject,exam_type,total_score,status,grading_mode,publish_policy,created_by) VALUES($1,$2,$3,'synthetic source','math','quiz',2,'draft','ai_assisted','manual_after_confirmation',$4)`, f.exam, tenant, school, actor)
	exec(`INSERT INTO question(id,tenant_id,exam_id,question_no,question_type,score,sort_order,status,assessment_archetype,options) VALUES($1,$2,$3,'1','single_choice',2,1,'active','selected_response','["甲","乙"]')`, f.question, tenant, f.exam)
	img := image.NewNRGBA(image.Rect(0, 0, 3, 3))
	img.Set(1, 1, color.NRGBA{R: 220, A: 255})
	var data bytes.Buffer
	if err = png.Encode(&data, img); err != nil {
		t.Fatal(err)
	}
	f.image = data.Bytes()
	digest := fmt.Sprintf("%x", sha256.Sum256(f.image))
	for _, asset := range []struct{ id, owner string }{{f.source, "generic"}, {f.crop, "question_reference_crop"}} {
		exec(`INSERT INTO file_asset(id,tenant_id,exam_id,school_id,owner_type,owner_id,original_name,content_type,size_bytes,hash_sha256,storage_bucket,storage_key,visibility,uploaded_by) VALUES($1,$2,$3,$4,$5,$1,'synthetic.png','image/png',$6,$7,'synthetic',$1::uuid::text,'private',$8)`, asset.id, tenant, f.exam, school, asset.owner, len(f.image), digest, actor)
		if err = f.objects.Put(t.Context(), "synthetic", asset.id, bytes.NewReader(f.image), int64(len(f.image)), "image/png"); err != nil {
			t.Fatal(err)
		}
	}
	item := paper.ReferenceMaterialItem{ID: uuid.NewString(), CropRef: f.crop, Role: "question_diagram", Ordinal: 1, Required: true, SHA256: digest, Width: 3, Height: 3, SizeBytes: int64(len(f.image)), Box: paper.ReferencePixelBox{Width: 3, Height: 3}, Source: paper.ReferenceMaterialSource{PageRef: "synthetic-frozen-page", Kind: "import_image", PageNo: 1, SHA256: digest, Width: 3, Height: 3, SourceHash: digest, Identity: "synthetic-generation"}}
	raw, _ := json.Marshal(item)
	// 生产的清单创建/item密封触发器要求整份清单在同一事务创建。
	tx, err := db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.ExecContext(t.Context(), `INSERT INTO question_reference_manifest(id,tenant_id,exam_id,question_id,revision,mode,content_hash,command_id,request_hash,created_by,exam_use_grant_json) VALUES($1,$2,$3,$4,1,'reference_images',repeat('b',64),'synthetic-source-manifest',repeat('c',64),$5,'{"schema_version":1}')`, f.manifest, tenant, f.exam, f.question, actor); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if _, err = tx.ExecContext(t.Context(), `INSERT INTO question_reference_manifest_item(id,tenant_id,manifest_id,ordinal,role,source_file_asset_id,page_file_asset_id,derived_file_asset_id,payload) VALUES($1,$2,$3,1,'question_diagram',$4,$4,$5,$6)`, item.ID, tenant, f.manifest, f.source, f.crop, raw); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	f.questionFacts = paper.ReadinessImportQuestionSnapshot{ID: f.question, AssessmentSnapshotID: f.assessment, AssessmentSnapshotHash: strings.Repeat("a", 64), QuestionNo: "1", QuestionType: "single_choice", AssessmentArchetype: "selected_response", Score: 2, Stem: "synthetic frozen stem", Options: []string{"甲", "乙"}, KnowledgePoints: []string{}, Answer: &paper.ReadinessAnswerSnapshot{StandardAnswer: "B", EquivalentAnswers: []any{}, Tolerance: map[string]any{}}, ReferenceMaterials: &paper.ReferenceMaterialBinding{ID: f.manifest, Mode: "reference_images", Revision: 1, ContentHash: strings.Repeat("b", 64), ImageCount: 1}}
	exec(`INSERT INTO exam_question_snapshot(id,tenant_id,exam_id,question_id,subject_profile_id,subject_profile_version,archetype_code,allowed_evidence_types,risk_tier,profile_snapshot_json,archetype_snapshot_json,rubric_snapshot_json,scoring_policy_snapshot_json,content_hash) VALUES($1,$2,$3,$4,$5,1,'selected_response','[]','R1','{"subject_code":"mathematics","education_stage":"junior"}','{}','{}','{}',repeat('a',64))`, f.assessment, tenant, f.exam, f.question, actor)
	frozen := paper.ReadinessImportSnapshot{Version: 2, ConfigurationHash: strings.Repeat("c", 64), Questions: []paper.ReadinessImportQuestionSnapshot{f.questionFacts}}
	raw, _ = json.Marshal(frozen)
	exec(`INSERT INTO exam_readiness_snapshot(id,tenant_id,exam_id,configuration_hash,status,checks,confirmed_by,import_snapshot_schema_version,import_snapshot_hash,import_snapshot_json) VALUES($1,$2,$3,$4,'passed','[]',$5,2,$6,$7)`, f.readiness, tenant, f.exam, frozen.ConfigurationHash, actor, hashJSON(frozen), raw)
	return f
}

func (f referenceImportFixture) importQuestion(t *testing.T) ImportResult {
	t.Helper()
	command := "synthetic-" + uuid.NewString()
	in := ImportQuestionInput{TargetBankID: f.bank, SourceSnapshotID: f.readiness, AssessmentSnapshotID: f.assessment, ScoringSource: "original_exam", DedupDecision: "new_item", ExpectedTargetRevision: 1, ExpectedTargetSchemaVersion: 1, CommandID: command, Mapping: ImportMapping{ItemCode: "SYNTHETIC-REFERENCE", GradeScope: "grade_8", Copyright: "owned"}}
	result, err := f.store.ImportQuestion(commandreceipt.WithID(t.Context(), command), f.scope, f.question, in)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestPostgresReferenceImagesImportReadTransferAndWithdraw(t *testing.T) {
	f := newReferenceImportFixture(t)
	result := f.importQuestion(t)
	if result.Version.Scoring.ReferenceMaterials == nil || result.Version.Scoring.ReferenceMaterials.ID != f.manifest || len(result.Version.Scoring.ReferenceMaterials.Items) != 1 || len(result.Version.Scoring.Assets) != 0 {
		t.Fatalf("frozen reference lost or broadened: %#v", result.Version.Scoring)
	}
	viewer := auth.AccessScope{TenantID: f.scope.TenantID, ActorID: f.viewer, SchoolIDs: f.scope.SchoolIDs}
	// 题库查看者没有来源考试范围，只能经本版本精确授权路由读裁图，不能读来源文件。
	handler := NewHandler(f.store).WithReferenceMaterials(f.images)
	read := func(scope auth.AccessScope, version, item string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.SetPathValue("versionId", version)
		r.SetPathValue("itemId", item)
		r = r.WithContext(auth.WithAccessScope(r.Context(), scope))
		w := httptest.NewRecorder()
		handler.GetReferenceMaterialImage(w, r)
		return w
	}
	item := result.Version.Scoring.ReferenceMaterials.Items[0]
	if response := read(viewer, result.Version.ID, item.ID); response.Code != 200 || !bytes.Equal(response.Body.Bytes(), f.image) {
		t.Fatalf("authorized image failed: %d %s", response.Code, response.Body.String())
	}
	if response := read(viewer, result.Version.ID, uuid.NewString()); response.Code != 404 {
		t.Fatalf("arbitrary item read: %d", response.Code)
	}
	if _, err := files.NewPostgresStore(f.db).GetScoped(t.Context(), viewer, f.crop); !errors.Is(err, files.ErrForbidden) {
		t.Fatalf("generic file bypass: %v", err)
	}
	other := viewer
	other.ActorID = uuid.NewString()
	if response := read(other, result.Version.ID, item.ID); response.Code != 404 && response.Code != 403 {
		t.Fatalf("bank ACL bypass: %d", response.Code)
	}
	clone, err := f.store.CreateVersion(commandreceipt.WithID(t.Context(), "clone-"+uuid.NewString()), f.scope, result.Item.ID, CreateVersionInput{SourceVersionID: result.Version.ID})
	if err != nil {
		t.Fatal(err)
	}
	if response := read(viewer, clone.ID, item.ID); response.Code != 200 {
		t.Fatalf("new version did not retain exact grant: %d %s", response.Code, response.Body.String())
	}
	omitted := copyScoring(clone.Scoring)
	omitted.ReferenceMaterials = nil
	clone, err = f.store.UpdateScoring(commandreceipt.WithID(t.Context(), "scoring-"+uuid.NewString()), f.scope, clone.ID, UpdateScoringInput{ExpectedRevision: clone.Revision, Scoring: omitted})
	if err != nil || clone.Scoring.ReferenceMaterials == nil || clone.Scoring.ReferenceMaterials.ID != f.manifest {
		t.Fatalf("ordinary scoring edit removed reference grant: %#v %v", clone, err)
	}
	forged := copyScoring(clone.Scoring)
	forged.ReferenceMaterials.ID = uuid.NewString()
	if _, err = f.store.UpdateScoring(commandreceipt.WithID(t.Context(), "forged-"+uuid.NewString()), f.scope, clone.ID, UpdateScoringInput{ExpectedRevision: clone.Revision, Scoring: forged}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("scoring request rebound reference identity: %v", err)
	}
	// 即使绕过HTTP，缺少版本授权记录的插入也必须在提交时被数据库拒绝。
	tx, err := f.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = tx.ExecContext(t.Context(), `INSERT INTO question_bank_item_version(tenant_id,item_id,version_no,schema_version,source_version_id,author_id,content,content_hash,scoring) SELECT tenant_id,item_id,3,schema_version,id,author_id,content,content_hash,scoring FROM question_bank_item_version WHERE id=$1`, clone.ID)
	if err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err = tx.Commit(); err == nil {
		t.Fatal("ungranted copied reference committed")
	}
	if _, err = f.db.ExecContext(t.Context(), `UPDATE question_bank_reference_grant SET purpose=purpose WHERE version_id=$1`, clone.ID); err == nil {
		t.Fatal("version grant was mutable")
	}
	if err = f.objects.Put(t.Context(), "synthetic", f.crop, strings.NewReader("synthetic replacement"), 21, "image/png"); err != nil {
		t.Fatal(err)
	}
	if response := read(viewer, clone.ID, item.ID); response.Code != 409 {
		t.Fatalf("changed object bytes bypassed frozen hash: %d", response.Code)
	}
	if err = f.objects.Put(t.Context(), "synthetic", f.crop, bytes.NewReader(f.image), int64(len(f.image)), "image/png"); err != nil {
		t.Fatal(err)
	}
	if _, err = f.db.ExecContext(t.Context(), `UPDATE file_asset SET lifecycle_status='quarantined' WHERE id=$1`, f.source); err != nil {
		t.Fatal(err)
	}
	if response := read(viewer, clone.ID, item.ID); response.Code != 409 {
		t.Fatalf("quarantined source remained readable: %d", response.Code)
	}
	if _, err = f.db.ExecContext(t.Context(), `UPDATE file_asset SET lifecycle_status='active' WHERE id=$1`, f.source); err != nil {
		t.Fatal(err)
	}
	if _, err = f.db.ExecContext(t.Context(), `INSERT INTO question_bank_acl(tenant_id,bank_id,user_id,action) VALUES($1,$2,$3,'review')`, f.scope.TenantID, f.bank, f.viewer); err != nil {
		t.Fatal(err)
	}
	version := result.Version
	for _, decision := range []string{"submit-review", "approve", "publish"} {
		actorScope := f.scope
		if decision == "approve" {
			actorScope = viewer
		}
		version, err = f.store.Transition(commandreceipt.WithID(t.Context(), decision+"-"+uuid.NewString()), actorScope, version.ID, decision, ReviewInput{ExpectedRevision: version.Revision, BundleHash: version.BundleHash, Comment: "synthetic verified reference"})
		if err != nil {
			t.Fatalf("%s: %v", decision, err)
		}
	}
	target := uuid.NewString()
	if _, err = f.db.ExecContext(t.Context(), `INSERT INTO exam(id,tenant_id,school_id,name,subject,exam_type,total_score,status,grading_mode,publish_policy,created_by) VALUES($1,$2,$3,'synthetic target','math','quiz',2,'draft','ai_assisted','manual_after_confirmation',$4)`, target, f.scope.TenantID, f.scope.SchoolIDs[0], f.scope.ActorID); err != nil {
		t.Fatal(err)
	}
	materializeInput := MaterializeInput{ExpectedRevision: 1, Selections: []MaterializeSelection{{VersionID: version.ID, QuestionNo: "1", SortOrder: 1}}}
	navigationScope := f.scope
	navigationScope.ExamIDs = append(append([]string{}, f.scope.ExamIDs...), target)
	if _, err = f.store.Materialize(commandreceipt.WithID(t.Context(), "navigation-only-"+uuid.NewString()), navigationScope, target, materializeInput); !errors.Is(err, ErrNotFound) {
		t.Fatalf("exam visibility granted materialize management: %v", err)
	}
	var targetRevision int64
	var targetQuestionCount int
	if err = f.db.QueryRowContext(t.Context(), `SELECT revision,(SELECT count(*) FROM question WHERE tenant_id=$1 AND exam_id=$2) FROM exam WHERE tenant_id=$1 AND id=$2`, f.scope.TenantID, target).Scan(&targetRevision, &targetQuestionCount); err != nil || targetRevision != 1 || targetQuestionCount != 0 {
		t.Fatalf("rejected materialize changed target: revision=%d questions=%d err=%v", targetRevision, targetQuestionCount, err)
	}
	// 与认证后的仓储解析相同，从账号实际学校派生管理范围，再验证正常复制和冻结引用链。
	targetScope, err := auth.NewPostgresStore(f.db).ResolveAccessScope(t.Context(), auth.User{TenantID: f.scope.TenantID, ID: f.scope.ActorID, Roles: []string{"school_admin"}, DataScope: map[string]any{"school_admin": map[string]any{"scope": "school"}}})
	if err != nil {
		t.Fatal(err)
	}
	materialized, err := f.store.Materialize(commandreceipt.WithID(t.Context(), "materialize-"+uuid.NewString()), targetScope, target, materializeInput)
	if err != nil {
		t.Fatal(err)
	}
	if len(materialized.Questions) != 1 || materialized.Questions[0].ReferenceMaterials == nil || materialized.Questions[0].ReferenceMaterials.ID == f.manifest {
		t.Fatalf("target reference was not independently bound: %#v", materialized)
	}
	targetQuestion := materialized.Questions[0]
	if data, err := f.images.CropImage(t.Context(), targetScope, target, targetQuestion.ID, f.crop); err != nil || !bytes.Equal(data, f.image) {
		t.Fatalf("target exact crop read: %v", err)
	}
	_, targetImages, err := f.images.Resolve(t.Context(), f.scope.TenantID, target, targetQuestion.ID, targetQuestion.ReferenceMaterials)
	if err != nil || len(targetImages) != 1 || !bytes.Equal(targetImages[0].Data, f.image) {
		t.Fatalf("target frozen consumption: %#v %v", targetImages, err)
	}
	// 目标草稿保留继承图片时创建新清单，不得因裁图仍属原考试而丢图或断开撤回链。
	var save paper.SaveReferenceMaterialsInput
	if err = json.Unmarshal([]byte(fmt.Sprintf(`{"command_id":"save-%s","expected_revision":1,"mode":"reference_images","items":[{"crop_ref":"%s","role":"question_diagram","ordinal":1}]}`, uuid.NewString(), f.crop)), &save); err != nil {
		t.Fatal(err)
	}
	saved, err := f.images.Save(t.Context(), targetScope, target, targetQuestion.ID, save)
	if err != nil || saved.Revision != 2 || len(saved.Items) != 1 {
		t.Fatalf("inherited image could not be saved: %#v %v", saved, err)
	}
	targetQuestion.ReferenceMaterials = &paper.ReferenceMaterialBinding{ID: saved.ID, Mode: saved.Mode, Revision: saved.Revision, ContentHash: saved.ContentHash, ImageCount: len(saved.Items)}
	// 归档不改冻结身份和已授予的版本用途，仍然可以读取；撤回则实时阻断。
	if _, err = f.db.ExecContext(t.Context(), `UPDATE exam SET status='archived' WHERE id=$1`, f.exam); err != nil {
		t.Fatal(err)
	}
	if response := read(viewer, result.Version.ID, item.ID); response.Code != 200 {
		t.Fatalf("archive removed historical read grant: %d", response.Code)
	}
	// 原归档规则禁止写入归档考试，本测试恢复为冻结状态后通过正式撤回方法验证父链。
	if _, err = f.db.ExecContext(t.Context(), `UPDATE exam SET status='ready' WHERE id=$1`, f.exam); err != nil {
		t.Fatal(err)
	}
	_, err = f.images.Withdraw(t.Context(), f.scope, f.exam, f.question, f.manifest, paper.WithdrawReferenceMaterialsInput{CommandID: "withdraw-" + uuid.NewString(), ExpectedAuthorizationRevision: 1, Reason: "synthetic withdrawal"})
	if err != nil {
		t.Fatal(err)
	}
	if response := read(viewer, clone.ID, item.ID); response.Code != 409 {
		t.Fatalf("withdrawn source remained usable: %d %s", response.Code, response.Body.String())
	}
	if _, _, err = f.images.Resolve(t.Context(), f.scope.TenantID, target, targetQuestion.ID, targetQuestion.ReferenceMaterials); err == nil {
		t.Fatal("source withdrawal did not invalidate target exam parent chain")
	}
}
