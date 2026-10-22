package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/files"
	"edugrade-enterprise/services/api-gateway/internal/paper"
	"github.com/google/uuid"
)

type referenceMaterialTestObjects struct {
	files.ObjectStorage
	failPut atomic.Bool
}

// 使用现有就绪确认入口冻结真实派生图，验证材料与评估事实的伴随快照绑定；不运行模型。
func TestA01ReferenceFreezePostgres(t *testing.T) {
	dsn := os.Getenv("EDUGRADE_E2E_DATABASE_URL")
	if dsn == "" {
		t.Skip("EDUGRADE_E2E_DATABASE_URL is required")
	}
	db := e2eOpenPostgresTestDB(t, dsn)
	e2eApplyPostgresMigrations(t, db)
	e2eActivatePostgresDemoUsers(t, db, []string{"tenant_admin"})
	router := e2ePostgresRouter(db)
	token := e2eLoginWithTenant(t, router, "demo", "tenant_admin", "ChangeMe123!")
	// 复用旧组织/题目前提，但按当前锁定接口提供准确 revision；旧夹具本身仍保留。
	fixtureRouter := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/api/v1/answer-sheet-templates/") && strings.HasSuffix(r.URL.Path, "/lock") {
			id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/v1/answer-sheet-templates/"), "/lock")
			var revision int
			if e := db.QueryRow(`SELECT revision FROM answer_sheet_template WHERE id=$1`, id).Scan(&revision); e != nil {
				t.Fatal(e)
			}
			body := fmt.Sprintf(`{"expected_revision":%d}`, revision)
			r.Body = io.NopCloser(strings.NewReader(body))
			r.ContentLength = int64(len(body))
		}
		router.ServeHTTP(w, r)
	})
	f := e2eCreateStory056MutableTemplateFixture(t, db, fixtureRouter, token, strings.ReplaceAll(uuid.NewString(), "-", ""), nil)
	ctx := context.Background()
	scope := auth.AccessScope{TenantID: f.TenantID, ActorID: f.AdminID, TenantWide: true}
	assets := files.NewPostgresStore(db)
	objects := files.NewMemoryObjectStorage()
	svc := paper.NewReferenceMaterialsService(db, assets, objects, "synthetic")
	var pngBody bytes.Buffer
	if err := png.Encode(&pngBody, image.NewNRGBA(image.Rect(0, 0, 16, 12))); err != nil {
		t.Fatal(err)
	}
	data := pngBody.Bytes()
	key := uuid.NewString()
	hash := fmt.Sprintf("%x", sha256.Sum256(data))
	a, err := assets.Create(ctx, files.CreateAssetInput{TenantID: f.TenantID, ExamID: f.ExamID, OwnerType: "exam", OwnerID: f.ExamID, OriginalName: "reference.png", ContentType: "image/png", SizeBytes: int64(len(data)), HashSHA256: hash, StorageBucket: "synthetic", StorageKey: key, UploadedBy: f.AdminID, Visibility: "private"})
	if err != nil {
		t.Fatal(err)
	}
	if err = objects.Put(ctx, "synthetic", key, bytes.NewReader(data), int64(len(data)), "image/png"); err != nil {
		t.Fatal(err)
	}
	job, err := paper.NewPostgresStore(db).CreatePaperImport(ctx, f.TenantID, f.ExamID, f.AdminID, paper.CreatePaperImportInput{Subject: "physics", Sources: []paper.CreatePaperImportSourceInput{{FileAssetID: a.ID, DocumentIndex: 0, RoleHint: "question"}}})
	if err != nil {
		t.Fatal(err)
	}
	// 结构已经由独立夹具建立；这里只设定“当前代次结构已人工核对”的业务前提。
	mustScoringTrialExec(t, db, `UPDATE paper_import_job SET status='review_required',structure_confirmed_generation=current_generation WHERE id=$1`, job.ID)
	sources, err := svc.Sources(ctx, scope, f.ExamID)
	if err != nil || len(sources) != 1 {
		t.Fatalf("source=%+v %v", sources, err)
	}
	source := sources[0]
	q := f.QuestionIDs["single_choice"]
	crop, err := svc.CreateCrop(ctx, scope, f.ExamID, q, paper.CreateReferenceCropInput{CommandID: uuid.NewString(), PageRef: source.PageRef, PageSHA256: source.SHA256, Width: 16, Height: 12, Box: paper.ReferencePixelBox{Width: 16, Height: 12}})
	if err != nil {
		t.Fatal(err)
	}
	var input paper.SaveReferenceMaterialsInput
	if err = json.Unmarshal([]byte(`{"command_id":"`+uuid.NewString()+`","mode":"reference_images","items":[{"crop_ref":"`+crop.CropRef+`","role":"question_diagram","ordinal":1}]}`), &input); err != nil {
		t.Fatal(err)
	}
	m, err := svc.Save(ctx, scope, f.ExamID, q, input)
	if err != nil {
		t.Fatal(err)
	}
	store := paper.NewPostgresStore(db)
	ready, err := store.Readiness(ctx, f.TenantID, f.ExamID)
	if err != nil || !ready.Ready {
		t.Fatalf("readiness=%+v %v", ready, err)
	}
	confirmed, err := store.ConfirmReadiness(ctx, f.TenantID, f.ExamID, f.AdminID, paper.ConfirmReadinessInput{ExpectedConfigurationHash: ready.ConfigurationHash})
	if err != nil || !confirmed.Confirmed {
		t.Fatalf("confirm=%+v %v", confirmed, err)
	}
	var snapshot string
	if err = db.QueryRow(`SELECT id::text FROM exam_question_snapshot WHERE tenant_id=$1 AND exam_id=$2 AND question_id=$3 ORDER BY snapshot_version DESC LIMIT 1`, f.TenantID, f.ExamID, q).Scan(&snapshot); err != nil {
		t.Fatal(err)
	}
	frozen, err := svc.ForSnapshot(ctx, f.TenantID, f.ExamID, q, snapshot)
	if err != nil || frozen.ID != m.ID || !frozen.Frozen || len(frozen.Items) != 1 {
		t.Fatalf("exact frozen=%+v %v", frozen, err)
	}
	if _, err = svc.FrozenImage(ctx, f.TenantID, f.ExamID, q, snapshot, frozen.Items[0].ID); err != nil {
		t.Fatal(err)
	}
	input.CommandID = uuid.NewString()
	input.ExpectedRevision = 1
	if _, err = svc.Save(ctx, scope, f.ExamID, q, input); !errors.Is(err, paper.ErrExamFrozen) {
		t.Fatalf("frozen replacement=%v", err)
	}
	if _, err = svc.Withdraw(ctx, scope, f.ExamID, q, m.ID, paper.WithdrawReferenceMaterialsInput{CommandID: uuid.NewString(), ExpectedAuthorizationRevision: 1, Reason: "合成撤回测试"}); err != nil {
		t.Fatal(err)
	}
	if _, err = store.StartCollection(ctx, f.TenantID, f.ExamID, f.AdminID); !errors.Is(err, paper.ErrNotReady) {
		t.Fatalf("withdrawn materials allowed collection: %v", err)
	}
	// HTTP读取仍是已冻结版本，不能回落成历史未记录或找草稿最新版。
	response := e2eGetJSON(t, router, "/api/v1/exams/"+f.ExamID+"/questions/"+q+"/reference-materials", token, http.StatusOK)
	if response["materials"].(map[string]any)["manifest_id"] != m.ID {
		t.Fatalf("frozen route=%+v", response)
	}
}

func (o *referenceMaterialTestObjects) Put(ctx context.Context, bucket, key string, data io.Reader, size int64, kind string) error {
	if o.failPut.Swap(false) {
		return errors.New("synthetic interrupted upload")
	}
	return o.ObjectStorage.Put(ctx, bucket, key, data, size, kind)
}

// 单独临时数据库应用所有迁移，合成 PNG 经过生产 files/paper 存储；不调用供应商。
func TestA01ReferenceMaterialsPostgres(t *testing.T) {
	dsn := os.Getenv("EDUGRADE_E2E_DATABASE_URL")
	if dsn == "" {
		t.Skip("EDUGRADE_E2E_DATABASE_URL is required")
	}
	db := e2eOpenPostgresTestDB(t, dsn)
	e2eApplyPostgresMigrations(t, db)
	f := seedScoringTrialPostgres(t, db)
	ctx := context.Background()
	scope := auth.AccessScope{TenantID: f.tenantID, ActorID: f.actorID, TenantWide: true}
	assets := files.NewPostgresStore(db)
	objects := &referenceMaterialTestObjects{ObjectStorage: files.NewMemoryObjectStorage()}
	svc := paper.NewReferenceMaterialsService(db, assets, objects, "synthetic")
	img := image.NewNRGBA(image.Rect(0, 0, 24, 16))
	img.Set(5, 6, color.NRGBA{R: 255, A: 255})
	var imageBody bytes.Buffer
	if e := png.Encode(&imageBody, img); e != nil {
		t.Fatal(e)
	}
	raw := imageBody.Bytes()
	hash := fmt.Sprintf("%x", sha256.Sum256(raw))
	key := uuid.NewString()
	asset, e := assets.Create(ctx, files.CreateAssetInput{TenantID: f.tenantID, ExamID: f.examID, OwnerType: "exam", OwnerID: f.examID, OriginalName: "synthetic.png", ContentType: "image/png", SizeBytes: int64(len(raw)), HashSHA256: hash, StorageBucket: "synthetic", StorageKey: key, UploadedBy: f.actorID, Visibility: "private"})
	if e != nil {
		t.Fatal(e)
	}
	if e = objects.Put(ctx, "synthetic", key, bytes.NewReader(raw), int64(len(raw)), "image/png"); e != nil {
		t.Fatal(e)
	}
	_, e = paper.NewPostgresStore(db).CreatePaperImport(ctx, f.tenantID, f.examID, f.actorID, paper.CreatePaperImportInput{Subject: "physics", Sources: []paper.CreatePaperImportSourceInput{{FileAssetID: asset.ID, DocumentIndex: 0, RoleHint: "question"}}})
	if e != nil {
		t.Fatal(e)
	}
	sources, e := svc.Sources(ctx, scope, f.examID)
	if e != nil || len(sources) != 1 {
		t.Fatalf("sources=%+v err=%v", sources, e)
	}
	source := sources[0]
	cropInput := paper.CreateReferenceCropInput{CommandID: uuid.NewString(), PageRef: source.PageRef, PageSHA256: source.SHA256, Width: source.Width, Height: source.Height, Box: paper.ReferencePixelBox{X: 2, Y: 2, Width: 12, Height: 8}}
	crop, e := svc.CreateCrop(ctx, scope, f.examID, f.questionID, cropInput)
	if e != nil {
		t.Fatal(e)
	}
	data, e := svc.CropImage(ctx, scope, f.examID, f.questionID, crop.CropRef)
	if e != nil {
		t.Fatal(e)
	}
	actual, e := png.Decode(bytes.NewReader(data))
	if e != nil || actual.Bounds().Dx() != 12 {
		t.Fatalf("actual crop=%v err=%v", actual, e)
	}
	replay, e := svc.CreateCrop(ctx, scope, f.examID, f.questionID, cropInput)
	if e != nil || replay.CropRef != crop.CropRef {
		t.Fatalf("crop replay=%+v err=%v", replay, e)
	}
	cropInput.Box.Width++
	if _, e = svc.CreateCrop(ctx, scope, f.examID, f.questionID, cropInput); e == nil {
		t.Fatal("same command changed crop")
	}
	// 原命令已登记但对象未写入；刷新只带命令 ID，由服务器恢复同一裁图。
	interrupted := cropInput
	interrupted.CommandID = uuid.NewString()
	objects.failPut.Store(true)
	if _, e = svc.CreateCrop(ctx, scope, f.examID, f.questionID, interrupted); e == nil {
		t.Fatal("expected interrupted upload")
	}
	before, e := svc.Recover(ctx, scope, f.examID, interrupted.CommandID)
	if e != nil || before.Status != "processing" {
		t.Fatalf("pending recovery=%+v %v", before, e)
	}
	after, e := svc.ResumeCommand(ctx, scope, f.examID, f.questionID, interrupted.CommandID)
	if e != nil || after.Status != "succeeded" {
		t.Fatalf("resumed recovery=%+v %v", after, e)
	}
	if _, e = svc.ResumeCommand(ctx, scope, f.examID, uuid.NewString(), interrupted.CommandID); !errors.Is(e, paper.ErrConflict) {
		t.Fatalf("other question resumed command: %v", e)
	}
	closed := cropInput
	closed.CommandID = uuid.NewString()
	if receipt, e := svc.ResumeCommand(ctx, scope, f.examID, f.questionID, closed.CommandID); e != nil || receipt.Status != "rejected" {
		t.Fatalf("close unaccepted=%+v %v", receipt, e)
	}
	if _, e = svc.CreateCrop(ctx, scope, f.examID, f.questionID, closed); e == nil {
		t.Fatal("late original request submitted after closure")
	}
	var input paper.SaveReferenceMaterialsInput
	if e = json.Unmarshal([]byte(`{"command_id":"`+uuid.NewString()+`","expected_revision":0,"mode":"reference_images","items":[{"crop_ref":"`+crop.CropRef+`","role":"question_diagram","ordinal":1}]}`), &input); e != nil {
		t.Fatal(e)
	}
	m, e := svc.Save(ctx, scope, f.examID, f.questionID, input)
	if e != nil {
		t.Fatal(e)
	}
	again, e := svc.Save(ctx, scope, f.examID, f.questionID, input)
	if e != nil || again.ID != m.ID {
		t.Fatalf("manifest replay=%+v %v", again, e)
	}
	input.CommandID = uuid.NewString()
	if _, e = svc.Save(ctx, scope, f.examID, f.questionID, input); !errors.Is(e, paper.ErrConflict) {
		t.Fatalf("stale revision accepted=%v", e)
	}
	if _, e = assets.GetScoped(ctx, scope, crop.CropRef); !errors.Is(e, files.ErrForbidden) {
		t.Fatalf("generic download bypass=%v", e)
	}
	if _, e = db.Exec(`UPDATE file_asset SET lifecycle_status='pending_delete' WHERE id=$1`, asset.ID); e == nil {
		t.Fatal("pinned source soft-deleted")
	}
	binding := &paper.ReferenceMaterialBinding{ID: m.ID, Mode: m.Mode, Revision: m.Revision, ContentHash: m.ContentHash, ImageCount: 1}
	if _, images, e := svc.Resolve(ctx, f.tenantID, f.examID, f.questionID, binding); e != nil || len(images) != 1 {
		t.Fatalf("resolved=%d err=%v", len(images), e)
	}
	assertReferenceReadinessCheck := func(want bool) {
		t.Helper()
		readiness, e := paper.NewPostgresStore(db).Readiness(ctx, f.tenantID, f.examID)
		if e != nil {
			t.Fatal(e)
		}
		for _, check := range readiness.Checks {
			if check.Code == "reference_materials" {
				if check.Passed != want {
					t.Fatalf("reference readiness=%+v", check)
				}
				return
			}
		}
		t.Fatal("missing reference material readiness check")
	}
	assertReferenceReadinessCheck(true)
	// 同一对象键的另一登记行也不能进入删除状态，清单不能只保护主资产 ID。
	alias, e := assets.Create(ctx, files.CreateAssetInput{TenantID: f.tenantID, ExamID: f.examID, OwnerType: "question", OwnerID: f.questionID, OriginalName: "alias.png", ContentType: "image/png", SizeBytes: int64(len(raw)), HashSHA256: hash, StorageBucket: "synthetic", StorageKey: key, UploadedBy: f.actorID, Visibility: "private"})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec(`UPDATE file_asset SET lifecycle_status='pending_delete' WHERE id=$1`, alias.ID); e == nil {
		t.Fatal("shared object alias deletion accepted")
	}
	for _, tenant := range []string{"", uuid.NewString(), f.tenantID} {
		tx, e := db.Begin()
		if e != nil {
			t.Fatal(e)
		}
		if _, e = tx.Exec(`SET LOCAL ROLE edugrade_tenant_runtime`); e != nil {
			t.Fatal(e)
		}
		if _, e = tx.Exec(`SELECT set_config('edugrade.tenant_id',$1,true)`, tenant); e != nil {
			t.Fatal(e)
		}
		var count int
		e = tx.QueryRow(`SELECT count(*) FROM question_reference_manifest WHERE id=$1`, m.ID).Scan(&count)
		tx.Rollback()
		want := 0
		if tenant == f.tenantID {
			want = 1
		}
		if e != nil || count != want {
			t.Fatalf("RLS tenant=%s count=%d err=%v", tenant, count, e)
		}
	}
	revoked, e := svc.Withdraw(ctx, scope, f.examID, f.questionID, m.ID, paper.WithdrawReferenceMaterialsInput{CommandID: uuid.NewString(), ExpectedAuthorizationRevision: 1, Reason: "合成撤回测试"})
	if e != nil || revoked.UseState != "withdrawn" {
		t.Fatalf("revoke=%+v err=%v", revoked, e)
	}
	if _, _, e = svc.Resolve(ctx, f.tenantID, f.examID, f.questionID, binding); !errors.Is(e, paper.ErrConflict) {
		t.Fatalf("withdrawn image resolved=%v", e)
	}
	assertReferenceReadinessCheck(false)
}
