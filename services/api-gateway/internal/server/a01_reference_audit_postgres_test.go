package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/png"
	"os"
	"strings"
	"sync"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/files"
	"edugrade-enterprise/services/api-gateway/internal/paper"
	"edugrade-enterprise/services/api-gateway/internal/subjective"
	"github.com/google/uuid"
)

func TestA01ReferenceAuditPostgres(t *testing.T) {
	dsn := os.Getenv("EDUGRADE_E2E_DATABASE_URL")
	if dsn == "" {
		t.Skip("EDUGRADE_E2E_DATABASE_URL is required")
	}
	db := e2eOpenPostgresTestDB(t, dsn)
	e2eApplyPostgresMigrations(t, db)
	ctx := context.Background()

	t.Run("missing_question_has_no_phantom_receipt", func(t *testing.T) {
		f := seedScoringTrialPostgres(t, db)
		other := seedScoringTrialPostgres(t, db)
		scope := auth.AccessScope{TenantID: f.tenantID, ActorID: f.actorID, TenantWide: true}
		svc := paper.NewReferenceMaterialsService(db, files.NewPostgresStore(db), files.NewMemoryObjectStorage(), "synthetic")
		for _, question := range []string{uuid.NewString(), other.questionID} {
			input := paper.SaveReferenceMaterialsInput{CommandID: uuid.NewString(), Mode: "text_only"}
			if _, err := svc.Save(ctx, scope, f.examID, question, input); !errors.Is(err, paper.ErrNotFound) {
				t.Fatalf("unmatched question=%s save error=%v", question, err)
			}
			receipt, err := svc.Recover(ctx, scope, f.examID, input.CommandID)
			if err != nil || receipt.Status != "not_accepted" {
				t.Fatalf("phantom receipt=%+v err=%v", receipt, err)
			}
			var count int
			if err = db.QueryRow(`SELECT count(*) FROM question_reference_manifest WHERE tenant_id=$1 AND created_by=$2 AND command_id=$3`, f.tenantID, f.actorID, input.CommandID).Scan(&count); err != nil || count != 0 {
				t.Fatalf("unmatched manifest count=%d err=%v", count, err)
			}
		}
		if _, err := svc.Save(ctx, scope, f.examID, f.questionID, paper.SaveReferenceMaterialsInput{CommandID: uuid.NewString(), Mode: "text_only"}); err != nil {
			t.Fatalf("valid text-only save=%v", err)
		}
	})

	t.Run("scoring_snapshot_scope_and_parameter_types", func(t *testing.T) {
		f := seedScoringTrialPostgres(t, db)
		other := seedScoringTrialPostgres(t, db)
		svc := paper.NewReferenceMaterialsService(db, files.NewPostgresStore(db), files.NewMemoryObjectStorage(), "synthetic")
		scope := auth.AccessScope{TenantID: f.tenantID, ActorID: f.actorID, TenantWide: true}
		exam, snapshot, err := svc.ScoringRunSnapshot(ctx, scope, f.runID, f.questionID)
		if err != nil || exam != f.examID || snapshot == "" {
			t.Fatalf("snapshot scope=%s %s err=%v", exam, snapshot, err)
		}
		if _, _, err = svc.ScoringRunSnapshot(ctx, scope, f.runID, other.questionID); !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("foreign question snapshot=%v", err)
		}
		scope.TenantWide = false
		if _, _, err = svc.ScoringRunSnapshot(ctx, scope, f.runID, f.questionID); !errors.Is(err, files.ErrForbidden) {
			t.Fatalf("unscoped scoring snapshot=%v", err)
		}
	})

	t.Run("coordinates_and_concurrent_command_recovery", func(t *testing.T) {
		f := seedScoringTrialPostgres(t, db)
		scope := auth.AccessScope{TenantID: f.tenantID, ActorID: f.actorID, TenantWide: true}
		assets := files.NewPostgresStore(db)
		objects := &referenceMaterialTestObjects{ObjectStorage: files.NewMemoryObjectStorage()}
		svc := paper.NewReferenceMaterialsService(db, assets, objects, "synthetic")
		var body bytes.Buffer
		if err := png.Encode(&body, image.NewNRGBA(image.Rect(0, 0, 16, 12))); err != nil {
			t.Fatal(err)
		}
		data, key := body.Bytes(), uuid.NewString()
		asset, err := assets.Create(ctx, files.CreateAssetInput{TenantID: f.tenantID, ExamID: f.examID, OwnerType: "exam", OwnerID: f.examID, OriginalName: "coordinates.png", ContentType: "image/png", SizeBytes: int64(len(data)), HashSHA256: fmt.Sprintf("%x", sha256.Sum256(data)), StorageBucket: "synthetic", StorageKey: key, UploadedBy: f.actorID, Visibility: "private"})
		if err != nil {
			t.Fatal(err)
		}
		if err = objects.Put(ctx, "synthetic", key, bytes.NewReader(data), int64(len(data)), "image/png"); err != nil {
			t.Fatal(err)
		}
		job, err := paper.NewPostgresStore(db).CreatePaperImport(ctx, f.tenantID, f.examID, f.actorID, paper.CreatePaperImportInput{Subject: "physics", Sources: []paper.CreatePaperImportSourceInput{{FileAssetID: asset.ID, DocumentIndex: 0, RoleHint: "question"}}})
		if err != nil {
			t.Fatal(err)
		}
		sources, err := svc.Sources(ctx, scope, f.examID)
		if err != nil || len(sources) != 1 {
			t.Fatalf("sources=%+v err=%v", sources, err)
		}
		source := sources[0]
		input := paper.CreateReferenceCropInput{CommandID: uuid.NewString(), PageRef: source.PageRef, PageSHA256: source.SHA256, Width: source.Width, Height: source.Height, Box: paper.ReferencePixelBox{X: 1, Y: 2, Width: 3, Height: 4}}
		objects.failPut.Store(true)
		crop, err := svc.CreateCrop(ctx, scope, f.examID, f.questionID, input)
		if err == nil || crop.CropRef == "" {
			t.Fatalf("expected registered pending crop=%+v err=%v", crop, err)
		}
		// 对象写入在命令事务提交后中断；并发恢复必须仍指向同一资产并正确重建原坐标。
		var workers sync.WaitGroup
		errorsOut := make(chan error, 2)
		for range 2 {
			workers.Add(1)
			go func() {
				defer workers.Done()
				receipt, err := svc.ResumeCommand(ctx, scope, f.examID, f.questionID, input.CommandID)
				if err == nil && receipt.Status != "succeeded" {
					err = fmt.Errorf("resume status=%s", receipt.Status)
				}
				errorsOut <- err
			}()
		}
		workers.Wait()
		close(errorsOut)
		for err := range errorsOut {
			if err != nil {
				t.Fatal(err)
			}
		}
		var receiptItem paper.ReferenceMaterialItem
		receipt, err := svc.Recover(ctx, scope, f.examID, input.CommandID)
		if err != nil || receipt.Status != "succeeded" || json.Unmarshal(receipt.Result, &receiptItem) != nil || receiptItem.Box != input.Box || receiptItem.Source.Width != 16 || receiptItem.Source.Height != 12 || receiptItem.Source.Identity == "" {
			t.Fatalf("recovered coordinate metadata=%+v receipt=%+v err=%v", receiptItem, receipt, err)
		}
		crop, err = svc.CreateCrop(ctx, scope, f.examID, f.questionID, input)
		if err != nil {
			t.Fatal(err)
		}
		m, err := svc.Save(ctx, scope, f.examID, f.questionID, a01AuditSaveInput(crop.CropRef, 0))
		if err != nil {
			t.Fatal(err)
		}
		loaded, err := svc.Get(ctx, f.tenantID, f.examID, f.questionID)
		if err != nil || len(loaded.Items) != 1 || loaded.Items[0].Box != input.Box || loaded.ContentHash != m.ContentHash || loaded.Items[0].Source.SourceHash != asset.HashSHA256 {
			t.Fatalf("stored coordinate metadata=%+v err=%v", loaded, err)
		}
		// 同色图的两个等大框生成相同 PNG；清单摘要仍须区分实际选择的页坐标。
		input.CommandID = uuid.NewString()
		input.Box.X++
		second, err := svc.CreateCrop(ctx, scope, f.examID, f.questionID, input)
		if err != nil || second.SHA256 != crop.SHA256 {
			t.Fatalf("equal pixels crop=%+v err=%v", second, err)
		}
		m2, err := svc.Save(ctx, scope, f.examID, f.questionID, a01AuditSaveInput(second.CropRef, 1))
		if err != nil || m2.ContentHash == m.ContentHash {
			t.Fatalf("coordinates omitted from manifest hash: %s %s err=%v", m.ContentHash, m2.ContentHash, err)
		}
		if _, err = svc.CropImage(ctx, scope, f.examID, uuid.NewString(), crop.CropRef); !errors.Is(err, files.ErrForbidden) {
			t.Fatalf("cross-question crop read=%v", err)
		}
		if _, err = svc.ResumeCommand(ctx, scope, f.examID, uuid.NewString(), input.CommandID); !errors.Is(err, paper.ErrConflict) {
			t.Fatalf("cross-question command resume=%v", err)
		}
		replaced, err := paper.NewPostgresStore(db).ReplacePaperImportSources(ctx, f.tenantID, job.ID, f.actorID, paper.ReplacePaperImportSourcesInput{CommandID: uuid.NewString(), ExpectedGeneration: job.Generation, Sources: []paper.ReplacePaperImportSourceInput{{ID: job.Sources[0].ID, DocumentIndex: 0, RoleHint: "question"}}})
		if err != nil {
			t.Fatal(err)
		}
		if _, err = svc.Save(ctx, scope, f.examID, f.questionID, a01AuditSaveInput(second.CropRef, 2)); !errors.Is(err, paper.ErrConflict) {
			t.Fatalf("stale source generation saved=%v", err)
		}
		// 移除来源只软删导入关系，文件仍为 active；不能把旧关系包装成新代次可选来源。
		if _, err = paper.NewPostgresStore(db).ReplacePaperImportSources(ctx, f.tenantID, job.ID, f.actorID, paper.ReplacePaperImportSourcesInput{CommandID: uuid.NewString(), ExpectedGeneration: replaced.Generation, Sources: []paper.ReplacePaperImportSourceInput{}}); err != nil {
			t.Fatal(err)
		}
		if removed, err := svc.Sources(ctx, scope, f.examID); err != nil || len(removed) != 0 {
			t.Fatalf("removed sources reappeared=%+v err=%v", removed, err)
		}
	})

	t.Run("schema_failure_preserves_receipt_and_ledger_usage", func(t *testing.T) {
		f := seedA01Dispatch(t, db)
		addA01Readiness(t, db, f, f.examID, strings.Repeat("a", 64), f.manifestID)
		policy := subjective.ModelPolicy{ModelVersion: "trial-model-v1", PromptVersion: "trial-prompt-v1", MinConfidence: .8}
		store := subjective.NewPostgresStore(db)
		trial, err := store.CreateScoringTrial(ctx, f.tenantID, f.actorID, f.runID, f.scoringTrialPostgresFixture.input(), policy)
		if err != nil {
			t.Fatal(err)
		}
		run, request, err := insertA01DispatchRun(db, f, f.snapshotID)
		if err != nil {
			t.Fatal(err)
		}
		mustScoringTrialExec(t, db, `UPDATE subjective_grading_run SET batch_id=$2 WHERE id=$1`, run, trial.SampleBatchID)
		receipts := subjective.NewMaterialReceiptStore(db, strings.Repeat("s", 32)).WithImageAdmission(func(context.Context, string, string, string) error { return nil })
		digest, wire := strings.Repeat("d", 64), strings.Repeat("f", 64)
		p, err := receipts.Prepare(ctx, f.tenantID, request, digest, strings.Repeat("e", 64), f.media)
		if err != nil {
			t.Fatal(err)
		}
		assertA01Callback(t, receipts, p, "consume", digest, wire, nil, 200)
		assertA01Callback(t, receipts, p, "provider_response_received", digest, wire, map[string]int64{"total_tokens": 17}, 200)
		mustScoringTrialExec(t, db, `UPDATE subjective_grading_run SET status='failed',error_code='agent_response_invalid' WHERE id=$1`, run)
		assertUsage := func(want int64) {
			t.Helper()
			trials, err := store.ListScoringTrials(ctx, f.tenantID, f.runID, policy)
			if err != nil || len(trials) != 1 {
				t.Fatalf("trial projection=%+v err=%v", trials, err)
			}
			for _, sample := range trials[0].Samples {
				if sample.SegmentID == f.segments[0] {
					result := sample.Result
					if result == nil || result.GradeID != "" || result.SuggestedScore != nil || result.TotalTokens == nil || *result.TotalTokens != want || result.MaterialReceipt == nil || result.MaterialReceipt.Stage != "provider_response_received" {
						t.Fatalf("failed sample usage=%+v want=%d", result, want)
					}
					return
				}
			}
			t.Fatal("missing failed trial sample")
		}
		assertUsage(17)
		mustScoringTrialExec(t, db, `INSERT INTO model_usage_event(tenant_id,request_id,feature,provider_key,model_name,total_tokens,status) VALUES($1,$2,'subjective_grading','synthetic','trial-model-v1',23,'failed')`, f.tenantID, request)
		assertUsage(23)
	})
}

func a01AuditSaveInput(crop string, revision int) paper.SaveReferenceMaterialsInput {
	var input paper.SaveReferenceMaterialsInput
	_ = json.Unmarshal([]byte(fmt.Sprintf(`{"command_id":%q,"expected_revision":%d,"mode":"reference_images","items":[{"crop_ref":%q,"role":"question_diagram","ordinal":1}]}`, uuid.NewString(), revision, crop)), &input)
	return input
}
