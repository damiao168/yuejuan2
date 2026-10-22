package server

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/subjective"
	"github.com/google/uuid"
)

type a01DispatchFixture struct {
	scoringTrialPostgresFixture
	snapshotID, manifestID, mediaID string
	media                           []subjective.ReferenceMedia
}

// 每次在独立数据库应用全部迁移；收据回调走真实 SQL 事务，不运行模型或 worker。
func TestA01MaterialDispatchPostgres(t *testing.T) {
	dsn := os.Getenv("EDUGRADE_E2E_DATABASE_URL")
	if dsn == "" {
		t.Skip("EDUGRADE_E2E_DATABASE_URL is required")
	}
	db := e2eOpenPostgresTestDB(t, dsn)
	e2eApplyPostgresMigrations(t, db)
	store := subjective.NewMaterialReceiptStore(db, strings.Repeat("s", 32)).WithImageAdmission(func(context.Context, string, string, string) error { return nil })
	digest, wrapper, wire := strings.Repeat("d", 64), strings.Repeat("e", 64), strings.Repeat("f", 64)

	t.Run("exact_frozen_companion", func(t *testing.T) {
		f := seedA01Dispatch(t, db)
		addA01Readiness(t, db, f, f.examID, strings.Repeat("a", 64), f.manifestID)
		// 较新的异考试和错误哈希记录都不能覆盖准确 companion；草稿新材料也不换掉旧冻结版本。
		other := seedScoringTrialPostgres(t, db)
		addA01Readiness(t, db, f, other.examID, strings.Repeat("a", 64), f.manifestID)
		addA01Readiness(t, db, f, f.examID, strings.Repeat("b", 64), f.manifestID)
		mustScoringTrialExec(t, db, `INSERT INTO question_reference_manifest(tenant_id,exam_id,question_id,revision,previous_manifest_id,mode,content_hash,command_id,request_hash,created_by) VALUES($1,$2,$3,2,$4,'text_only',repeat('b',64),$5,repeat('c',64),$6)`, f.tenantID, f.examID, f.questionID, f.manifestID, uuid.NewString(), f.actorID)
		run, request, err := insertA01DispatchRun(db, f, f.snapshotID)
		if err != nil {
			t.Fatal(err)
		}
		var manifest, hash, profile string
		var revision int
		if err = db.QueryRow(`SELECT reference_manifest_id::text,reference_manifest_hash,reference_authorization_revision,material_input_profile FROM subjective_grading_run WHERE id=$1`, run).Scan(&manifest, &hash, &revision, &profile); err != nil || manifest != f.manifestID || hash != strings.Repeat("c", 64) || revision != 1 || profile != "material-input-v1" {
			t.Fatalf("frozen binding=%s %s %d %s err=%v", manifest, hash, revision, profile, err)
		}
		permit, err := store.Prepare(context.Background(), f.tenantID, request, digest, wrapper, f.media)
		if err != nil || permit.RunID != run {
			t.Fatalf("prepare=%+v err=%v", permit, err)
		}
		for _, mutation := range []string{
			`UPDATE subjective_grading_run SET material_input_profile='text-only-v1' WHERE id=$1`,
			`UPDATE subjective_grading_run SET math_wrapper_hash=repeat('a',64) WHERE id=$1`,
			`UPDATE subjective_grading_run SET exam_question_snapshot_id=NULL WHERE id=$1`,
		} {
			if _, err = db.Exec(mutation, run); err == nil {
				t.Fatalf("frozen run mutation accepted: %s", mutation)
			}
		}
	})

	t.Run("missing_or_mismatched_snapshot", func(t *testing.T) {
		f := seedA01Dispatch(t, db)
		addA01Readiness(t, db, f, f.examID, strings.Repeat("a", 64), f.manifestID)
		if _, _, err := insertA01DispatchRun(db, f, ""); err == nil {
			t.Fatal("material run accepted without exact snapshot")
		}
		other := seedScoringTrialPostgres(t, db)
		var foreignSnapshot string
		if err := db.QueryRow(`SELECT id::text FROM exam_question_snapshot WHERE question_id=$1`, other.questionID).Scan(&foreignSnapshot); err != nil {
			t.Fatal(err)
		}
		if _, _, err := insertA01DispatchRun(db, f, foreignSnapshot); err == nil {
			t.Fatal("material run accepted another exam's snapshot")
		}
		var unfrozenSnapshot string
		if err := db.QueryRow(`INSERT INTO exam_question_snapshot(tenant_id,exam_id,question_id,snapshot_version,subject_profile_id,subject_profile_version,archetype_code,allowed_evidence_types,risk_tier,profile_snapshot_json,archetype_snapshot_json,rubric_snapshot_json,scoring_policy_snapshot_json,content_hash) SELECT tenant_id,exam_id,question_id,2,subject_profile_id,subject_profile_version,archetype_code,allowed_evidence_types,risk_tier,profile_snapshot_json,archetype_snapshot_json,rubric_snapshot_json,scoring_policy_snapshot_json,repeat('b',64) FROM exam_question_snapshot WHERE id=$1 RETURNING id::text`, f.snapshotID).Scan(&unfrozenSnapshot); err != nil {
			t.Fatal(err)
		}
		if _, _, err := insertA01DispatchRun(db, f, unfrozenSnapshot); err == nil {
			t.Fatal("unpaired snapshot bypassed existing frozen material binding")
		}
		bad := seedA01Dispatch(t, db)
		addA01Readiness(t, db, bad, bad.examID, strings.Repeat("b", 64), bad.manifestID)
		if _, _, err := insertA01DispatchRun(db, bad, bad.snapshotID); err == nil {
			t.Fatal("material run downgraded to text after companion hash mismatch")
		}
		legacy := seedA01Dispatch(t, db)
		if _, _, err := insertA01DispatchRun(db, legacy, ""); err != nil {
			t.Fatalf("legacy no-material run rejected: %v", err)
		}
	})

	t.Run("consume_cas_binding_and_stage_replay", func(t *testing.T) {
		f := seedA01Dispatch(t, db)
		addA01Readiness(t, db, f, f.examID, strings.Repeat("a", 64), f.manifestID)
		_, request, err := insertA01DispatchRun(db, f, f.snapshotID)
		if err != nil {
			t.Fatal(err)
		}
		p, err := store.Prepare(context.Background(), f.tenantID, request, digest, wrapper, f.media)
		if err != nil {
			t.Fatal(err)
		}
		replay, err := store.Prepare(context.Background(), f.tenantID, request, digest, wrapper, f.media)
		if err != nil || replay != p {
			t.Fatalf("prepared identity changed=%+v err=%v", replay, err)
		}
		if _, err = store.Prepare(context.Background(), f.tenantID, request, strings.Repeat("a", 64), wrapper, f.media); err == nil {
			t.Fatal("changed request digest reused permit")
		}
		changed := append([]subjective.ReferenceMedia(nil), f.media...)
		changed[0].WidthPixels++
		if _, err = store.Prepare(context.Background(), f.tenantID, request, digest, wrapper, changed); err == nil {
			t.Fatal("changed media reused permit")
		}
		_, changedRequest, err := insertA01DispatchRun(db, f, f.snapshotID)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = store.Prepare(context.Background(), f.tenantID, changedRequest, digest, wrapper, changed); err == nil {
			t.Fatal("new permit accepted media outside persisted manifest")
		}
		// 过期许可仍是原执行身份，不能续期或发放新许可冒充首次发送。
		expiredRun, expiredRequest, err := insertA01DispatchRun(db, f, f.snapshotID)
		if err != nil {
			t.Fatal(err)
		}
		mustScoringTrialExec(t, db, `INSERT INTO subjective_request_material_receipt(tenant_id,run_id,execution_attempt_id,stage,request_id,request_digest,manifest_id,manifest_hash,authorization_revision,wrapper_hash,ordered_media_json,credential_hash,expires_at) SELECT tenant_id,$2,gen_random_uuid(),'prepared',$3,request_digest,manifest_id,manifest_hash,authorization_revision,wrapper_hash,ordered_media_json,credential_hash,now()-interval '1 second' FROM subjective_request_material_receipt WHERE id=$1`, p.PermitID, expiredRun, expiredRequest)
		if _, err = store.Prepare(context.Background(), f.tenantID, expiredRequest, digest, wrapper, f.media); err == nil {
			t.Fatal("expired permit was renewed")
		}
		assertA01Callback(t, store, p, "provider_request_attempted", digest, wire, nil, 409)
		invalid := p
		invalid.Credential = p.Credential[:len(p.Credential)-1] + "0"
		if invalid.Credential == p.Credential {
			invalid.Credential = p.Credential[:len(p.Credential)-1] + "1"
		}
		assertA01Callback(t, store, invalid, "consume", digest, wire, nil, 401)
		invalid = p
		invalid.RunID = uuid.NewString()
		assertA01Callback(t, store, invalid, "consume", digest, wire, nil, 401)
		assertA01Callback(t, store, p, "consume", strings.Repeat("b", 64), wire, nil, 409)
		assertA01Callback(t, store, p, "consume", digest, "", nil, 400)
		denied := subjective.NewMaterialReceiptStore(db, strings.Repeat("s", 32)).WithImageAdmission(func(context.Context, string, string, string) error { return fmt.Errorf("not admitted") })
		assertA01Callback(t, denied, p, "consume", digest, wire, nil, 403)
		// 同时领取同一许可，仅一次消费返回成功；失败方不能把成功响应当成可重发许可。
		const competitors = 12
		results := make(chan int, competitors)
		var workers sync.WaitGroup
		for range competitors {
			workers.Add(1)
			go func() {
				defer workers.Done()
				results <- a01Callback(store, p, "consume", digest, wire, nil).Code
			}()
		}
		workers.Wait()
		close(results)
		success := 0
		for code := range results {
			if code == 200 {
				success++
			} else if code != 409 {
				t.Errorf("consume status=%d", code)
			}
		}
		if success != 1 {
			t.Fatalf("successful consumers=%d", success)
		}
		if _, err = store.Prepare(context.Background(), f.tenantID, request, digest, wrapper, f.media); err == nil {
			t.Fatal("consumed permit was offered for resend")
		}
		var actualWire string
		if err = db.QueryRow(`SELECT wire_payload_hash FROM subjective_request_material_receipt WHERE id=$1 AND consumed_at IS NOT NULL`, p.PermitID).Scan(&actualWire); err != nil || actualWire != wire {
			t.Fatalf("consume did not freeze wire=%s err=%v", actualWire, err)
		}
		assertA01Callback(t, store, p, "provider_request_attempted", digest, wire, nil, 200)
		assertA01Callback(t, store, p, "provider_request_attempted", digest, wire, nil, 200)
		assertA01Callback(t, store, p, "provider_request_attempted", digest, strings.Repeat("a", 64), nil, 409)
		assertA01Callback(t, store, p, "provider_response_received", digest, wire, map[string]int64{"input_tokens": 3, "output_tokens": 2, "total_tokens": 5}, 200)
		assertA01Callback(t, store, p, "provider_response_received", digest, wire, map[string]int64{"input_tokens": 3, "output_tokens": 2, "total_tokens": 5}, 200)
		assertA01Callback(t, store, p, "provider_response_received", digest, wire, map[string]int64{"total_tokens": 99}, 409)
		for _, mutation := range []string{
			`UPDATE subjective_request_material_receipt SET usage_json='{"total_tokens":99}' WHERE run_id=$1`,
			`UPDATE subjective_request_material_receipt SET consumed_at=NULL,wire_payload_hash=NULL WHERE run_id=$1 AND stage='prepared'`,
			`DELETE FROM subjective_request_material_receipt WHERE run_id=$1`,
			`INSERT INTO subjective_request_material_receipt(tenant_id,run_id,execution_attempt_id,stage,request_id,request_digest,manifest_id,manifest_hash,authorization_revision,wrapper_hash,ordered_media_json,credential_hash,expires_at,consumed_at,wire_payload_hash) SELECT tenant_id,run_id,execution_attempt_id,'unknown_after_attempt',request_id,repeat('a',64),manifest_id,manifest_hash,authorization_revision,wrapper_hash,ordered_media_json,credential_hash,expires_at,consumed_at,wire_payload_hash FROM subjective_request_material_receipt WHERE run_id=$1 AND stage='prepared'`,
		} {
			if _, err = db.Exec(mutation, p.RunID); err == nil {
				t.Fatalf("receipt history mutation accepted: %s", mutation)
			}
		}
		var count int
		if err = db.QueryRow(`SELECT count(*) FROM subjective_request_material_receipt WHERE run_id=$1`, p.RunID).Scan(&count); err != nil || count != 3 {
			t.Fatalf("receipt stages=%d err=%v", count, err)
		}
		for _, tenant := range []string{"", uuid.NewString(), f.tenantID} {
			tx, err := db.Begin()
			if err != nil {
				t.Fatal(err)
			}
			if _, err = tx.Exec(`SET LOCAL ROLE edugrade_tenant_runtime`); err == nil {
				_, err = tx.Exec(`SELECT set_config('edugrade.tenant_id',$1,true)`, tenant)
			}
			if err == nil {
				err = tx.QueryRow(`SELECT count(*) FROM subjective_request_material_receipt WHERE run_id=$1`, p.RunID).Scan(&count)
			}
			tx.Rollback()
			want := 0
			if tenant == f.tenantID {
				want = 3
			}
			if err != nil || count != want {
				t.Fatalf("receipt RLS tenant=%s count=%d want=%d err=%v", tenant, count, want, err)
			}
		}
	})

	for _, afterConsume := range []bool{false, true} {
		t.Run(fmt.Sprintf("withdraw_after_consume_%t", afterConsume), func(t *testing.T) {
			f := seedA01Dispatch(t, db)
			addA01Readiness(t, db, f, f.examID, strings.Repeat("a", 64), f.manifestID)
			_, request, err := insertA01DispatchRun(db, f, f.snapshotID)
			if err != nil {
				t.Fatal(err)
			}
			p, err := store.Prepare(context.Background(), f.tenantID, request, digest, wrapper, f.media)
			if err != nil {
				t.Fatal(err)
			}
			if afterConsume {
				assertA01Callback(t, store, p, "consume", digest, wire, nil, 200)
			}
			mustScoringTrialExec(t, db, `UPDATE question_reference_manifest SET use_state='withdrawn',authorization_revision=authorization_revision+1,withdrawn_at=now(),withdrawal_reason='合成撤回测试' WHERE id=$1`, f.manifestID)
			if afterConsume {
				// 已消费请求仍记录在途事实；撤回只阻止新消费和最终结算，不能删掉用量证据。
				assertA01Callback(t, store, p, "provider_request_attempted", digest, wire, nil, 200)
				assertA01Callback(t, store, p, "provider_response_received", digest, wire, map[string]int64{"total_tokens": 7}, 200)
			} else {
				assertA01Callback(t, store, p, "consume", digest, wire, nil, 409)
				var consumed bool
				if err = db.QueryRow(`SELECT consumed_at IS NOT NULL FROM subjective_request_material_receipt WHERE id=$1`, p.PermitID).Scan(&consumed); err != nil || consumed {
					t.Fatalf("withdrawn permit consumed=%t err=%v", consumed, err)
				}
			}
			if _, err = db.Exec(`UPDATE subjective_grading_run SET status='succeeded' WHERE id=$1`, p.RunID); err == nil {
				t.Fatal("withdrawn reference result settled")
			}
		})
	}

	t.Run("asset_quarantined_before_send", func(t *testing.T) {
		f := seedA01Dispatch(t, db)
		addA01Readiness(t, db, f, f.examID, strings.Repeat("a", 64), f.manifestID)
		_, request, err := insertA01DispatchRun(db, f, f.snapshotID)
		if err != nil {
			t.Fatal(err)
		}
		p, err := store.Prepare(context.Background(), f.tenantID, request, digest, wrapper, f.media)
		if err != nil {
			t.Fatal(err)
		}
		mustScoringTrialExec(t, db, `UPDATE file_asset SET lifecycle_status='quarantined' WHERE tenant_id=$1 AND exam_id=$2`, f.tenantID, f.examID)
		assertA01Callback(t, store, p, "consume", digest, wire, nil, 409)
		if _, err = db.Exec(`UPDATE subjective_grading_run SET status='succeeded' WHERE id=$1`, p.RunID); err == nil {
			t.Fatal("quarantined reference result settled")
		}
	})

	t.Run("settlement_before_withdrawal", func(t *testing.T) {
		f := seedA01Dispatch(t, db)
		addA01Readiness(t, db, f, f.examID, strings.Repeat("a", 64), f.manifestID)
		run, request, err := insertA01DispatchRun(db, f, f.snapshotID)
		if err != nil {
			t.Fatal(err)
		}
		p, err := store.Prepare(context.Background(), f.tenantID, request, digest, wrapper, f.media)
		if err != nil {
			t.Fatal(err)
		}
		assertA01Callback(t, store, p, "consume", digest, wire, nil, 200)
		assertA01Callback(t, store, p, "provider_response_received", digest, wire, nil, 200)
		mustScoringTrialExec(t, db, `UPDATE subjective_grading_run SET status='succeeded' WHERE id=$1`, run)
		mustScoringTrialExec(t, db, `UPDATE question_reference_manifest SET use_state='withdrawn',authorization_revision=2,withdrawn_at=now(),withdrawal_reason='结算后撤回' WHERE id=$1`, f.manifestID)
		if _, err = db.Exec(`UPDATE subjective_grading_run SET math_wrapper_hash=repeat('a',64) WHERE id=$1`, run); err == nil {
			t.Fatal("settled wrapper hash overwritten")
		}
		if _, err = db.Exec(`UPDATE subjective_grading_run SET material_input_profile='text-only-v1' WHERE id=$1`, run); err == nil {
			t.Fatal("settled material profile overwritten")
		}
	})
}

func seedA01Dispatch(t *testing.T, db *sql.DB) a01DispatchFixture {
	t.Helper()
	f := a01DispatchFixture{scoringTrialPostgresFixture: seedScoringTrialPostgres(t, db), manifestID: uuid.NewString(), mediaID: uuid.NewString()}
	if err := db.QueryRow(`SELECT id::text FROM exam_question_snapshot WHERE tenant_id=$1 AND question_id=$2`, f.tenantID, f.questionID).Scan(&f.snapshotID); err != nil {
		t.Fatal(err)
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`INSERT INTO question_reference_manifest(id,tenant_id,exam_id,question_id,revision,mode,content_hash,command_id,request_hash,created_by) VALUES($1,$2,$3,$4,1,'reference_images',repeat('c',64),$5,repeat('d',64),$6)`, f.manifestID, f.tenantID, f.examID, f.questionID, uuid.NewString(), f.actorID); err != nil {
		t.Fatal(err)
	}
	payload := fmt.Sprintf(`{"id":%q,"role":"question_diagram","ordinal":1,"required":true,"sha256":%q,"width":2,"height":2,"size_bytes":1}`, f.mediaID, strings.Repeat("a", 64))
	if _, err = tx.Exec(`INSERT INTO question_reference_manifest_item(id,tenant_id,manifest_id,ordinal,role,source_file_asset_id,page_file_asset_id,derived_file_asset_id,payload) SELECT $1,$2,$3,1,'question_diagram',id,id,id,$4 FROM file_asset WHERE tenant_id=$2 AND exam_id=$5 LIMIT 1`, f.mediaID, f.tenantID, f.manifestID, payload, f.examID); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	f.media = []subjective.ReferenceMedia{{MediaID: f.mediaID, Role: "question_diagram", Ordinal: 1, SHA256: strings.Repeat("a", 64), WidthPixels: 2, HeightPixels: 2, ByteSize: 1}}
	return f
}

func addA01Readiness(t *testing.T, db *sql.DB, f a01DispatchFixture, exam, snapshotHash, manifest string) {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"questions": []any{map[string]any{"id": f.questionID, "assessment_snapshot_id": f.snapshotID, "assessment_snapshot_hash": snapshotHash, "reference_materials": map[string]any{"manifest_id": manifest, "content_hash": strings.Repeat("c", 64), "revision": 1, "mode": "reference_images", "image_count": 1}}}})
	if err != nil {
		t.Fatal(err)
	}
	mustScoringTrialExec(t, db, `INSERT INTO exam_readiness_snapshot(tenant_id,exam_id,configuration_hash,status,checks,confirmed_by,import_snapshot_schema_version,import_snapshot_hash,import_snapshot_json) VALUES($1,$2,repeat('a',64),'passed','[]',$3,2,repeat('b',64),$4)`, f.tenantID, exam, f.actorID, raw)
}

func insertA01DispatchRun(db *sql.DB, f a01DispatchFixture, snapshot string) (string, string, error) {
	request := uuid.NewString()
	var run string
	err := db.QueryRow(`INSERT INTO subjective_grading_run(tenant_id,answer_segment_id,answer_version,question_id,rubric_version,model_version,prompt_version,request_id,status,exam_question_snapshot_id) VALUES($1,$2,'synthetic-answer',$3,'rubric-v1','synthetic-model','synthetic-prompt',$4,'processing',NULLIF($5,'')::uuid) RETURNING id::text`, f.tenantID, f.segments[0], f.questionID, request, snapshot).Scan(&run)
	return run, request, err
}

func a01Callback(store *subjective.MaterialReceiptStore, permit subjective.MaterialExecutionPermit, stage, digest, wire string, usage map[string]int64) *httptest.ResponseRecorder {
	raw, _ := json.Marshal(map[string]any{"stage": stage, "request_binding_hash": digest, "wire_payload_hash": wire, "usage": usage, "usage_state": "reported"})
	req := httptest.NewRequest(http.MethodPost, "/material-record", bytes.NewReader(raw))
	req.SetPathValue("runId", permit.RunID)
	req.SetPathValue("permitId", permit.PermitID)
	req.Header.Set("Authorization", "Bearer "+permit.Credential)
	response := httptest.NewRecorder()
	store.Callback(response, req)
	return response
}

func assertA01Callback(t *testing.T, store *subjective.MaterialReceiptStore, permit subjective.MaterialExecutionPermit, stage, digest, wire string, usage map[string]int64, want int) {
	t.Helper()
	response := a01Callback(store, permit, stage, digest, wire, usage)
	if response.Code != want {
		t.Fatalf("callback %s status=%d want=%d body=%s", stage, response.Code, want, response.Body.String())
	}
}
