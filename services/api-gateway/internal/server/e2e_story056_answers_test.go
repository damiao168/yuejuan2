package server

import (
	"context"
	"database/sql"
	"edugrade-enterprise/services/api-gateway/internal/files"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

func e2eSeedStory056AcceptanceAnswers(t *testing.T, db *sql.DB, fixture story056AcceptanceFixture, suffix string) {
	e2eSeedStory056AcceptanceAnswersCount(t, db, fixture, suffix, story056AcceptanceAnswerCount)
}

// e2eSeedStory056AcceptanceAnswersCount 使 PostgreSQL 流程测试保持聚焦：
// 广泛恢复测试可预置完整验收规模，事务边界测试则可明确断言单个答案。
func e2eSeedStory056AcceptanceAnswersCount(t *testing.T, db *sql.DB, fixture story056AcceptanceFixture, suffix string, count int) {
	e2eSeedStory056AcceptanceAnswersWithQuestionTypes(t, db, fixture, suffix, count, []string{"single_choice", "true_false", "multiple_choice"})
}

func e2eSeedStory056AcceptanceAnswersWithQuestionTypes(t *testing.T, db *sql.DB, fixture story056AcceptanceFixture, suffix string, count int, questionTypes []string) {
	t.Helper()
	if count < 1 || len(questionTypes) == 0 {
		t.Fatalf("STORY-056 acceptance answer count must be positive, got %d", count)
	}
	if count > len(fixture.StudentIDs) {
		t.Fatalf("answer count %d exceeds the %d candidates prepared before confirmation", count, len(fixture.StudentIDs))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	fileStore := files.NewPostgresStore(db)
	for index := 0; index < count; index++ {
		questionType := questionTypes[index%len(questionTypes)]
		if _, ok := fixture.QuestionIDs[questionType]; !ok {
			t.Fatalf("STORY-056 fixture has no %q question", questionType)
		}
		studentID := fixture.StudentIDs[index]
		var submissionID string
		if err := db.QueryRowContext(ctx, `
INSERT INTO submission (
  tenant_id, exam_id, student_id, candidate_no, source_type, status,
  expected_page_count, actual_page_count, quality_status, quality_issues, collected_by,
  identity_status, identity_evidence
)
VALUES ($1::uuid, $2::uuid, $3::uuid, $4, 'scanner_upload', 'ready_for_ocr', 1, 1, 'passed', '[]'::jsonb, $5::uuid, 'matched', '{}'::jsonb)
RETURNING id::text
`, fixture.TenantID, fixture.ExamID, studentID, fmt.Sprintf("C056-%03d", index+1), fixture.AdminID).Scan(&submissionID); err != nil {
			t.Fatalf("seed acceptance submission %d: %v", index+1, err)
		}
		assetToken := fmt.Sprintf("%s-%03d", strings.ReplaceAll(suffix, ".", ""), index+1)
		cropHash := e2eStory056AssetHash("crop", fixture.ExamID, suffix, fmt.Sprintf("%d", index+1))
		crop, err := fileStore.Create(ctx, files.CreateAssetInput{
			TenantID: fixture.TenantID, ExamID: fixture.ExamID, SubmissionID: submissionID, OwnerType: "answer_segment_crop",
			OriginalName: fmt.Sprintf("story056-crop-%s.png", assetToken), ContentType: "image/png", SizeBytes: 1,
			HashSHA256: cropHash, StorageBucket: "edugrade-story056-e2e", StorageKey: fmt.Sprintf("crops/%s.png", assetToken),
			Visibility: "private", UploadedBy: fixture.AdminID,
		})
		if err != nil {
			t.Fatalf("seed acceptance crop %d: %v", index+1, err)
		}
		var pageID string
		if err := db.QueryRowContext(ctx, `
INSERT INTO submission_page (tenant_id, submission_id, file_asset_id, page_no, status, quality_status)
VALUES ($1::uuid, $2::uuid, $3::uuid, 1, 'accepted', 'passed')
RETURNING id::text
`, fixture.TenantID, submissionID, crop.ID).Scan(&pageID); err != nil {
			t.Fatalf("seed acceptance page %d: %v", index+1, err)
		}
		if _, err := db.ExecContext(ctx, `
INSERT INTO answer_segment (
  tenant_id, submission_id, submission_page_id, question_id, question_no, bbox, source, status,
  template_id, template_content_hash, normalized_bbox, pixel_bbox, crop_file_asset_id, crop_sha256,
  question_version, processing_status, confidence
)
VALUES (
  $1::uuid, $2::uuid, $3::uuid, $4::uuid, $5, '{"x":0.1,"y":0.1,"width":0.7,"height":0.16}'::jsonb, 'configured_answer_area', 'accepted',
  $6::uuid, $7, '{"x":0.1,"y":0.1,"width":0.7,"height":0.16}'::jsonb, '{"x":100,"y":100,"width":700,"height":224}'::jsonb, $8::uuid, $9,
  1, 'completed', 0.99
)
`, fixture.TenantID, submissionID, pageID, fixture.QuestionIDs[questionType], map[string]string{"single_choice": "Q1", "true_false": "Q2", "multiple_choice": "Q3"}[questionType], fixture.TemplateID, fixture.TemplateContentHash, crop.ID, crop.HashSHA256); err != nil {
			t.Fatalf("seed acceptance answer segment %d: %v", index+1, err)
		}
	}
}

func e2eStartStory056Run(t *testing.T, router http.Handler, adminToken, examID, idempotencyKey string) map[string]any {
	t.Helper()
	return e2ePostJSON(t, router, http.MethodPost, "/api/v1/exams/"+examID+"/scoring-runs", adminToken, story056JSON(t, map[string]any{
		"idempotency_key": idempotencyKey,
	}), http.StatusCreated)["scoring_run"].(map[string]any)
}

func e2eStory062ScoringReadiness(t *testing.T, router http.Handler, adminToken, examID string) map[string]any {
	t.Helper()
	response := e2eGetJSON(t, router, "/api/v1/exams/"+examID+"/scoring-readiness", adminToken, http.StatusOK)
	readiness, ok := response["scoring_readiness"].(map[string]any)
	if !ok {
		t.Fatalf("scoring readiness response has unexpected shape: %#v", response)
	}
	return readiness
}

func e2eAssertStory062ScoringReadiness(
	t *testing.T,
	readiness map[string]any,
	wantReady bool,
	wantQuestions, wantSegments, wantAutomatic, wantManual int,
) {
	t.Helper()
	if got, ok := readiness["ready"].(bool); !ok || got != wantReady {
		t.Fatalf("scoring readiness ready=%v, want %v: %#v", readiness["ready"], wantReady, readiness)
	}
	counts := map[string]int{
		"total_questions":          wantQuestions,
		"total_segments":           wantSegments,
		"automatic_candidates":     wantAutomatic,
		"manual_review_candidates": wantManual,
	}
	for field, want := range counts {
		if got, ok := readiness[field].(float64); !ok || int(got) != want {
			t.Fatalf("scoring readiness %s=%v, want %d: %#v", field, readiness[field], want, readiness)
		}
	}
}

func e2eAssertStory062ReadinessCheck(t *testing.T, readiness map[string]any, code string, wantPassed bool, wantSeverity string) {
	t.Helper()
	checks, ok := readiness["checks"].([]any)
	if !ok {
		t.Fatalf("scoring readiness checks have unexpected shape: %#v", readiness)
	}
	for _, raw := range checks {
		check, ok := raw.(map[string]any)
		if !ok || check["code"] != code {
			continue
		}
		if got, ok := check["passed"].(bool); !ok || got != wantPassed {
			t.Fatalf("scoring readiness check %s passed=%v, want %v: %#v", code, check["passed"], wantPassed, check)
		}
		if check["severity"] != wantSeverity {
			t.Fatalf("scoring readiness check %s severity=%v, want %s: %#v", code, check["severity"], wantSeverity, check)
		}
		return
	}
	t.Fatalf("scoring readiness check %s is missing: %#v", code, readiness)
}

func e2eAssertStory062ReadinessError(t *testing.T, response map[string]any, blockerCode string) {
	t.Helper()
	apiError, ok := response["error"].(map[string]any)
	if !ok || apiError["code"] != "scoring_not_ready" {
		t.Fatalf("blocked scoring start must return scoring_not_ready: %#v", response)
	}
	readiness, ok := response["scoring_readiness"].(map[string]any)
	if !ok {
		t.Fatalf("blocked scoring start must include readiness details: %#v", response)
	}
	if ready, ok := readiness["ready"].(bool); !ok || ready {
		t.Fatalf("blocked scoring start must report ready=false: %#v", response)
	}
	e2eAssertStory062ReadinessCheck(t, readiness, blockerCode, false, "blocker")
}
