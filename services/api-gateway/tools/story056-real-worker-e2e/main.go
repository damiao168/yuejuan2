// Command story056-real-worker-e2e seeds and verifies the smallest useful OMR
// page-processing worker acceptance path for STORY-056. It is intentionally a
// test-only harness: it talks to the public API and uses a disposable
// PostgreSQL database to fabricate a completed upstream capture/registration
// boundary. It deliberately does not accept the capture or registration worker
// pipelines, and never addresses a developer or production Compose project.
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

const (
	defaultAPIBaseURL = "http://api-gateway:8080"
	defaultRunKey     = "story056-real-worker-e2e-v1"
)

type settings struct {
	apiBaseURL       string
	postgresDSN      string
	adminPassword    string
	workerUser       string
	workerPass       string
	unprivilegedUser string
	unprivilegedPass string
	runKey           string
}

type registrationFixture struct {
	submissionID      string
	submissionPageID  string
	registrationRunID string
}

type apiClient struct {
	baseURL       string
	token         string
	loginResponse map[string]any
	client        *http.Client
}

// STORY-056 E2E 使用临时数据库和真实 Worker 验证租约、证据文件及权限边界，失败时保留诊断状态。
func main() {
	phase := flag.String("phase", "", "acceptance phase: seed or verify")
	flag.Parse()

	if *phase != "seed" && *phase != "verify" {
		fatalf("--phase must be seed or verify")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	cfg, err := loadSettings()
	if err != nil {
		fatalf("load STORY-056 real-worker E2E settings: %v", err)
	}
	client := &apiClient{
		baseURL: cfg.apiBaseURL,
		client:  &http.Client{Timeout: 30 * time.Second},
	}
	if err := client.login(ctx, "platform", "platform_admin", cfg.adminPassword); err != nil {
		fatalf("authenticate isolated E2E platform admin: %v", err)
	}

	switch *phase {
	case "seed":
		if err := seed(ctx, cfg, client); err != nil {
			fatalf("seed STORY-056 real-worker fixture: %v", err)
		}
		fmt.Printf("{\"phase\":\"seed\",\"result\":\"ready\",\"scope\":\"omr-worker-only\",\"upstream_registration\":\"direct-sql-fixture\",\"run_key\":%q}\n", cfg.runKey)
	case "verify":
		if err := verify(ctx, cfg, client); err != nil {
			fatalf("verify STORY-056 real-worker fixture: %v", err)
		}
		fmt.Printf("{\"phase\":\"verify\",\"result\":\"passed\",\"scope\":\"omr-worker-only\",\"upstream_registration\":\"direct-sql-fixture\",\"run_key\":%q}\n", cfg.runKey)
	}
}

func loadSettings() (settings, error) {
	cfg := settings{
		apiBaseURL:       strings.TrimRight(envOr("EDUGRADE_E2E_API_BASE_URL", defaultAPIBaseURL), "/"),
		postgresDSN:      strings.TrimSpace(os.Getenv("EDUGRADE_E2E_POSTGRES_DSN")),
		adminPassword:    os.Getenv("EDUGRADE_E2E_ADMIN_PASSWORD"),
		workerUser:       envOr("EDUGRADE_E2E_WORKER_USERNAME", "story056_e2e_worker"),
		workerPass:       os.Getenv("EDUGRADE_E2E_WORKER_PASSWORD"),
		unprivilegedUser: envOr("EDUGRADE_E2E_UNPRIVILEGED_USERNAME", "story056_e2e_unprivileged"),
		unprivilegedPass: os.Getenv("EDUGRADE_E2E_UNPRIVILEGED_PASSWORD"),
		runKey:           envOr("EDUGRADE_E2E_RUN_KEY", defaultRunKey),
	}
	if cfg.postgresDSN == "" {
		return settings{}, errors.New("EDUGRADE_E2E_POSTGRES_DSN is required")
	}
	if cfg.adminPassword == "" || cfg.workerPass == "" || cfg.unprivilegedPass == "" {
		return settings{}, errors.New("EDUGRADE_E2E_ADMIN_PASSWORD, EDUGRADE_E2E_WORKER_PASSWORD, and EDUGRADE_E2E_UNPRIVILEGED_PASSWORD are required")
	}
	if len(cfg.runKey) > 160 {
		return settings{}, errors.New("EDUGRADE_E2E_RUN_KEY must be at most 160 characters")
	}
	return cfg, nil
}

func seed(ctx context.Context, cfg settings, client *apiClient) error {
	admin := client.currentUser()
	tenantID := stringField(admin, "tenant_id")
	adminID := stringField(admin, "id")
	if tenantID == "" || adminID == "" {
		return errors.New("platform admin login response lacks tenant or user id")
	}

	if _, err := client.json(ctx, http.MethodPost, "/api/v1/users", map[string]any{
		"username":     cfg.workerUser,
		"display_name": "STORY-056 isolated page-processing worker",
		"password":     cfg.workerPass,
		"role_code":    "page_processing_worker",
	}, http.StatusCreated); err != nil {
		return fmt.Errorf("create test-only page-processing user: %w", err)
	}
	if _, err := client.json(ctx, http.MethodPost, "/api/v1/users", map[string]any{
		"username":     cfg.unprivilegedUser,
		"display_name": "STORY-056 isolated unprivileged user",
		"password":     cfg.unprivilegedPass,
		"role_code":    "student",
	}, http.StatusCreated); err != nil {
		return fmt.Errorf("create test-only unprivileged user: %w", err)
	}

	school, err := client.json(ctx, http.MethodPost, "/api/v1/schools", map[string]any{
		"name": "STORY-056 Real Worker E2E School",
		"code": "s056-real-worker-e2e",
	}, http.StatusCreated)
	if err != nil {
		return fmt.Errorf("create E2E school: %w", err)
	}
	schoolID := nestedStringField(school, "school", "id")
	grade, err := client.json(ctx, http.MethodPost, "/api/v1/grades", map[string]any{
		"school_id": schoolID, "name": "STORY-056 E2E Grade", "level_no": 10, "academic_year": "2026",
	}, http.StatusCreated)
	if err != nil {
		return fmt.Errorf("create E2E grade: %w", err)
	}
	class, err := client.json(ctx, http.MethodPost, "/api/v1/classes", map[string]any{
		"school_id": schoolID, "grade_id": nestedStringField(grade, "grade", "id"),
		"name": "STORY-056 E2E Class", "code": "s056-e2e",
	}, http.StatusCreated)
	if err != nil {
		return fmt.Errorf("create E2E class: %w", err)
	}
	classID := nestedStringField(class, "class", "id")
	student, err := client.json(ctx, http.MethodPost, "/api/v1/students", map[string]any{
		"school_id": schoolID, "class_id": classID, "student_no": "S056-E2E-001", "name": "STORY-056 E2E Student",
	}, http.StatusCreated)
	if err != nil {
		return fmt.Errorf("create E2E student: %w", err)
	}
	studentID := nestedStringField(student, "student", "id")

	exam, err := client.json(ctx, http.MethodPost, "/api/v1/exams", map[string]any{
		"school_id": schoolID, "name": "STORY-056 real Worker acceptance", "subject": "Science",
		"exam_type": "unit_test", "total_score": 1, "grading_mode": "auto_objective_only",
		"appeal_enabled": true, "publish_policy": "manual_after_confirmation", "class_ids": []string{classID},
	}, http.StatusCreated)
	if err != nil {
		return fmt.Errorf("create E2E exam: %w", err)
	}
	examID := nestedStringField(exam, "exam", "id")

	templateImage, answerImage, err := syntheticOMRImages()
	if err != nil {
		return fmt.Errorf("build synthetic OMR image: %w", err)
	}
	paperAsset, err := client.uploadPNG(ctx, "story056-template.png", templateImage, map[string]string{
		"owner_type": "exam", "owner_id": examID, "exam_id": examID,
	})
	if err != nil {
		return fmt.Errorf("upload real template asset: %w", err)
	}
	paper, err := client.json(ctx, http.MethodPost, "/api/v1/exams/"+examID+"/papers", map[string]any{
		"file_asset_id": stringField(paperAsset, "id"),
	}, http.StatusCreated)
	if err != nil {
		return fmt.Errorf("create E2E paper: %w", err)
	}
	paperID := nestedStringField(paper, "paper", "id")
	question, err := client.json(ctx, http.MethodPost, "/api/v1/exams/"+examID+"/questions", map[string]any{
		"exam_paper_id": paperID, "question_no": "Q1", "question_type": "single_choice", "score": 1,
		"stem": "STORY-056 real worker fixture", "knowledge_points": []string{"story056"}, "answer_area": map[string]any{}, "sort_order": 1,
		"answer_key": map[string]any{"standard_answer": "B", "equivalent_answers": []any{}, "tolerance": map[string]any{}},
	}, http.StatusCreated)
	if err != nil {
		return fmt.Errorf("create objective question: %w", err)
	}
	questionID := nestedStringField(question, "question", "id")
	rule, err := client.json(ctx, http.MethodPost, "/api/v1/questions/"+questionID+"/scoring-rules", map[string]any{
		"rule_type": "single_choice", "config": map[string]any{},
	}, http.StatusCreated)
	if err != nil {
		return fmt.Errorf("create scoring rule: %w", err)
	}
	if _, err = client.json(ctx, http.MethodPost, "/api/v1/scoring-rules/"+nestedStringField(rule, "scoring_rule", "id")+"/publish", map[string]any{}, http.StatusOK); err != nil {
		return fmt.Errorf("publish scoring rule: %w", err)
	}

	layout := map[string]any{
		"omr_profile": map[string]any{
			"mode": "template_difference", "version": "opencv-template-difference-bubble-v1",
		},
		"pages": []any{
			map[string]any{
				"page_no": 1, "width": 160, "height": 70,
				"registration_marks": []any{}, "identity_regions": []any{},
				"question_regions": []any{
					map[string]any{
						"question_id": questionID, "label": "Q1", "x": 0.0, "y": 0.0, "width": 1.0, "height": 1.0,
						"option_regions": []any{
							map[string]any{"label": "A", "x": 0.125, "y": 0.2857142857, "width": 0.175, "height": 0.4},
							map[string]any{"label": "B", "x": 0.4375, "y": 0.2857142857, "width": 0.175, "height": 0.4},
							map[string]any{"label": "C", "x": 0.75, "y": 0.2857142857, "width": 0.175, "height": 0.4},
						},
					},
				},
			},
		},
	}
	template, err := client.json(ctx, http.MethodPost, "/api/v1/exams/"+examID+"/answer-sheet-templates", map[string]any{
		"exam_paper_id": paperID,
		"name":          "STORY-056 real-worker OMR template",
		"page_count":    1,
		"layout":        layout,
	}, http.StatusCreated)
	if err != nil {
		return fmt.Errorf("create answer-sheet template: %w", err)
	}
	templateID := nestedStringField(template, "template", "id")
	locked, err := client.json(ctx, http.MethodPost, "/api/v1/answer-sheet-templates/"+templateID+"/lock", map[string]any{}, http.StatusOK)
	if err != nil {
		return fmt.Errorf("lock answer-sheet template: %w", err)
	}
	templateHash := nestedStringField(locked, "template", "content_hash")
	if _, err = client.json(ctx, http.MethodPost, "/api/v1/exams/"+examID+"/readiness/confirm", map[string]any{}, http.StatusOK); err != nil {
		return fmt.Errorf("confirm E2E readiness: %w", err)
	}
	if _, err = client.json(ctx, http.MethodPost, "/api/v1/exams/"+examID+"/start-collection", map[string]any{}, http.StatusOK); err != nil {
		return fmt.Errorf("start E2E collection: %w", err)
	}

	sourceAsset, err := client.uploadPNG(ctx, "story056-source-answer.png", answerImage, map[string]string{
		"owner_type": "generic", "exam_id": examID,
	})
	if err != nil {
		return fmt.Errorf("upload real answer source asset: %w", err)
	}
	registration, err := seedRegistrationFixture(ctx, cfg.postgresDSN, tenantID, adminID, examID, studentID, templateID, templateHash, sourceAsset)
	if err != nil {
		return err
	}
	registeredAsset, err := client.uploadPNG(ctx, "story056-registered-answer.png", answerImage, map[string]string{
		"owner_type": "page_registration_output", "owner_id": registration.registrationRunID, "exam_id": examID,
	})
	if err != nil {
		return fmt.Errorf("upload registration output evidence: %w", err)
	}
	cropAsset, err := client.uploadPNG(ctx, "story056-answer-segment.png", answerImage, map[string]string{
		"owner_type": "answer_segment_crop", "owner_id": registration.registrationRunID, "exam_id": examID,
	})
	if err != nil {
		return fmt.Errorf("upload registered answer crop evidence: %w", err)
	}
	segmentID, err := completeRegistrationAndSeedSegment(ctx, cfg.postgresDSN, tenantID, questionID, templateID, templateHash, registration, registeredAsset, cropAsset)
	if err != nil {
		return err
	}

	run, err := client.json(ctx, http.MethodPost, "/api/v1/exams/"+examID+"/scoring-runs", map[string]any{
		"idempotency_key": cfg.runKey,
	}, http.StatusCreated)
	if err != nil {
		return fmt.Errorf("queue real OMR task: %w", err)
	}
	runData := nestedMap(run, "scoring_run")
	if stringField(runData, "status") != "processing" || intField(runData, "total_count") != 1 || intField(runData, "queued_count") != 1 {
		return fmt.Errorf("unexpected seeded scoring run: %#v", runData)
	}
	if segmentID == "" {
		return errors.New("seeded answer segment id is empty")
	}
	return nil
}

func verify(ctx context.Context, cfg settings, client *apiClient) error {
	db, err := sql.Open("pgx", cfg.postgresDSN)
	if err != nil {
		return fmt.Errorf("open isolated PostgreSQL database: %w", err)
	}
	defer db.Close()

	var runID, omrRunID, runStatus, omrStatus, decision, runtimeStatus, overlayID, overlayOwnerType, overlayOwnerID, overlayHash, overlayVisibility, registrationID, registrationStatus, cropOwnerType, cropOwnerID, autoConfirmReason, profileVersion, referenceID, referenceSHA256, referenceFileSHA256, reviewReason string
	var selectedRaw []byte
	var autoConfirmed int
	var autoConfirmEligible bool
	var score float64
	err = db.QueryRowContext(ctx, `
SELECT r.id::text, r.status, r.auto_confirmed_count,
	       o.id::text, o.status, COALESCE(o.decision,''), o.selected_options,
	       COALESCE(o.overlay_file_asset_id::text,''), COALESCE(wt.status,''), o.auto_confirm_eligible,o.auto_confirm_reason,
	       o.profile_version, COALESCE(o.reference_file_asset_id::text,''), COALESCE(o.reference_sha256,''), reference.hash_sha256,
	       COALESCE(g.score::float8,-1), pr.id::text, pr.processing_status,
	       crop.owner_type, COALESCE(crop.owner_id::text,''),
	       overlay.owner_type, COALESCE(overlay.owner_id::text,''), overlay.hash_sha256, overlay.visibility,
	       COALESCE(rt.reason_code,'')
FROM scoring_run r
JOIN tenant t ON t.id=r.tenant_id
JOIN omr_run o ON o.tenant_id=r.tenant_id AND o.scoring_run_id=r.id AND o.deleted_at IS NULL
JOIN answer_segment seg ON seg.tenant_id=o.tenant_id AND seg.id=o.answer_segment_id AND seg.deleted_at IS NULL
	JOIN page_registration_run pr ON pr.tenant_id=seg.tenant_id AND pr.id=seg.registration_run_id AND pr.deleted_at IS NULL
	JOIN file_asset crop ON crop.tenant_id=seg.tenant_id AND crop.id=seg.crop_file_asset_id AND crop.deleted_at IS NULL
	JOIN file_asset reference ON reference.tenant_id=o.tenant_id AND reference.id=o.reference_file_asset_id AND reference.deleted_at IS NULL
	JOIN file_asset overlay ON overlay.tenant_id=o.tenant_id AND overlay.id=o.overlay_file_asset_id AND overlay.deleted_at IS NULL
	LEFT JOIN agent_worker_task wt ON wt.tenant_id=o.tenant_id AND wt.id=o.runtime_task_id
LEFT JOIN question_grade g ON g.tenant_id=o.tenant_id AND g.scoring_run_id=r.id
  AND g.answer_segment_id=o.answer_segment_id AND g.is_current AND g.deleted_at IS NULL
LEFT JOIN review_task rt ON rt.tenant_id=o.tenant_id AND rt.scoring_run_id=r.id AND rt.answer_segment_id=o.answer_segment_id
  AND rt.source='omr_ambiguous' AND rt.status IN ('pending','assigned','in_progress','returned') AND rt.deleted_at IS NULL
WHERE t.code='platform' AND r.idempotency_key=$1 AND r.deleted_at IS NULL
	`, cfg.runKey).Scan(&runID, &runStatus, &autoConfirmed, &omrRunID, &omrStatus, &decision, &selectedRaw, &overlayID, &runtimeStatus, &autoConfirmEligible, &autoConfirmReason, &profileVersion, &referenceID, &referenceSHA256, &referenceFileSHA256, &score, &registrationID, &registrationStatus, &cropOwnerType, &cropOwnerID, &overlayOwnerType, &overlayOwnerID, &overlayHash, &overlayVisibility, &reviewReason)
	if err != nil {
		return fmt.Errorf("read real worker result: %w", err)
	}
	var selected []string
	if err = json.Unmarshal(selectedRaw, &selected); err != nil {
		return fmt.Errorf("decode OMR selected options: %w", err)
	}
	if runStatus != "needs_review" || omrStatus != "completed" || runtimeStatus != "succeeded" || decision != "selected" || autoConfirmed != 0 || autoConfirmEligible || autoConfirmReason != "template_difference_calibration_unapproved" || profileVersion != "opencv-template-difference-bubble-v1" || referenceID == "" || referenceSHA256 == "" || !strings.EqualFold(referenceSHA256, referenceFileSHA256) || score != -1 || reviewReason != "omr_template_difference_calibration_unapproved" || len(selected) != 1 || selected[0] != "B" || registrationStatus != "completed" || cropOwnerType != "answer_segment_crop" || cropOwnerID != registrationID || overlayOwnerType != "omr_evidence" || overlayOwnerID != omrRunID || overlayHash == "" || overlayVisibility != "private" {
		return fmt.Errorf("unexpected real worker state run=%s status=%s omr=%s task=%s decision=%s selected=%v auto_confirmed=%d eligible=%t reason=%s profile=%s reference=%s/%s review=%s score=%g", runID, runStatus, omrStatus, runtimeStatus, decision, selected, autoConfirmed, autoConfirmEligible, autoConfirmReason, profileVersion, referenceID, referenceSHA256, reviewReason, score)
	}
	if overlayID == "" {
		return errors.New("real worker completed without a private overlay asset")
	}
	overlay, err := client.bytes(ctx, "/api/v1/files/"+overlayID+"/download")
	if err != nil {
		return fmt.Errorf("download private overlay evidence: %w", err)
	}
	if len(overlay) < 8 || !bytes.Equal(overlay[:8], []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}) {
		return errors.New("private overlay evidence is not a PNG")
	}
	actualOverlayHash := fmt.Sprintf("%x", sha256.Sum256(overlay))
	if !strings.EqualFold(actualOverlayHash, overlayHash) {
		return fmt.Errorf("private overlay evidence hash differs from file asset: got %s want %s", actualOverlayHash, overlayHash)
	}
	unprivileged := &apiClient{baseURL: cfg.apiBaseURL, client: &http.Client{Timeout: 30 * time.Second}}
	if err := unprivileged.login(ctx, "platform", cfg.unprivilegedUser, cfg.unprivilegedPass); err != nil {
		return fmt.Errorf("authenticate isolated unprivileged user: %w", err)
	}
	if err := unprivileged.expectStatus(ctx, http.MethodGet, "/api/v1/files/"+overlayID+"/download", http.StatusForbidden); err != nil {
		return fmt.Errorf("unprivileged user must not download private overlay: %w", err)
	}
	return nil
}

func seedRegistrationFixture(ctx context.Context, dsn, tenantID, adminID, examID, studentID, templateID, templateHash string, source map[string]any) (registrationFixture, error) {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return registrationFixture{}, fmt.Errorf("open isolated PostgreSQL database: %w", err)
	}
	defer db.Close()

	sourceID := stringField(source, "id")
	sourceHash := stringField(source, "hash_sha256")
	sourceBytes := int64(intField(source, "size_bytes"))
	if sourceID == "" || sourceHash == "" || sourceBytes <= 0 {
		return registrationFixture{}, errors.New("source asset lacks id, hash, or size")
	}
	var submissionID string
	err = db.QueryRowContext(ctx, `
INSERT INTO submission (
  tenant_id, exam_id, student_id, candidate_no, source_type, status,
  expected_page_count, actual_page_count, quality_status, quality_issues, collected_by,
  identity_status, identity_evidence
)
VALUES ($1::uuid,$2::uuid,$3::uuid,'S056-E2E-001','scanner_upload','ready_for_ocr',1,1,'passed','[]'::jsonb,$4::uuid,'matched','{}'::jsonb)
RETURNING id::text
`, tenantID, examID, studentID, adminID).Scan(&submissionID)
	if err != nil {
		return registrationFixture{}, fmt.Errorf("seed isolated submission: %w", err)
	}
	var pageID string
	err = db.QueryRowContext(ctx, `
INSERT INTO submission_page (tenant_id,submission_id,file_asset_id,page_no,status,quality_status)
VALUES ($1::uuid,$2::uuid,$3::uuid,1,'accepted','passed')
RETURNING id::text
`, tenantID, submissionID, sourceID).Scan(&pageID)
	if err != nil {
		return registrationFixture{}, fmt.Errorf("seed isolated submission page: %w", err)
	}
	var batchID string
	err = db.QueryRowContext(ctx, `
INSERT INTO capture_batch (tenant_id,exam_id,name,source_type,status,operator_id)
VALUES ($1::uuid,$2::uuid,'STORY-056 real-worker fixture','scanner_upload','ready',$3::uuid)
RETURNING id::text
`, tenantID, examID, adminID).Scan(&batchID)
	if err != nil {
		return registrationFixture{}, fmt.Errorf("seed isolated capture batch: %w", err)
	}
	var captureFileID string
	err = db.QueryRowContext(ctx, `
INSERT INTO capture_file (tenant_id,capture_batch_id,file_asset_id,original_name,content_type,sha256,byte_size,status,idempotency_key,uploaded_by)
VALUES ($1::uuid,$2::uuid,$3::uuid,'story056-source-answer.png','image/png',$4,$5,'completed','story056-real-worker-source',$6::uuid)
RETURNING id::text
`, tenantID, batchID, sourceID, sourceHash, sourceBytes, adminID).Scan(&captureFileID)
	if err != nil {
		return registrationFixture{}, fmt.Errorf("seed isolated capture file: %w", err)
	}
	var capturePageID string
	err = db.QueryRowContext(ctx, `
INSERT INTO capture_page (
  tenant_id,capture_batch_id,capture_file_id,source_index,submission_id,submission_page_id,
  assigned_page_no,sequence_no,decoded_file_asset_id,status,page_identity,match_candidates
)
VALUES ($1::uuid,$2::uuid,$3::uuid,1,$4::uuid,$5::uuid,1,1,$6::uuid,'ready','{}'::jsonb,'[]'::jsonb)
RETURNING id::text
`, tenantID, batchID, captureFileID, submissionID, pageID, sourceID).Scan(&capturePageID)
	if err != nil {
		return registrationFixture{}, fmt.Errorf("seed isolated capture page: %w", err)
	}
	var registrationRunID string
	err = db.QueryRowContext(ctx, `
INSERT INTO page_registration_run (
  tenant_id,capture_page_id,submission_page_id,source_file_asset_id,source_sha256,
  template_id,template_content_hash,page_no,processing_status,profile_version,started_at
)
VALUES ($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5,$6::uuid,$7,1,'processing','fixture-identity-v1',now())
RETURNING id::text
`, tenantID, capturePageID, pageID, sourceID, sourceHash, templateID, templateHash).Scan(&registrationRunID)
	if err != nil {
		return registrationFixture{}, fmt.Errorf("seed isolated registration run: %w", err)
	}
	return registrationFixture{submissionID: submissionID, submissionPageID: pageID, registrationRunID: registrationRunID}, nil
}

func completeRegistrationAndSeedSegment(ctx context.Context, dsn, tenantID, questionID, templateID, templateHash string, registration registrationFixture, registered, crop map[string]any) (string, error) {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return "", fmt.Errorf("open isolated PostgreSQL database: %w", err)
	}
	defer db.Close()

	registeredID := stringField(registered, "id")
	cropID := stringField(crop, "id")
	cropHash := stringField(crop, "hash_sha256")
	if registeredID == "" || cropID == "" || cropHash == "" {
		return "", errors.New("registration assets lack required identity or hash")
	}
	result, err := db.ExecContext(ctx, `
UPDATE page_registration_run
SET processing_status='completed',match_status='matched',confidence=1.0,method='fixture_identity',
    registered_file_asset_id=$3::uuid,result_version='fixture-registration-v1',duration_ms=1,completed_at=now(),updated_at=now()
WHERE tenant_id=$1::uuid AND id=$2::uuid AND processing_status='processing'
`, tenantID, registration.registrationRunID, registeredID)
	if err != nil {
		return "", fmt.Errorf("complete isolated registration run: %w", err)
	}
	updated, err := result.RowsAffected()
	if err != nil || updated != 1 {
		return "", fmt.Errorf("complete isolated registration run affected %d rows", updated)
	}
	var segmentID string
	err = db.QueryRowContext(ctx, `
INSERT INTO answer_segment (
  tenant_id,submission_id,submission_page_id,question_id,question_no,bbox,source,status,
  template_id,template_content_hash,registration_run_id,normalized_bbox,pixel_bbox,crop_file_asset_id,crop_sha256,
  question_version,processing_status,confidence
)
VALUES (
  $1::uuid,$2::uuid,$3::uuid,$4::uuid,'Q1','{"x":0,"y":0,"width":1,"height":1}'::jsonb,'configured_answer_area','accepted',
  $5::uuid,$6,$7::uuid,'{"x":0,"y":0,"width":1,"height":1}'::jsonb,'{"x":0,"y":0,"width":160,"height":70}'::jsonb,$8::uuid,$9,
  1,'completed',0.99
)
RETURNING id::text
`, tenantID, registration.submissionID, registration.submissionPageID, questionID, templateID, templateHash, registration.registrationRunID, cropID, cropHash).Scan(&segmentID)
	if err != nil {
		return "", fmt.Errorf("seed isolated answer segment: %w", err)
	}
	return segmentID, nil
}

func (c *apiClient) login(ctx context.Context, tenantCode, username, password string) error {
	response, err := c.json(ctx, http.MethodPost, "/api/v1/auth/token", map[string]any{
		"tenant_code": tenantCode, "username": username, "password": password,
		"client_type": "desktop", "device_name": "STORY-056 validation",
	}, http.StatusOK)
	if err != nil {
		return err
	}
	c.token = stringField(response, "access_token")
	if c.token == "" {
		return errors.New("login response did not include access_token")
	}
	return nil
}

func (c *apiClient) currentUser() map[string]any {
	return nestedMap(c.loginResponse, "user")
}

// loginResponse is retained only for immutable identity values needed to seed
// the isolated fixture. It never contains a password.
func (c *apiClient) json(ctx context.Context, method, path string, body any, want int) (map[string]any, error) {
	var payload io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		payload = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, payload)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	response, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, 2<<20))
	if err != nil {
		return nil, err
	}
	if response.StatusCode != want {
		return nil, fmt.Errorf("%s %s returned %d: %s", method, path, response.StatusCode, strings.TrimSpace(string(raw)))
	}
	result := map[string]any{}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &result); err != nil {
			return nil, fmt.Errorf("decode %s %s response: %w", method, path, err)
		}
	}
	if path == "/api/v1/auth/token" && method == http.MethodPost {
		c.loginResponse = result
	}
	return result, nil
}

func (c *apiClient) uploadPNG(ctx context.Context, filename string, content []byte, fields map[string]string) (map[string]any, error) {
	var buffer bytes.Buffer
	writer := multipart.NewWriter(&buffer)
	for key, value := range fields {
		if err := writer.WriteField(key, value); err != nil {
			return nil, err
		}
	}
	part, err := writer.CreateFormFile("file", filename)
	if err != nil {
		return nil, err
	}
	if _, err = part.Write(content); err != nil {
		return nil, err
	}
	if err = writer.Close(); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/v1/files", &buffer)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	response, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, 2<<20))
	if err != nil {
		return nil, err
	}
	if response.StatusCode != http.StatusCreated {
		return nil, fmt.Errorf("upload %s returned %d: %s", filename, response.StatusCode, strings.TrimSpace(string(raw)))
	}
	result := map[string]any{}
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, err
	}
	asset := nestedMap(result, "file")
	if stringField(asset, "id") == "" || stringField(asset, "hash_sha256") == "" {
		return nil, errors.New("upload response lacks file identity or hash")
	}
	return asset, nil
}

func (c *apiClient) bytes(ctx context.Context, path string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	response, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	content, err := io.ReadAll(io.LimitReader(response.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s returned %d", path, response.StatusCode)
	}
	return content, nil
}

func (c *apiClient) expectStatus(ctx context.Context, method, path string, want int) error {
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, nil)
	if err != nil {
		return err
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	response, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 64<<10))
	if err != nil {
		return err
	}
	if response.StatusCode != want {
		return fmt.Errorf("%s %s returned %d, want %d: %s", method, path, response.StatusCode, want, strings.TrimSpace(string(body)))
	}
	return nil
}

func syntheticOMRImages() ([]byte, []byte, error) {
	base := image.NewRGBA(image.Rect(0, 0, 160, 70))
	draw.Draw(base, base.Bounds(), &image.Uniform{C: color.White}, image.Point{}, draw.Src)
	for _, x := range []int{20, 70, 120} {
		drawOutline(base, image.Rect(x, 20, x+28, 48), color.Black, 2)
	}
	// This simulates printed form content inside option A. Raw fill-ratio OMR
	// would treat it as a second mark; the template-difference worker must
	// remove it using the frozen blank reference before selecting B.
	draw.Draw(base, image.Rect(27, 27, 41, 41), &image.Uniform{C: color.Black}, image.Point{}, draw.Src)
	template, err := encodePNG(base)
	if err != nil {
		return nil, nil, err
	}
	answer := image.NewRGBA(base.Bounds())
	draw.Draw(answer, answer.Bounds(), base, image.Point{}, draw.Src)
	fillEllipse(answer, 76, 26, 92, 42, color.Black)
	marked, err := encodePNG(answer)
	if err != nil {
		return nil, nil, err
	}
	return template, marked, nil
}

func drawOutline(dst draw.Image, rect image.Rectangle, colour color.Color, width int) {
	for offset := 0; offset < width; offset++ {
		for x := rect.Min.X + offset; x < rect.Max.X-offset; x++ {
			dst.Set(x, rect.Min.Y+offset, colour)
			dst.Set(x, rect.Max.Y-1-offset, colour)
		}
		for y := rect.Min.Y + offset; y < rect.Max.Y-offset; y++ {
			dst.Set(rect.Min.X+offset, y, colour)
			dst.Set(rect.Max.X-1-offset, y, colour)
		}
	}
}

func fillEllipse(dst draw.Image, minX, minY, maxX, maxY int, colour color.Color) {
	cx, cy := float64(minX+maxX-1)/2, float64(minY+maxY-1)/2
	rx, ry := float64(maxX-minX)/2, float64(maxY-minY)/2
	for y := minY; y < maxY; y++ {
		for x := minX; x < maxX; x++ {
			dx, dy := (float64(x)-cx)/rx, (float64(y)-cy)/ry
			if dx*dx+dy*dy <= 1 {
				dst.Set(x, y, colour)
			}
		}
	}
}

func encodePNG(img image.Image) ([]byte, error) {
	var output bytes.Buffer
	if err := png.Encode(&output, img); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}

func nestedMap(value map[string]any, key string) map[string]any {
	item, _ := value[key].(map[string]any)
	return item
}

func nestedStringField(value map[string]any, nested, key string) string {
	return stringField(nestedMap(value, nested), key)
}

func stringField(value map[string]any, key string) string {
	item, _ := value[key].(string)
	return item
}

func intField(value map[string]any, key string) int {
	item, _ := value[key].(float64)
	return int(item)
}

func envOr(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
