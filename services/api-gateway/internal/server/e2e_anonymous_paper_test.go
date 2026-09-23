package server

import (
	"bytes"
	"context"
	"crypto/sha256"
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
	if _, err := service.Publish(ctx, f.TenantID, release.ID, f.AdminID); err != scorerelease.ErrAnonymousPaperUnavailable {
		t.Fatalf("missing identity regions and registration must block publish: %v", err)
	}
	var highestSubmission string
	if err := db.QueryRowContext(ctx, `SELECT submission_id::text FROM score_release_item WHERE tenant_id=$1::uuid AND release_id=$2::uuid ORDER BY total_score DESC,submission_id LIMIT 1`, f.TenantID, release.ID).Scan(&highestSubmission); err != nil {
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
	// The registered image is template aligned. Colored synthetic identity
	// strokes and a QR-like checkerboard are confined to the identity region.
	imageData := image.NewRGBA(image.Rect(0, 0, 100, 140))
	for y := 0; y < 140; y++ {
		for x := 0; x < 100; x++ {
			imageData.Set(x, y, color.RGBA{R: 40, G: 140, B: 210, A: 255})
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
SELECT tenant_id,exam_id,exam_paper_id,version_no+1,'Synthetic anonymous layout','locked',page_count,
jsonb_set(layout,'{pages,0,identity_regions}','[{"id":"identity-name-number-qr","x":0,"y":0,"width":0.5,"height":0.2}]'::jsonb),
$2,created_by,$3::uuid,now() FROM answer_sheet_template WHERE tenant_id=$1::uuid AND id=$4::uuid
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
	if _, err := service.Publish(ctx, f.TenantID, release.ID, f.AdminID); err != nil {
		t.Fatalf("publish with verified anonymous page: %v", err)
	}
	studentView, err := store.StudentResult(ctx, f.TenantID, f.ExamID, other.student)
	if err != nil || studentView.HighScorePaper == nil || !studentView.HighScorePaper.Available ||
		len(studentView.HighScorePaper.Pages) != 1 || studentView.HighScorePaper.Pages[0].SubmissionPageID != "" {
		t.Fatalf("student result did not expose only anonymous page metadata: %+v %v", studentView.HighScorePaper, err)
	}
	questionID := f.QuestionIDs["single_choice"]
	e2eActivatePostgresDemoUsers(t, db, []string{"student"})
	studentUsername := "anonymous_other_" + uuid.NewString()
	e2eSeedPostgresStudentScopes(t, db, top.student, other.student, studentUsername)
	studentToken := e2eLoginWithTenant(t, router, "demo", studentUsername, "ChangeMe123!")
	request := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/student/exams/"+f.ExamID+"/questions/"+questionID+"/page-image?variant=high_score", nil)
		req.Header.Set("Authorization", "Bearer "+studentToken)
		res := httptest.NewRecorder()
		router.ServeHTTP(res, req)
		return res
	}
	response := request()
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
	// The ordinary page route still resolves the requesting student's own
	// segment; the public path cannot name the top student's submission.
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
	fileRequest := httptest.NewRequest(http.MethodGet, "/api/v1/files/"+sourceAsset.ID+"/download", nil)
	fileRequest.Header.Set("Authorization", "Bearer "+studentToken)
	fileResponse := httptest.NewRecorder()
	router.ServeHTTP(fileResponse, fileRequest)
	if fileResponse.Code != http.StatusForbidden {
		t.Fatalf("student fetched peer source asset: status=%d", fileResponse.Code)
	}
	if err := service.RevokeHighScorePaper(ctx, f.TenantID, release.ID, f.AdminID); err != nil {
		t.Fatal(err)
	}
	if response := request(); response.Code != http.StatusNotFound {
		t.Fatalf("revoked share status=%d, want 404", response.Code)
	}
	result, err := store.StudentResult(ctx, f.TenantID, f.ExamID, other.student)
	if err != nil || result.HighScorePaper != nil {
		t.Fatalf("revoked share still listed: %+v %v", result.HighScorePaper, err)
	}
}
