package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/files"
	"edugrade-enterprise/services/api-gateway/internal/scorerelease"
	"github.com/google/uuid"
)

func TestHighScorePaperShareRedactsAndRevokesWithPostgresTestDatabase(t *testing.T) {
	db, router, _, f, answers, objects := repairFixture(t)
	ctx := context.Background()
	fileStore := files.NewPostgresStore(db)
	manager := scorerelease.NewPostgresHighScorePaperManager(db, fileStore, objects, "edugrade-story041-e2e")
	store := scorerelease.NewPostgresStore(db, nil)
	service := scorerelease.NewService(store).WithHighScorePaper(manager)
	release, err := service.Create(ctx, f.TenantID, f.ExamID, f.AdminID, scorerelease.CreateInput{
		Reason: "synthetic anonymous example", IdempotencyKey: "anonymous-paper-release-1",
		VisibilityPolicy: scorerelease.VisibilityPolicy{ShowQuestionScores: true, ShowHighScorePaper: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	// 历史单卷没有名次字段；即使两人同分也只读取原来选中的一份。
	if _, err = db.ExecContext(ctx, `UPDATE score_release SET visibility_policy=visibility_policy-'high_score_paper_top_n' WHERE id=$1::uuid AND status='draft'`, release.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Publish(ctx, f.TenantID, release.ID, f.AdminID); err != scorerelease.ErrAnonymousPaperUnavailable {
		t.Fatalf("missing identity regions and registration must block publish: %v", err)
	}
	top, other := e2eSelectAnonymousShareCandidates(t, ctx, db, f, release.ID, answers)
	sourceAssetID, imageData := e2eSeedAnonymousRegisteredPage(t, ctx, db, fileStore, objects, f, top, 40)
	if _, err := service.Publish(ctx, f.TenantID, release.ID, f.AdminID); err != nil {
		t.Fatalf("publish with verified anonymous page: %v", err)
	}
	studentView, err := store.StudentResult(ctx, f.TenantID, f.ExamID, other.student)
	if err != nil || studentView.HighScorePaper == nil || !studentView.HighScorePaper.Available ||
		len(studentView.HighScorePaper.Pages) != 1 || studentView.HighScorePaper.Pages[0].SubmissionPageID != "" {
		t.Fatalf("student result did not expose only anonymous page metadata: %+v %v", studentView.HighScorePaper, err)
	}
	questionID := f.QuestionIDs["single_choice"]
	var schoolCode, studentNo string
	if err := db.QueryRow(`SELECT sc.code,st.student_no FROM student st JOIN school sc ON sc.tenant_id=st.tenant_id AND sc.id=st.school_id WHERE st.tenant_id=$1 AND st.id=$2`, f.TenantID, other.student).Scan(&schoolCode, &studentNo); err != nil {
		t.Fatal(err)
	}
	studentToken := e2eLoginProvisionedStudent(t, router, schoolCode, studentNo, "SyntheticStudent123!")
	request := func(query string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/student/exams/"+f.ExamID+"/questions/"+questionID+"/page-image?variant=high_score"+query, nil)
		req.Header.Set("Authorization", "Bearer "+studentToken)
		res := httptest.NewRecorder()
		router.ServeHTTP(res, req)
		return res
	}
	response := request("")
	e2eAssertAnonymousIdentityMask(t, response, imageData)
	e2eAssertAnonymousSourceAuthorization(t, ctx, db, router, store, anonymousSourceIdentity{Fixture: f, Top: top, Other: other, QuestionID: questionID, SourceAssetID: sourceAssetID, StudentToken: studentToken})
	if err := service.RevokeHighScorePaper(ctx, f.TenantID, release.ID, f.AdminID); err != nil {
		t.Fatal(err)
	}
	if response := request(""); response.Code != http.StatusNotFound {
		t.Fatalf("revoked share status=%d, want 404", response.Code)
	}
	result, err := store.StudentResult(ctx, f.TenantID, f.ExamID, other.student)
	if err != nil || result.HighScorePaper != nil {
		t.Fatalf("revoked share still listed: %+v %v", result.HighScorePaper, err)
	}

	// 前一名同分时，两份卷的第1页必须分别登记和读取；旧发布的匿名标识不能复用。
	_, otherImage := e2eSeedAnonymousRegisteredPage(t, ctx, db, fileStore, objects, f, other, 90)
	next, err := service.Create(ctx, f.TenantID, f.ExamID, f.AdminID, scorerelease.CreateInput{
		Reason: "synthetic tied excellent papers", IdempotencyKey: "anonymous-paper-release-2",
		VisibilityPolicy: scorerelease.VisibilityPolicy{ShowQuestionScores: true, ShowHighScorePaper: true, HighScorePaperTopN: new(1)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.Publish(ctx, f.TenantID, next.ID, f.AdminID); err != nil {
		t.Fatalf("publish complete tied candidates: %v", err)
	}
	result, err = store.StudentResult(ctx, f.TenantID, f.ExamID, other.student)
	if err != nil || len(result.HighScorePapers) != 2 {
		t.Fatalf("top one with equal scores should share both papers: count=%d, %v", len(result.HighScorePapers), err)
	}
	first, second := result.HighScorePapers[0], result.HighScorePapers[1]
	if first.PaperID == "" || second.PaperID == "" || first.PaperID == second.PaperID || len(first.Pages) != 1 || len(second.Pages) != 1 || first.Pages[0].PageNo != 1 || second.Pages[0].PageNo != 1 {
		t.Fatal("selected anonymous papers did not preserve their separate page-one images")
	}
	e2eAssertAnonymousIdentityMask(t, request("&expected_release_id="+next.ID+"&paper_id="+first.PaperID), imageData)
	e2eAssertAnonymousIdentityMask(t, request("&expected_release_id="+next.ID+"&paper_id="+second.PaperID), otherImage)
	if response := request("&expected_release_id=" + release.ID + "&paper_id=" + studentView.HighScorePaper.PaperID); response.Code != http.StatusConflict {
		t.Fatalf("old anonymous page version status=%d, want 409", response.Code)
	}
	if response := request("&expected_release_id=" + next.ID + "&paper_id=" + top.submission); response.Code != http.StatusNotFound {
		t.Fatalf("source submission ID accepted as anonymous paper: status=%d", response.Code)
	}
	if err = service.RevokeHighScorePaper(ctx, f.TenantID, next.ID, f.AdminID); err != nil {
		t.Fatal(err)
	}
	if response := request("&expected_release_id=" + next.ID + "&paper_id=" + second.PaperID); response.Code != http.StatusNotFound {
		t.Fatalf("revoked selected share status=%d, want 404", response.Code)
	}
}

// 先从本次冻结发布项目选最高分提交，再从原合成答案中找两名不同学生；不复用客户端传入的同学身份。
func e2eSelectAnonymousShareCandidates(t *testing.T, ctx context.Context, db *sql.DB, f story056AcceptanceFixture, releaseID string, answers []repairAnswer) (repairAnswer, repairAnswer) {
	t.Helper()
	var highestSubmission string
	if err := db.QueryRowContext(ctx, `SELECT submission_id::text FROM score_release_item WHERE tenant_id=$1::uuid AND release_id=$2::uuid ORDER BY total_score DESC,submission_id LIMIT 1`, f.TenantID, releaseID).Scan(&highestSubmission); err != nil {
		t.Fatal(err)
	}
	var top, other repairAnswer
	for _, answer := range answers {
		if answer.submission == highestSubmission {
			top = answer
		} else {
			other = answer
		}
	}
	if top.page == "" || other.page == "" {
		t.Fatal("expected two distinct synthetic candidates")
	}
	return top, other
}

// 合成来源先完成身份区域模板和配准，只有这个已核对来源才能参与匿名页面发布。
func e2eSeedAnonymousRegisteredPage(t *testing.T, ctx context.Context, db *sql.DB, fileStore *files.PostgresStore, objects *files.MemoryObjectStorage, f story056AcceptanceFixture, top repairAnswer, answerRed uint8) (string, *image.RGBA) {
	t.Helper()
	sourceBytes, imageData := e2eAnonymousIdentityImage(t, answerRed)
	sourceHash := fmt.Sprintf("%x", sha256.Sum256(sourceBytes))
	sourceOwner := uuid.NewString()
	sourceKey := "tenant/" + f.TenantID + "/synthetic-registration/" + sourceOwner + ".png"
	if err := objects.Put(ctx, "edugrade-story041-e2e", sourceKey, bytes.NewReader(sourceBytes), int64(len(sourceBytes)), "image/png"); err != nil {
		t.Fatal(err)
	}
	sourceAsset, err := fileStore.Create(ctx, files.CreateAssetInput{
		TenantID: f.TenantID, ExamID: f.ExamID, OwnerType: "synthetic_registered_page", OwnerID: sourceOwner,
		OriginalName: "synthetic-registered-page.png", ContentType: "image/png", SizeBytes: int64(len(sourceBytes)),
		HashSHA256: sourceHash, StorageBucket: "edugrade-story041-e2e", StorageKey: sourceKey,
		Visibility: "private", UploadedBy: f.AdminID,
	})
	if err != nil {
		t.Fatal(err)
	}
	var templateID string
	templateHash := fmt.Sprintf("%x", sha256.Sum256([]byte("synthetic-identity-layout-v1")))
	if err := db.QueryRowContext(ctx, `INSERT INTO answer_sheet_template
(tenant_id,exam_id,exam_paper_id,version_no,name,status,page_count,layout,content_hash,created_by,locked_by,locked_at)
SELECT tenant_id,exam_id,exam_paper_id,
(SELECT max(used.version_no)+1 FROM answer_sheet_template used WHERE used.tenant_id=base.tenant_id AND used.exam_id=base.exam_id),
'Synthetic anonymous layout','locked',page_count,
jsonb_set(layout,'{pages,0,identity_regions}','[{"id":"identity-name-number-qr","x":0,"y":0,"width":0.5,"height":0.2}]'::jsonb),
$2,created_by,$3::uuid,now() FROM answer_sheet_template base WHERE tenant_id=$1::uuid AND id=$4::uuid
RETURNING id::text`, f.TenantID, templateHash, f.AdminID, f.TemplateID).Scan(&templateID); err != nil {
		t.Fatal(err)
	}
	var batchID, captureFileID, capturePageID string
	if err := db.QueryRowContext(ctx, `INSERT INTO capture_batch(tenant_id,exam_id,name,source_type,status,operator_id)
VALUES($1::uuid,$2::uuid,'Synthetic anonymous page','scanner_upload','completed',$3::uuid) RETURNING id::text`, f.TenantID, f.ExamID, f.AdminID).Scan(&batchID); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `INSERT INTO capture_file
(tenant_id,capture_batch_id,file_asset_id,original_name,content_type,sha256,byte_size,page_count,status,idempotency_key,uploaded_by)
VALUES($1::uuid,$2::uuid,$3::uuid,'synthetic.png','image/png',$4,$5,1,'completed',$6,$7::uuid) RETURNING id::text`,
		f.TenantID, batchID, sourceAsset.ID, sourceHash, len(sourceBytes), uuid.NewString(), f.AdminID).Scan(&captureFileID); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `INSERT INTO capture_page
(tenant_id,capture_batch_id,capture_file_id,source_index,submission_id,submission_page_id,assigned_page_no,sequence_no,decoded_file_asset_id,status)
VALUES($1::uuid,$2::uuid,$3::uuid,1,$4::uuid,$5::uuid,1,1,$6::uuid,'ready') RETURNING id::text`,
		f.TenantID, batchID, captureFileID, top.submission, top.page, sourceAsset.ID).Scan(&capturePageID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO page_registration_run
(tenant_id,capture_page_id,submission_page_id,source_file_asset_id,source_sha256,template_id,template_content_hash,page_no,
 processing_status,match_status,profile_version,registered_file_asset_id,completed_at)
VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5,$6::uuid,$7,1,'completed','matched','synthetic-v1',$4::uuid,now())`,
		f.TenantID, capturePageID, top.page, sourceAsset.ID, sourceHash, templateID, templateHash); err != nil {
		t.Fatal(err)
	}
	return sourceAsset.ID, imageData
}

type anonymousSourceIdentity struct {
	Fixture                                 story056AcceptanceFixture
	Top, Other                              repairAnswer
	QuestionID, SourceAssetID, StudentToken string
}

// 普通页面仍只解析本人答案；匿名分享不能借用原文件下载路由越过同学间隔离。
func e2eAssertAnonymousSourceAuthorization(t *testing.T, ctx context.Context, db *sql.DB, router http.Handler, store *scorerelease.PostgresStore, identity anonymousSourceIdentity) {
	t.Helper()
	f, top, other, questionID, studentToken := identity.Fixture, identity.Top, identity.Other, identity.QuestionID, identity.StudentToken

	// 普通页面路由仍解析请求学生自己的分段；公开路径不能指定高分学生的提交。
	own, err := store.StudentPaperPageImage(ctx, f.TenantID, f.ExamID, other.student, questionID, false)
	if err != nil || own.AnswerSegmentID == "" {
		t.Fatalf("own page authorization: %+v %v", own, err)
	}
	var topSegment string
	if err := db.QueryRowContext(ctx, `SELECT id::text FROM answer_segment WHERE tenant_id=$1::uuid AND submission_id=$2::uuid AND question_id=$3::uuid LIMIT 1`, f.TenantID, top.submission, questionID).Scan(&topSegment); err != nil {
		t.Fatal(err)
	}
	if own.AnswerSegmentID == topSegment {
		t.Fatal("ordinary page route resolved a peer's source segment")
	}
	fileRequest := httptest.NewRequest(http.MethodGet, "/api/v1/files/"+identity.SourceAssetID+"/download", nil)
	fileRequest.Header.Set("Authorization", "Bearer "+studentToken)
	fileResponse := httptest.NewRecorder()
	router.ServeHTTP(fileResponse, fileRequest)
	if fileResponse.Code != http.StatusForbidden {
		t.Fatalf("student fetched peer source asset: status=%d", fileResponse.Code)
	}
}

// 把彩色身份笔画、棋盘码和 PNG 尾随秘密都放进合成来源，供后续脱敏验证。
func e2eAnonymousIdentityImage(t *testing.T, answerRed uint8) ([]byte, *image.RGBA) {
	t.Helper()
	// 配准图像已与模板对齐。彩色合成身份笔画和类似二维码的棋盘图案都限制在身份区域内。
	imageData := image.NewRGBA(image.Rect(0, 0, 100, 140))
	for y := 0; y < 140; y++ {
		for x := 0; x < 100; x++ {
			imageData.Set(x, y, color.RGBA{R: answerRed, G: 140, B: 210, A: 255})
		}
	}
	for y := 3; y < 24; y++ {
		for x := 3; x < 48; x++ {
			if (x/3+y/3)%2 == 0 {
				imageData.Set(x, y, color.Black)
			} else {
				imageData.Set(x, y, color.RGBA{R: 240, G: 20, B: 20, A: 255})
			}
		}
	}
	var source bytes.Buffer
	if err := png.Encode(&source, imageData); err != nil {
		t.Fatal(err)
	}
	source.WriteString("SYNTHETIC-NAME-STUDENT-NUMBER-QR-SECRET")
	sourceBytes := source.Bytes()
	return sourceBytes, imageData
}

// 检查身份区域三个像素与尾随秘密被去除，同时保留区域外的一处答题像素。
func e2eAssertAnonymousIdentityMask(t *testing.T, response *httptest.ResponseRecorder, imageData *image.RGBA) {
	t.Helper()
	if response.Code != http.StatusOK || response.Header().Get("Content-Type") != "image/png" || bytes.Contains(response.Body.Bytes(), []byte("QR-SECRET")) {
		t.Fatalf("anonymous sharing response: status=%d content_type=%q", response.Code, response.Header().Get("Content-Type"))
	}
	shared, err := png.Decode(bytes.NewReader(response.Body.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	for _, point := range []image.Point{{5, 5}, {25, 12}, {45, 20}} {
		r, g, b, a := shared.At(point.X, point.Y).RGBA()
		if r != 0xffff || g != 0xffff || b != 0xffff || a != 0xffff {
			t.Fatalf("unmasked identity pixel at %v", point)
		}
	}
	if shared.At(80, 100) != imageData.At(80, 100) {
		t.Fatal("answer region changed")
	}
}
