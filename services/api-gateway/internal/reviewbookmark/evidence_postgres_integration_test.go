package reviewbookmark

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
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/files"
	"edugrade-enterprise/services/api-gateway/internal/segment"
	"github.com/google/uuid"
)

type bookmarkEvidenceFixture struct {
	exam, question, submission, segment, page, sourceAsset string
	snapshot, registration, crop, hash, gold               string
	bytes                                                  []byte
}

// 真实 PostgreSQL 的无 BYPASSRLS 账号读取普通与暗样本，只以合成对象存储替代外部服务。
// 同人同题两次暗样本必须逐 task 定位；历史意见、当前图像和公开响应分别验证。
func TestPostgresBookmarkEvidenceIsolationAndVersions(t *testing.T) {
	admin, runtimeDB, user, task := bookmarkIntegrationDB(t)
	ctx := auth.WithUser(context.Background(), user)
	provider := NewPostgresSourceProvider(runtimeDB)
	store := NewPostgresStore(runtimeDB, provider)
	f := bookmarkSeedEvidenceFixture(t, admin, user, task)

	if _, _, err := provider.ResolveEvidence(ctx, user, task); !errors.Is(err, ErrEvidenceUnavailable) {
		t.Fatalf("draft definition must not appear frozen: %v", err)
	}
	bookmarkEvidenceExec(t, admin, `UPDATE exam SET status='ready' WHERE id=$1::uuid`, f.exam)
	for _, query := range []string{`UPDATE question SET stem='后来改题干' WHERE id=$1::uuid`, `UPDATE question SET options='["后来改选项"]' WHERE id=$1::uuid`} {
		if _, err := admin.Exec(query, f.question); err == nil {
			t.Fatal("ready question definition was mutable")
		}
	}

	objects := files.NewMemoryObjectStorage()
	if err := objects.Put(ctx, "bookmark-evidence", f.crop, bytes.NewReader(f.bytes), int64(len(f.bytes)), "image/png"); err != nil {
		t.Fatal(err)
	}
	images := segment.NewImageService(segment.NewPostgresStore(runtimeDB), nil, files.NewPostgresStore(runtimeDB), objects)
	evidence := NewEvidenceService(provider, images, []byte("synthetic-bookmark-evidence-secret"))
	handler := NewHandler(store).WithEvidence(evidence)
	normal, err := store.Put(ctx, user, task, PutInput{Reason: "仅本人说明"})
	if err != nil {
		t.Fatal(err)
	}
	seedOne, seedTwo := uuid.NewString(), uuid.NewString()
	bookmarkInsertSeedEvidenceTask(t, admin, user, f, seedOne)
	bookmarkInsertSeedEvidenceTask(t, admin, user, f, seedTwo)
	if _, err = provider.Resolve(ctx, user, seedOne); !errors.Is(err, ErrNotFound) {
		t.Fatalf("completed seed without its own submission was readable: %v", err)
	}
	bookmarkInsertSeedEvidenceOpinion(t, admin, user, f, seedOne, 2)
	bookmarkInsertSeedEvidenceOpinion(t, admin, user, f, seedTwo, 4)
	first, err := store.Put(ctx, user, seedOne, PutInput{})
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.Put(ctx, user, seedTwo, PutInput{})
	if err != nil {
		t.Fatal(err)
	}
	bookmarkEvidenceExec(t, admin, `INSERT INTO human_grade(tenant_id,review_task_id,answer_segment_id,exam_question_snapshot_id,reviewer_id,score,max_score,rubric_selections,grade_round,comments,private_note) VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5::uuid,3,5,'[{"point_id":"p1","score":3}]','single','不得公开的历史评语','不得公开的私有备注')`, user.TenantID, task, f.segment, f.snapshot, user.ID)
	bookmarkEvidenceExec(t, admin, `UPDATE review_task SET status='submitted' WHERE id=$1::uuid`, task)
	var version string
	for _, entry := range []struct {
		ack   Acknowledgement
		score float64
	}{{normal, 3}, {first, 2}, {second, 4}} {
		record, e := store.GetByID(ctx, user, entry.ack.BookmarkID)
		if e != nil {
			t.Fatal(e)
		}
		result, e := evidence.Context(ctx, user, record)
		if e != nil {
			t.Fatal(e)
		}
		if !result.Readonly || result.OwnSubmission == nil || result.OwnSubmission.Score != entry.score || len(result.OwnSubmission.RubricSelections) != 1 || result.OwnSubmission.RubricSelections[0].Score != entry.score {
			t.Fatalf("exact task opinion: %+v", result)
		}
		if result.Question.Stem != "合成题干" || len(result.Question.Options) != 2 || result.Question.Rubric.MaxScore != 5 || len(result.Question.Rubric.Points) != 1 {
			t.Fatalf("frozen question projection: %+v", result.Question)
		}
		if result.Answer.Text != "" || result.Answer.TextBinding != "unavailable" || !result.Answer.ImageAvailable || result.Answer.MaterialChange != "unchanged" {
			t.Fatalf("equal source projection: %+v", result.Answer)
		}
		raw, _ := json.Marshal(result)
		for _, forbidden := range []string{f.segment, f.registration, f.crop, f.hash, f.gold, "reference_score", "seed_task_id", "gold_paper", "历史评语", "私有备注", "仅供来源内部识别文字"} {
			if bytes.Contains(raw, []byte(forbidden)) {
				t.Fatalf("source material escaped public projection: %s", forbidden)
			}
		}
		response := bookmarkEvidenceRequest(handler, user, http.MethodGet, entry.ack.BookmarkID, result.Answer.EvidenceVersion)
		if response.Code != http.StatusOK || !bytes.Equal(response.Body.Bytes(), f.bytes) || response.Header().Get("ETag") != "" || response.Header().Get("Content-Disposition") != `inline; filename="answer.png"` || response.Header().Get("Cache-Control") != "private, no-store" {
			t.Fatalf("safe image response: %d %v", response.Code, response.Header())
		}
		response = bookmarkEvidenceRequest(handler, user, http.MethodHead, entry.ack.BookmarkID, result.Answer.EvidenceVersion)
		if response.Code != http.StatusOK || response.Body.Len() != 0 {
			t.Fatalf("HEAD image response: %d %s", response.Code, response.Body.String())
		}
		if entry.ack.BookmarkID == first.BookmarkID {
			version = result.Answer.EvidenceVersion
		}
	}
	list, err := store.List(ctx, user, ListFilter{})
	if err != nil || list.TotalCount != 3 || len(list.Items) != 3 {
		t.Fatalf("both source kinds in authorized list: %+v %v", list, err)
	}

	// 裁剪改变不依赖 task revision；旧 context 的图像版本必须发生 409。
	bookmarkEvidenceExec(t, admin, `UPDATE answer_segment SET crop_sha256=$2 WHERE id=$1::uuid`, f.segment, strings.Repeat("b", 64))
	if response := bookmarkEvidenceRequest(handler, user, http.MethodGet, first.BookmarkID, version); response.Code != http.StatusConflict {
		t.Fatalf("old image version: %d %s", response.Code, response.Body.String())
	}
	record, err := store.GetByID(ctx, user, first.BookmarkID)
	if err != nil {
		t.Fatal(err)
	}
	changed, err := evidence.Context(ctx, user, record)
	if err != nil || changed.Answer.MaterialChange != "changed" || changed.Answer.EvidenceVersion == version {
		t.Fatalf("changed material: %+v %v", changed.Answer, err)
	}
	bookmarkEvidenceExec(t, admin, `UPDATE answer_segment SET crop_sha256=$2 WHERE id=$1::uuid`, f.segment, f.hash)

	// HEAD 和条件 GET 不能靠先前有效的版本令牌越过实时分配与学校检查。
	var other string
	if err = admin.QueryRow(`SELECT id::text FROM app_user WHERE tenant_id=$1::uuid AND id<>$2::uuid AND deleted_at IS NULL LIMIT 1`, user.TenantID, user.ID).Scan(&other); err != nil {
		t.Fatal(err)
	}
	bookmarkEvidenceExec(t, admin, `UPDATE seed_task SET grader_id=$2::uuid WHERE id=$1::uuid`, seedOne, other)
	for _, method := range []string{http.MethodGet, http.MethodHead} {
		if response := bookmarkEvidenceRequest(handler, user, method, first.BookmarkID, version); response.Code != http.StatusNotFound {
			t.Fatalf("reauthorize %s after reassignment: %d", method, response.Code)
		}
	}
	bookmarkEvidenceExec(t, admin, `UPDATE seed_task SET grader_id=$2::uuid WHERE id=$1::uuid`, seedOne, user.ID)
	bookmarkEvidenceExec(t, admin, `UPDATE app_user SET school_id=NULL WHERE id=$1::uuid`, user.ID)
	if response := bookmarkEvidenceRequest(handler, user, http.MethodHead, first.BookmarkID, version); response.Code != http.StatusNotFound {
		t.Fatalf("HEAD must recheck actual school: %d", response.Code)
	}
	bookmarkEvidenceExec(t, admin, `UPDATE app_user SET school_id=(SELECT school_id FROM exam WHERE id=$2::uuid) WHERE id=$1::uuid`, user.ID, f.exam)

	// 失效配准既不能由 context 宣称图片可用，也不能由版本化读取器继续打开旧资源。
	bookmarkEvidenceExec(t, admin, `UPDATE page_registration_run SET deleted_at=now() WHERE id=$1::uuid`, f.registration)
	record, err = store.GetByID(ctx, user, first.BookmarkID)
	if err != nil {
		t.Fatal(err)
	}
	unavailable, err := evidence.Context(ctx, user, record)
	if err != nil || unavailable.Answer.ImageAvailable {
		t.Fatalf("deleted registration available: %+v %v", unavailable.Answer, err)
	}
	if response := bookmarkEvidenceRequest(handler, user, http.MethodGet, first.BookmarkID, version); response.Code != http.StatusNotFound {
		t.Fatalf("deleted registration image: %d %s", response.Code, response.Body.String())
	}
	bookmarkEvidenceExec(t, admin, `UPDATE page_registration_run SET deleted_at=NULL WHERE id=$1::uuid`, f.registration)

	// 非标准来源路径和软删题块均须从清单/计数/筛选项中排除，清理仍只返回本人 ID 与版本。
	bookmarkEvidenceExec(t, admin, `UPDATE seed_task SET source_image_url='/unexpected/'||$2||'/image' WHERE id=$1::uuid`, seedOne, f.segment)
	if _, err = provider.Resolve(ctx, user, seedOne); !errors.Is(err, ErrNotFound) {
		t.Fatalf("noncanonical source path: %v", err)
	}
	list, err = store.List(ctx, user, ListFilter{})
	if err != nil || list.TotalCount != 2 || len(list.Items) != 2 {
		t.Fatalf("invalid source entered list: %+v %v", list, err)
	}
	cleanup, err := store.Cleanup(ctx, user)
	if err != nil || len(cleanup) != 1 || cleanup[0].BookmarkID != first.BookmarkID {
		t.Fatalf("invalid source cleanup: %+v %v", cleanup, err)
	}
	bookmarkEvidenceExec(t, admin, `UPDATE seed_task SET source_image_url=$2 WHERE id=$1::uuid`, seedOne, "/api/v1/answer-segments/"+f.segment+"/image")
	bookmarkEvidenceExec(t, admin, `UPDATE answer_segment SET deleted_at=now() WHERE id=$1::uuid`, f.segment)
	list, err = store.List(ctx, user, ListFilter{})
	if err != nil || list.TotalCount != 0 || len(list.Items) != 0 || len(list.Exams) != 0 || len(list.Questions) != 0 {
		t.Fatalf("deleted source entered authorized projection: %+v %v", list, err)
	}
	cleanup, err = store.Cleanup(ctx, user)
	if err != nil || len(cleanup) != 3 {
		t.Fatalf("source cleanup missing records: %+v %v", cleanup, err)
	}
	bookmarkEvidenceExec(t, admin, `UPDATE answer_segment SET deleted_at=NULL WHERE id=$1::uuid`, f.segment)
	if _, err = store.Delete(ctx, user, first.BookmarkID, DeleteInput{ExpectedRevision: first.Revision}); err != nil {
		t.Fatal(err)
	}
	if response := bookmarkEvidenceRequest(handler, user, http.MethodGet, first.BookmarkID, version); response.Code != http.StatusNotFound {
		t.Fatalf("cancelled bookmark reopened: %d", response.Code)
	}
}

func bookmarkEvidenceRequest(handler *Handler, user auth.User, method, bookmark, version string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, "/api/v1/me/review-bookmarks/"+bookmark+"/image?version="+version, nil)
	request.Header.Set("If-None-Match", `"old-source-etag"`)
	request.SetPathValue("bookmarkId", bookmark)
	request = request.WithContext(auth.WithUser(request.Context(), user))
	response := httptest.NewRecorder()
	handler.GetImage(response, request)
	return response
}

func bookmarkEvidenceExec(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.Exec(query, args...); err != nil {
		t.Fatalf("evidence fixture: %v", err)
	}
}

func bookmarkSeedEvidenceFixture(t *testing.T, db *sql.DB, user auth.User, task string) bookmarkEvidenceFixture {
	t.Helper()
	var f bookmarkEvidenceFixture
	if err := db.QueryRow(`SELECT t.exam_id::text,t.question_id::text,t.submission_id::text,t.answer_segment_id::text,s.submission_page_id::text,p.file_asset_id::text FROM review_task t JOIN answer_segment s ON s.id=t.answer_segment_id JOIN submission_page p ON p.id=s.submission_page_id WHERE t.id=$1::uuid`, task).Scan(&f.exam, &f.question, &f.submission, &f.segment, &f.page, &f.sourceAsset); err != nil {
		t.Fatal(err)
	}
	rubricVersion := uuid.NewString()
	bookmarkEvidenceExec(t, db, `UPDATE question SET options='["合成选项甲","合成选项乙"]' WHERE id=$1::uuid`, f.question)
	bookmarkEvidenceExec(t, db, `INSERT INTO rubric_version(id,tenant_id,question_id,version,status,content_hash,created_by) VALUES($1::uuid,$2::uuid,$3::uuid,'bookmark-fixture','approved',$4,$5::uuid)`, rubricVersion, user.TenantID, f.question, strings.Repeat("c", 64), user.ID)
	bookmarkEvidenceExec(t, db, `INSERT INTO question_rubric(tenant_id,question_id,rubric_version_id,status,max_score,points,created_by) VALUES($1::uuid,$2::uuid,$3::uuid,'approved',5,'[{"id":"p1","description":"合成评分点","score":5}]',$4::uuid)`, user.TenantID, f.question, rubricVersion, user.ID)
	bookmarkEvidenceExec(t, db, `SELECT assessment_apply_default_question_config($1::uuid,$2::uuid,$3::uuid)`, user.TenantID, f.exam, f.question)
	if err := db.QueryRow(`SELECT assessment_freeze_question_snapshot($1::uuid,$2::uuid,$3::uuid)::text`, user.TenantID, f.exam, f.question).Scan(&f.snapshot); err != nil {
		t.Fatal(err)
	}
	paper, template, batch, captureFile, capturePage := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	f.registration, f.crop, f.gold = uuid.NewString(), uuid.NewString(), uuid.NewString()
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	f.bytes = encoded.Bytes()
	f.hash = fmt.Sprintf("%x", sha256.Sum256(f.bytes))
	bookmarkEvidenceExec(t, db, `INSERT INTO exam_paper(id,tenant_id,exam_id,file_asset_id,version_no,status,uploaded_by) VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,1,'ready',$5::uuid)`, paper, user.TenantID, f.exam, f.sourceAsset, user.ID)
	bookmarkEvidenceExec(t, db, `INSERT INTO answer_sheet_template(id,tenant_id,exam_id,exam_paper_id,version_no,name,status,page_count,layout,content_hash,created_by) VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,1,'合成模板','locked',1,'{}',$5,$6::uuid)`, template, user.TenantID, f.exam, paper, strings.Repeat("d", 64), user.ID)
	bookmarkEvidenceExec(t, db, `INSERT INTO capture_batch(id,tenant_id,exam_id,name,source_type,status,operator_id) VALUES($1::uuid,$2::uuid,$3::uuid,'合成证据批次','web_upload','completed',$4::uuid)`, batch, user.TenantID, f.exam, user.ID)
	bookmarkEvidenceExec(t, db, `INSERT INTO capture_file(id,tenant_id,capture_batch_id,file_asset_id,original_name,content_type,sha256,byte_size,page_count,status,idempotency_key,uploaded_by) VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,'synthetic.png','image/png',$5,1,1,'completed',$1::text,$6::uuid)`, captureFile, user.TenantID, batch, f.sourceAsset, strings.Repeat("a", 64), user.ID)
	bookmarkEvidenceExec(t, db, `INSERT INTO capture_page(id,tenant_id,capture_batch_id,capture_file_id,source_index,submission_id,submission_page_id,assigned_page_no,sequence_no,decoded_file_asset_id,status) VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,1,$5::uuid,$6::uuid,1,1,$7::uuid,'ready')`, capturePage, user.TenantID, batch, captureFile, f.submission, f.page, f.sourceAsset)
	bookmarkEvidenceExec(t, db, `INSERT INTO page_registration_run(id,tenant_id,capture_page_id,submission_page_id,source_file_asset_id,source_sha256,template_id,template_content_hash,page_no,processing_status,match_status,profile_version) VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5::uuid,$6,$7::uuid,$8,1,'completed','matched','synthetic-bookmark')`, f.registration, user.TenantID, capturePage, f.page, f.sourceAsset, strings.Repeat("a", 64), template, strings.Repeat("d", 64))
	bookmarkEvidenceExec(t, db, `INSERT INTO file_asset(id,tenant_id,exam_id,owner_type,owner_id,original_name,content_type,size_bytes,hash_sha256,storage_bucket,storage_key,visibility,uploaded_by) VALUES($1::uuid,$2::uuid,$3::uuid,'answer_segment_crop',$4::uuid,$5,'image/png',$6,$7,'bookmark-evidence',$1::text,'private',$8::uuid)`, f.crop, user.TenantID, f.exam, f.registration, "private-"+f.segment+".png", len(f.bytes), f.hash, user.ID)
	bookmarkEvidenceExec(t, db, `UPDATE answer_segment SET registration_run_id=$2::uuid,crop_file_asset_id=$3::uuid,crop_sha256=$4,processing_status='completed' WHERE id=$1::uuid`, f.segment, f.registration, f.crop, f.hash)
	bookmarkEvidenceExec(t, db, `INSERT INTO answer_segment_answer(tenant_id,answer_segment_id,answer_text,source,recorded_by) VALUES($1::uuid,$2::uuid,'仅供来源内部识别文字','ocr_text',$3::uuid)`, user.TenantID, f.segment, user.ID)
	bookmarkEvidenceExec(t, db, `INSERT INTO grading_gold_paper(id,tenant_id,exam_id,question_id,submission_id,nominated_by) VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5::uuid,$6::uuid)`, f.gold, user.TenantID, f.exam, f.question, f.submission, user.ID)
	bookmarkEvidenceExec(t, db, `INSERT INTO grading_gold_paper_version(tenant_id,gold_paper_id,version,exam_question_snapshot_id,reference_score,max_score,rubric_snapshot_json,explanation,source_grade_ids_json,nominated_by,approved_by,approved_at) VALUES($1::uuid,$2::uuid,1,$3::uuid,5,5,'{}','合成参考分不得公开','["synthetic-grade"]',$4::uuid,$4::uuid,now())`, user.TenantID, f.gold, f.snapshot, user.ID)
	return f
}

func bookmarkInsertSeedEvidenceTask(t *testing.T, db *sql.DB, user auth.User, f bookmarkEvidenceFixture, task string) {
	t.Helper()
	bookmarkEvidenceExec(t, db, `INSERT INTO seed_task(id,tenant_id,exam_id,question_id,question_no,grader_id,gold_paper_id,gold_version,exam_question_snapshot_id,anonymous_code,source_image_url,reference_score,max_score,archetype_code,status) VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,'Q1',$5::uuid,$6::uuid,1,$7::uuid,'ANON-SYNTHETIC',$8,5,5,'short_constructed','completed')`, task, user.TenantID, f.exam, f.question, user.ID, f.gold, f.snapshot, "/api/v1/answer-segments/"+f.segment+"/image")
}

func bookmarkInsertSeedEvidenceOpinion(t *testing.T, db *sql.DB, user auth.User, f bookmarkEvidenceFixture, task string, score int) {
	t.Helper()
	bookmarkEvidenceExec(t, db, `INSERT INTO seed_observation(tenant_id,seed_task_id,exam_id,question_id,grader_id,gold_paper_id,gold_version,submitted_score,reference_score,rubric_selection_json,error,observation_kind,criterion_observation_json) VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5::uuid,$6::uuid,1,$7::numeric,5,jsonb_build_object('p1',$7::numeric),5-$7::numeric,'criterion','{}')`, user.TenantID, task, f.exam, f.question, user.ID, f.gold, score)
}

func TestBookmarkSourceURLRequiresCanonicalCropPath(t *testing.T) {
	for _, raw := range []string{"https://example.test/api/v1/answer-segments/x/image", "/unexpected/x/image", "/api/v1/answer-segments/x/image?x=1", "/api/v1/answer-segments/x/image#fragment", "/api/v1/answer-segments/%78/image", "api/v1/answer-segments/x/image", "/api/v1/answer-segments/x/image/"} {
		if got := segmentFromURL(raw); got != "" {
			t.Fatalf("noncanonical path %q resolved %q", raw, got)
		}
	}
	if got := segmentFromURL("/api/v1/answer-segments/x/image"); got != "x" {
		t.Fatalf("canonical path resolved %q", got)
	}
}
