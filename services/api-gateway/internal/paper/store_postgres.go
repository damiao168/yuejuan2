package paper

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"edugrade-enterprise/services/api-gateway/internal/assessment"
	"github.com/google/uuid"
)

type PostgresStore struct {
	db *sql.DB
}

type postgresQueryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func NewPostgresStore(db *sql.DB) *PostgresStore {
	return &PostgresStore{db: db}
}

func (s *PostgresStore) CreatePaper(ctx context.Context, tenantID string, examID string, userID string, input CreatePaperInput) (Paper, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Paper{}, err
	}
	defer tx.Rollback()
	if err := ensureExamPaperMutableTx(ctx, tx, tenantID, examID); err != nil {
		return Paper{}, err
	}
	version := 1
	_ = tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(version_no), 0) + 1 FROM exam_paper WHERE tenant_id = $1 AND exam_id = $2`, tenantID, examID).Scan(&version)
	var paperID string
	if err := tx.QueryRowContext(ctx, `SELECT gen_random_uuid()::text`).Scan(&paperID); err != nil {
		return Paper{}, err
	}
	fileID := input.FileAssetID
	fileInput := input.File
	if fileID != "" {
		var linkedExamID string
		err := tx.QueryRowContext(ctx, `
SELECT id::text, COALESCE(exam_id::text, ''), original_name, content_type, size_bytes, hash_sha256, storage_bucket, storage_key
FROM file_asset
WHERE tenant_id = $1 AND id::text = $2 AND deleted_at IS NULL
`, tenantID, fileID).Scan(&fileID, &linkedExamID, &fileInput.OriginalName, &fileInput.ContentType, &fileInput.SizeBytes, &fileInput.HashSHA256, &fileInput.StorageBucket, &fileInput.StorageKey)
		if errors.Is(err, sql.ErrNoRows) {
			return Paper{}, ErrNotFound
		}
		if err != nil {
			return Paper{}, err
		}
		if linkedExamID != "" && linkedExamID != examID {
			return Paper{}, ErrInvalidInput
		}
		if _, err := tx.ExecContext(ctx, `
UPDATE file_asset
SET exam_id = $3, owner_type = 'exam_paper', owner_id = $4::uuid
WHERE tenant_id = $1 AND id::text = $2 AND deleted_at IS NULL
`, tenantID, fileID, examID, paperID); err != nil {
			return Paper{}, err
		}
	} else {
		if err := tx.QueryRowContext(ctx, `SELECT gen_random_uuid()::text`).Scan(&fileID); err != nil {
			return Paper{}, err
		}
		if _, err := tx.ExecContext(ctx, `
INSERT INTO file_asset (id, tenant_id, exam_id, owner_type, owner_id, original_name, content_type, size_bytes, hash_sha256, storage_bucket, storage_key, visibility, uploaded_by)
VALUES ($1, $2, $3, 'exam_paper', $4, $5, $6, $7, $8, $9, $10, 'private', $11)
`, fileID, tenantID, examID, paperID, fileInput.OriginalName, fileInput.ContentType, fileInput.SizeBytes, fileInput.HashSHA256, fileInput.StorageBucket, fileInput.StorageKey, userID); err != nil {
			return Paper{}, err
		}
	}
	row := tx.QueryRowContext(ctx, `
INSERT INTO exam_paper (id, tenant_id, exam_id, file_asset_id, version_no, status, uploaded_by)
VALUES ($1, $2, $3, $4, $5, 'uploaded', $6)
RETURNING id::text, tenant_id::text, exam_id::text, file_asset_id::text, version_no, status
`, paperID, tenantID, examID, fileID, version, userID)
	var out Paper
	if err := row.Scan(&out.ID, &out.TenantID, &out.ExamID, &out.FileAssetID, &out.VersionNo, &out.Status); err != nil {
		return Paper{}, err
	}
	out.File = fileInput
	if err := tx.Commit(); err != nil {
		return Paper{}, err
	}
	return out, nil
}

func (s *PostgresStore) ListPapers(ctx context.Context, tenantID string, examID string) ([]Paper, error) {
	return listPapers(ctx, s.db, tenantID, examID)
}

func listPapers(ctx context.Context, queryer postgresQueryer, tenantID string, examID string) ([]Paper, error) {
	rows, err := queryer.QueryContext(ctx, `
SELECT p.id::text, p.tenant_id::text, p.exam_id::text, p.file_asset_id::text, p.version_no, p.status,
       f.original_name, f.content_type, f.size_bytes, f.hash_sha256, f.storage_bucket, f.storage_key
FROM exam_paper p
JOIN file_asset f ON f.tenant_id = p.tenant_id AND f.id = p.file_asset_id
WHERE p.tenant_id = $1 AND p.exam_id = $2 AND p.deleted_at IS NULL
ORDER BY p.version_no DESC
`, tenantID, examID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Paper{}
	for rows.Next() {
		var item Paper
		if err := rows.Scan(&item.ID, &item.TenantID, &item.ExamID, &item.FileAssetID, &item.VersionNo, &item.Status, &item.File.OriginalName, &item.File.ContentType, &item.File.SizeBytes, &item.File.HashSHA256, &item.File.StorageBucket, &item.File.StorageKey); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *PostgresStore) CreatePaperImport(ctx context.Context, tenantID, examID, userID string, input CreatePaperImportInput) (PaperImportJob, error) {
	if input.CommandID == "" {
		input.CommandID = uuid.NewString()
	}
	sources := normalizePaperImportSourceInputs(input)
	if len(sources) == 0 || strings.TrimSpace(input.Subject) == "" {
		return PaperImportJob{}, ErrInvalidInput
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return PaperImportJob{}, err
	}
	defer tx.Rollback()
	var authoritativeSubject string
	if err = tx.QueryRowContext(ctx, `SELECT subject FROM exam WHERE tenant_id=$1 AND id=$2::uuid AND deleted_at IS NULL`, tenantID, examID).Scan(&authoritativeSubject); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return PaperImportJob{}, ErrNotFound
		}
		return PaperImportJob{}, err
	}
	authoritativeCode, authoritativeOK := assessment.NormalizeSubjectCode(authoritativeSubject)
	requestedCode, requestedOK := assessment.NormalizeSubjectCode(input.Subject)
	if !authoritativeOK || !requestedOK || authoritativeCode != requestedCode {
		return PaperImportJob{}, ErrInvalidInput
	}
	// The browser value is only a consistency assertion. Persist the canonical
	// subject read from the exam record so a forged/stale request cannot route OCR.
	input.Subject = string(authoritativeCode)
	requestHash := paperImportCommandHash(input)
	if existingID, existingHash, replayErr := findPaperImportCommandInTx(ctx, tx, tenantID, userID, input.CommandID); replayErr == nil {
		if existingHash != requestHash {
			return PaperImportJob{}, ErrConflict
		}
		var sameTarget bool
		if err = tx.QueryRowContext(ctx, `SELECT exam_id=$3::uuid FROM paper_import_job WHERE tenant_id=$1 AND id=$2::uuid`, tenantID, existingID, examID).Scan(&sameTarget); err != nil {
			return PaperImportJob{}, err
		}
		if !sameTarget {
			return PaperImportJob{}, ErrConflict
		}
		if err = tx.Commit(); err != nil {
			return PaperImportJob{}, err
		}
		return s.GetPaperImport(ctx, tenantID, existingID)
	} else if !errors.Is(replayErr, ErrNotFound) {
		return PaperImportJob{}, replayErr
	}
	if err = ensureExamPaperMutableTx(ctx, tx, tenantID, examID); err != nil {
		return PaperImportJob{}, err
	}
	if input.ExamPaperID != "" {
		var valid bool
		if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM exam_paper WHERE tenant_id=$1 AND exam_id=$2 AND id=$3::uuid AND deleted_at IS NULL)`, tenantID, examID, input.ExamPaperID).Scan(&valid); err != nil {
			return PaperImportJob{}, err
		}
		if !valid {
			return PaperImportJob{}, ErrInvalidInput
		}
	}
	seen := map[string]bool{}
	indexes := map[int]bool{}
	for _, source := range sources {
		if source.FileAssetID == "" || source.DocumentIndex < 0 || seen[source.FileAssetID] || indexes[source.DocumentIndex] || !validPaperImportRole(source.RoleHint, true) {
			return PaperImportJob{}, ErrInvalidInput
		}
		var valid bool
		if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM file_asset WHERE tenant_id=$1 AND id=$2::uuid AND deleted_at IS NULL AND (exam_id IS NULL OR exam_id=$3::uuid))`, tenantID, source.FileAssetID, examID).Scan(&valid); err != nil {
			return PaperImportJob{}, err
		}
		if !valid {
			return PaperImportJob{}, ErrInvalidInput
		}
		seen[source.FileAssetID], indexes[source.DocumentIndex] = true, true
	}
	row := tx.QueryRowContext(ctx, `INSERT INTO paper_import_job
(tenant_id, exam_id, exam_paper_id, paper_file_asset_id, answer_file_asset_id, status, subject, created_by)
VALUES ($1,$2,NULLIF($3,'')::uuid,NULLIF($4,'')::uuid,NULLIF($5,'')::uuid,'processing',$6,$7)
RETURNING id::text,tenant_id::text,exam_id::text,COALESCE(exam_paper_id::text,''),COALESCE(paper_file_asset_id::text,''),COALESCE(answer_file_asset_id::text,''),status,subject,draft_questions,issues,error_code,created_by::text,created_at,updated_at,applied_at`, tenantID, examID, input.ExamPaperID, input.PaperFileAssetID, input.AnswerFileAssetID, input.Subject, userID)
	job, err := scanPaperImport(row)
	if err != nil {
		return PaperImportJob{}, err
	}
	for _, source := range sources {
		var item PaperImportSource
		err = tx.QueryRowContext(ctx, `INSERT INTO paper_import_source(tenant_id,paper_import_id,file_asset_id,document_index,role_hint) VALUES($1,$2::uuid,$3::uuid,$4,$5) RETURNING id::text,file_asset_id::text,document_index,role_hint,detected_role,role_confidence::float8,processing_status,created_at`, tenantID, job.ID, source.FileAssetID, source.DocumentIndex, source.RoleHint).Scan(&item.ID, &item.FileAssetID, &item.DocumentIndex, &item.RoleHint, &item.DetectedRole, &item.RoleConfidence, &item.ProcessingStatus, &item.CreatedAt)
		if err != nil {
			return PaperImportJob{}, err
		}
		job.Sources = append(job.Sources, item)
	}
	job.Generation = 1
	job.SourceRevision = paperImportSourceConfigurationHash(job.Sources)
	if err = tx.QueryRowContext(ctx, `INSERT INTO paper_import_run
(tenant_id,paper_import_id,generation,command_id,command_type,command_request_hash,actor_id,source_revision,status,dispatch_status)
VALUES($1,$2::uuid,1,$3,'create',$4,$5::uuid,$6,'processing','pending') RETURNING id::text`,
		tenantID, job.ID, input.CommandID, requestHash, userID, job.SourceRevision).Scan(&job.RunID); err != nil {
		return PaperImportJob{}, err
	}
	if err = savePaperImportSourceSnapshot(ctx, tx, tenantID, job.RunID, job.Sources); err != nil {
		return PaperImportJob{}, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE paper_import_job SET current_generation=1,source_revision=$3 WHERE tenant_id=$1 AND id=$2::uuid`, tenantID, job.ID, job.SourceRevision); err != nil {
		return PaperImportJob{}, err
	}
	if err = tx.Commit(); err != nil {
		return PaperImportJob{}, err
	}
	return job, nil
}

func (s *PostgresStore) AddPaperImportSources(ctx context.Context, tenantID, id, userID string, input AddPaperImportSourcesInput) (PaperImportJob, error) {
	if input.CommandID == "" {
		input.CommandID = uuid.NewString()
	}
	if len(input.Sources) == 0 || input.ExpectedGeneration <= 0 {
		return PaperImportJob{}, ErrInvalidInput
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return PaperImportJob{}, err
	}
	defer tx.Rollback()
	requestHash := paperImportCommandHash(input)
	if existingID, existingHash, replayErr := findPaperImportCommandInTx(ctx, tx, tenantID, userID, input.CommandID); replayErr == nil {
		if existingID != id || existingHash != requestHash {
			return PaperImportJob{}, ErrConflict
		}
		if err = tx.Commit(); err != nil {
			return PaperImportJob{}, err
		}
		return s.GetPaperImport(ctx, tenantID, id)
	} else if !errors.Is(replayErr, ErrNotFound) {
		return PaperImportJob{}, replayErr
	}
	if err = ensureImportExamMutableInTx(ctx, tx, tenantID, id); err != nil {
		return PaperImportJob{}, err
	}
	var status string
	var generation int64
	if err = tx.QueryRowContext(ctx, `SELECT status,current_generation FROM paper_import_job WHERE tenant_id=$1 AND id=$2::uuid AND deleted_at IS NULL FOR UPDATE`, tenantID, id).Scan(&status, &generation); errors.Is(err, sql.ErrNoRows) {
		return PaperImportJob{}, ErrNotFound
	} else if err != nil {
		return PaperImportJob{}, err
	}
	if status == "applied" || status == "processing" {
		return PaperImportJob{}, ErrConflict
	}
	if input.ExpectedGeneration > 0 && input.ExpectedGeneration != generation {
		return PaperImportJob{}, ErrConflict
	}
	for _, source := range input.Sources {
		if source.FileAssetID == "" || source.DocumentIndex < 0 || !validPaperImportRole(source.RoleHint, true) {
			return PaperImportJob{}, ErrInvalidInput
		}
		result, insertErr := tx.ExecContext(ctx, `INSERT INTO paper_import_source(tenant_id,paper_import_id,file_asset_id,document_index,role_hint) SELECT $1,$2::uuid,$3::uuid,$4,$5 WHERE EXISTS(SELECT 1 FROM file_asset f JOIN paper_import_job j ON j.tenant_id=f.tenant_id AND j.id=$2::uuid WHERE f.tenant_id=$1 AND f.id=$3::uuid AND f.deleted_at IS NULL AND (f.exam_id IS NULL OR f.exam_id=j.exam_id))`, tenantID, id, source.FileAssetID, source.DocumentIndex, defaultRoleHint(source.RoleHint))
		if insertErr != nil {
			return PaperImportJob{}, ErrInvalidInput
		}
		affected, affectedErr := result.RowsAffected()
		if affectedErr != nil {
			return PaperImportJob{}, affectedErr
		}
		if affected != 1 {
			return PaperImportJob{}, ErrInvalidInput
		}
	}
	if _, err = tx.ExecContext(ctx, `UPDATE paper_import_job SET status='processing',error_code='',issues='[]'::jsonb,structured_issues='[]'::jsonb,updated_at=now() WHERE tenant_id=$1 AND id=$2::uuid`, tenantID, id); err != nil {
		return PaperImportJob{}, err
	}
	job, err := getPaperImportInTx(ctx, tx, tenantID, id)
	if err != nil {
		return PaperImportJob{}, err
	}
	revision := paperImportSourceConfigurationHash(job.Sources)
	generation++
	var runID string
	if err = tx.QueryRowContext(ctx, `INSERT INTO paper_import_run
(tenant_id,paper_import_id,generation,command_id,command_type,command_request_hash,actor_id,source_revision,status,dispatch_status)
VALUES($1,$2::uuid,$3,$4,'add_sources',$5,$6::uuid,$7,'processing','pending') RETURNING id::text`, tenantID, id, generation, input.CommandID, requestHash, userID, revision).Scan(&runID); err != nil {
		return PaperImportJob{}, err
	}
	if err = savePaperImportSourceSnapshot(ctx, tx, tenantID, runID, job.Sources); err != nil {
		return PaperImportJob{}, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE paper_import_job SET current_generation=$3,source_revision=$4,result_generation=NULL,result_task_id=NULL,result_payload_hash=NULL WHERE tenant_id=$1 AND id=$2::uuid`, tenantID, id, generation, revision); err != nil {
		return PaperImportJob{}, err
	}
	if err = supersedePaperImportRunsInTx(ctx, tx, tenantID, id, generation); err != nil {
		return PaperImportJob{}, err
	}
	if err = tx.Commit(); err != nil {
		return PaperImportJob{}, err
	}
	return s.GetPaperImport(ctx, tenantID, id)
}

func (s *PostgresStore) ReplacePaperImportSources(ctx context.Context, tenantID, id, userID string, input ReplacePaperImportSourcesInput) (PaperImportJob, error) {
	if input.CommandID == "" {
		input.CommandID = uuid.NewString()
	}
	if input.ExpectedGeneration <= 0 {
		return PaperImportJob{}, ErrInvalidInput
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return PaperImportJob{}, err
	}
	defer tx.Rollback()
	requestHash := paperImportCommandHash(input)
	if existingID, existingHash, replayErr := findPaperImportCommandInTx(ctx, tx, tenantID, userID, input.CommandID); replayErr == nil {
		if existingID != id || existingHash != requestHash {
			return PaperImportJob{}, ErrConflict
		}
		if err = tx.Commit(); err != nil {
			return PaperImportJob{}, err
		}
		return s.GetPaperImport(ctx, tenantID, id)
	} else if !errors.Is(replayErr, ErrNotFound) {
		return PaperImportJob{}, replayErr
	}
	if err = ensureImportExamMutableInTx(ctx, tx, tenantID, id); err != nil {
		return PaperImportJob{}, err
	}
	var status string
	var generation int64
	if err = tx.QueryRowContext(ctx, `SELECT status,current_generation FROM paper_import_job WHERE tenant_id=$1 AND id=$2::uuid AND deleted_at IS NULL FOR UPDATE`, tenantID, id).Scan(&status, &generation); errors.Is(err, sql.ErrNoRows) {
		return PaperImportJob{}, ErrNotFound
	} else if err != nil {
		return PaperImportJob{}, err
	}
	if status == "applied" {
		return PaperImportJob{}, ErrConflict
	}
	if input.ExpectedGeneration > 0 && input.ExpectedGeneration != generation {
		return PaperImportJob{}, ErrConflict
	}
	rows, err := tx.QueryContext(ctx, `SELECT id::text FROM paper_import_source WHERE tenant_id=$1 AND paper_import_id=$2::uuid AND deleted_at IS NULL FOR UPDATE`, tenantID, id)
	if err != nil {
		return PaperImportJob{}, err
	}
	active := map[string]bool{}
	for rows.Next() {
		var sourceID string
		if err = rows.Scan(&sourceID); err != nil {
			rows.Close()
			return PaperImportJob{}, err
		}
		active[sourceID] = true
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return PaperImportJob{}, err
	}
	if err = rows.Close(); err != nil {
		return PaperImportJob{}, err
	}
	seenIDs := map[string]bool{}
	seenIndexes := map[int]bool{}
	for _, source := range input.Sources {
		if !active[source.ID] || source.DocumentIndex < 0 || seenIDs[source.ID] || seenIndexes[source.DocumentIndex] || !validPaperImportRole(source.RoleHint, true) {
			return PaperImportJob{}, ErrInvalidInput
		}
		seenIDs[source.ID], seenIndexes[source.DocumentIndex] = true, true
	}
	if _, err = tx.ExecContext(ctx, `UPDATE paper_import_source SET deleted_at=now(),updated_at=now() WHERE tenant_id=$1 AND paper_import_id=$2::uuid AND deleted_at IS NULL`, tenantID, id); err != nil {
		return PaperImportJob{}, err
	}
	for _, source := range input.Sources {
		result, updateErr := tx.ExecContext(ctx, `UPDATE paper_import_source SET document_index=$4,role_hint=$5,detected_role='unknown',role_confidence=0,processing_status='pending',deleted_at=NULL,updated_at=now() WHERE tenant_id=$1 AND paper_import_id=$2::uuid AND id=$3::uuid`, tenantID, id, source.ID, source.DocumentIndex, defaultRoleHint(source.RoleHint))
		if updateErr != nil {
			return PaperImportJob{}, updateErr
		}
		affected, affectedErr := result.RowsAffected()
		if affectedErr != nil {
			return PaperImportJob{}, affectedErr
		}
		if affected != 1 {
			return PaperImportJob{}, ErrConflict
		}
	}
	if len(input.Sources) == 0 {
		issues, _ := json.Marshal([]string{"已删除全部考试资料，请重新上传正确的资料"})
		_, err = tx.ExecContext(ctx, `UPDATE paper_import_job SET status='failed',error_code='paper_import_no_sources',issues=$3,structured_issues='[]'::jsonb,updated_at=now() WHERE tenant_id=$1 AND id=$2::uuid`, tenantID, id, issues)
	} else {
		_, err = tx.ExecContext(ctx, `UPDATE paper_import_job SET status='processing',error_code='',issues='[]'::jsonb,structured_issues='[]'::jsonb,updated_at=now() WHERE tenant_id=$1 AND id=$2::uuid`, tenantID, id)
	}
	if err != nil {
		return PaperImportJob{}, err
	}
	job, loadErr := getPaperImportInTx(ctx, tx, tenantID, id)
	if loadErr != nil {
		return PaperImportJob{}, loadErr
	}
	revision := paperImportSourceConfigurationHash(job.Sources)
	generation++
	runStatus, dispatch := "processing", "pending"
	if len(input.Sources) == 0 {
		runStatus, dispatch = "failed", "not_required"
	}
	var runID string
	if err = tx.QueryRowContext(ctx, `INSERT INTO paper_import_run
(tenant_id,paper_import_id,generation,command_id,command_type,command_request_hash,actor_id,source_revision,status,dispatch_status,error_code)
VALUES($1,$2::uuid,$3,$4,'replace_sources',$5,$6::uuid,$7,$8,$9,CASE WHEN $8='failed' THEN 'paper_import_no_sources' ELSE NULL END) RETURNING id::text`, tenantID, id, generation, input.CommandID, requestHash, userID, revision, runStatus, dispatch).Scan(&runID); err != nil {
		return PaperImportJob{}, err
	}
	if err = savePaperImportSourceSnapshot(ctx, tx, tenantID, runID, job.Sources); err != nil {
		return PaperImportJob{}, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE paper_import_job SET current_generation=$3,source_revision=$4,result_generation=NULL,result_task_id=NULL,result_payload_hash=NULL WHERE tenant_id=$1 AND id=$2::uuid`, tenantID, id, generation, revision); err != nil {
		return PaperImportJob{}, err
	}
	if err = supersedePaperImportRunsInTx(ctx, tx, tenantID, id, generation); err != nil {
		return PaperImportJob{}, err
	}
	if err = tx.Commit(); err != nil {
		return PaperImportJob{}, err
	}
	return s.GetPaperImport(ctx, tenantID, id)
}

func (s *PostgresStore) CompletePaperImportCandidates(ctx context.Context, tenantID, id string, detected []PaperImportDetectedDocument, questions []QuestionCandidate, answers []AnswerCandidate, solutions []SolutionCandidate, rubrics []RubricCandidate, issues []PaperImportIssue) (PaperImportJob, error) {
	// PostgreSQL imports must publish through CompletePaperImportParseTask, which
	// verifies protocol v2, run/generation/input binding and the active lease in
	// one transaction. Keeping this interface method for the memory store avoids
	// a broad API break, while rejecting every legacy unversioned database write.
	return PaperImportJob{}, ErrConflict
}

func (s *PostgresStore) applyPaperImportAssessmentArchetypes(ctx context.Context, tenantID, importID string, drafts []PaperImportDraftQuestion) error {
	rows, err := s.db.QueryContext(ctx, `
SELECT q.question_no,COALESCE(config.archetype_code,'')
FROM paper_import_job job
JOIN question q ON q.tenant_id=job.tenant_id AND q.exam_id=job.exam_id AND q.deleted_at IS NULL AND q.status<>'deleted'
LEFT JOIN question_assessment_config config ON config.tenant_id=q.tenant_id AND config.exam_id=q.exam_id AND config.question_id=q.id
WHERE job.tenant_id=$1 AND job.id=$2::uuid AND job.deleted_at IS NULL
`, tenantID, importID)
	if err != nil {
		return err
	}
	defer rows.Close()
	byNumber := map[string]string{}
	for rows.Next() {
		var number, archetype string
		if err := rows.Scan(&number, &archetype); err != nil {
			return err
		}
		if archetype != "" {
			byNumber[normalizePaperImportQuestionNumber(number)] = archetype
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for index := range drafts {
		if archetype := byNumber[normalizePaperImportQuestionNumber(drafts[index].QuestionNo)]; archetype != "" {
			drafts[index].AssessmentArchetype = archetype
		} else if drafts[index].AssessmentArchetype == "" {
			drafts[index].AssessmentArchetype = defaultPaperImportArchetype(drafts[index].QuestionType)
		}
	}
	return nil
}

func (s *PostgresStore) SavePaperImportReview(ctx context.Context, tenantID, id, _ string, input ReviewPaperImportInput) (PaperImportJob, error) {
	if input.ExpectedGeneration <= 0 {
		return PaperImportJob{}, ErrConflict
	}
	if err := s.applyPaperImportAssessmentArchetypes(ctx, tenantID, id, input.Questions); err != nil {
		return PaperImportJob{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return PaperImportJob{}, err
	}
	defer tx.Rollback()
	var currentGeneration int64
	var status string
	if err = tx.QueryRowContext(ctx, `SELECT current_generation,status FROM paper_import_job WHERE tenant_id=$1 AND id=$2::uuid AND deleted_at IS NULL FOR UPDATE`, tenantID, id).Scan(&currentGeneration, &status); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return PaperImportJob{}, ErrNotFound
		}
		return PaperImportJob{}, err
	}
	if status != "review_required" || currentGeneration != input.ExpectedGeneration {
		return PaperImportJob{}, ErrConflict
	}
	job, err := getPaperImportInTx(ctx, tx, tenantID, id)
	if err != nil {
		return PaperImportJob{}, err
	}
	for i := range input.Questions {
		input.Questions[i].HumanConfirmedFields = normalizeHumanConfirmedFields(input.Questions[i].HumanConfirmedFields)
		refreshDraftCompleteness(&input.Questions[i], len(job.QuestionCandidates) > 0)
	}
	structured := appendReviewedDraftIssues(issuesAfterHumanReview(job.StructuredIssues, input.Questions, true), input.Questions)
	structured = s.appendPaperImportBlueprintIssues(ctx, tenantID, id, questionCandidatesFromDrafts(input.Questions), withoutBlueprintIssues(structured))
	q, _ := json.Marshal(input.Questions)
	si, _ := json.Marshal(structured)
	messages, _ := json.Marshal(issueMessages(structured))
	result, err := tx.ExecContext(ctx, `UPDATE paper_import_job SET draft_questions=$3,structured_issues=$4,issues=$5,updated_at=now() WHERE tenant_id=$1 AND id=$2::uuid AND current_generation=$6 AND status='review_required' AND deleted_at IS NULL`, tenantID, id, q, si, messages, input.ExpectedGeneration)
	if err != nil {
		return PaperImportJob{}, err
	}
	n, rowsErr := result.RowsAffected()
	if rowsErr != nil {
		return PaperImportJob{}, rowsErr
	}
	if n != 1 {
		return PaperImportJob{}, ErrConflict
	}
	if err = tx.Commit(); err != nil {
		return PaperImportJob{}, err
	}
	return s.GetPaperImport(ctx, tenantID, id)
}

func (s *PostgresStore) appendPaperImportBlueprintIssues(ctx context.Context, tenantID, importID string, questions []QuestionCandidate, issues []PaperImportIssue) []PaperImportIssue {
	var examID string
	var total float64
	if err := s.db.QueryRowContext(ctx, `SELECT exam_id::text,total_score::float8 FROM exam JOIN paper_import_job j ON j.exam_id=exam.id AND j.tenant_id=exam.tenant_id WHERE j.tenant_id=$1 AND j.id=$2::uuid AND exam.deleted_at IS NULL`, tenantID, importID).Scan(&examID, &total); err != nil {
		return issues
	}
	type section struct {
		title, kind string
		count       int
		score       float64
	}
	sections := []section{}
	rows, err := s.db.QueryContext(ctx, `SELECT title,question_type,question_count,score_per_question::float8 FROM exam_blueprint_section WHERE tenant_id=$1 AND exam_id=$2::uuid AND deleted_at IS NULL ORDER BY sort_order`, tenantID, examID)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var item section
			if rows.Scan(&item.title, &item.kind, &item.count, &item.score) == nil {
				sections = append(sections, item)
			}
		}
	}
	knownTotal := 0.0
	byType := map[string]int{}
	for _, q := range questions {
		byType[q.QuestionType]++
		if q.Score != nil {
			knownTotal += *q.Score
		}
	}
	if len(sections) > 0 {
		expected := 0
		expectedByType := map[string]int{}
		titlesByType := map[string][]string{}
		for _, section := range sections {
			expected += section.count
			expectedByType[section.kind] += section.count
			titlesByType[section.kind] = append(titlesByType[section.kind], section.title)
		}
		for _, kind := range sortedPaperImportKeys(expectedByType) {
			expectedCount := expectedByType[kind]
			if byType[kind] == expectedCount {
				continue
			}
			label := strings.Join(titlesByType[kind], " / ")
			issues = append(issues, candidateIssue("SECTION_COUNT_MISMATCH", "error", "confirmed", "", fmt.Sprintf("%s预计%d道，当前识别%d道", label, expectedCount, byType[kind]), "请核对该部分是否漏传或题型识别错误", nil))
		}
		if len(questions) != expected {
			issues = append(issues, candidateIssue("QUESTION_COUNT_MISMATCH", "error", "confirmed", "", fmt.Sprintf("考试配置共%d道，当前识别%d道", expected, len(questions)), "请核对缺失资料或识别结果", nil))
		}
	}
	if len(questions) > 0 && !scoreEqual(knownTotal, total) {
		issues = append(issues, candidateIssue("SCORE_TOTAL_MISMATCH", "error", "confirmed", "", fmt.Sprintf("当前识别题目合计%.2f分，与考试配置%.2f分不一致", knownTotal, total), "可能存在漏题、分值识别错误或考试配置差异", nil))
	}
	return dedupePaperImportIssues(issues)
}

func (s *PostgresStore) CompletePaperImport(ctx context.Context, tenantID, id string, questions []PaperImportDraftQuestion, issues []string) (PaperImportJob, error) {
	// This pre-v2 completion hook has no run/input/task/lease identity. Production
	// PostgreSQL callers must use CompletePaperImportParseTask instead.
	return PaperImportJob{}, ErrConflict
}

type paperImportQuestionReader interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func reconcilePaperImportQuestions(ctx context.Context, reader paperImportQuestionReader, tenantID, examID string, drafts []PaperImportDraftQuestion, baseIssues []string) ([]PaperImportDraftQuestion, []string, error) {
	totalRows, err := reader.QueryContext(ctx, `SELECT total_score::float8 FROM exam WHERE tenant_id=$1::uuid AND id=$2::uuid AND deleted_at IS NULL`, tenantID, examID)
	if err != nil {
		return nil, nil, err
	}
	var expectedTotal float64
	if !totalRows.Next() {
		err := totalRows.Err()
		totalRows.Close()
		if err != nil {
			return nil, nil, err
		}
		return nil, nil, ErrNotFound
	}
	if err := totalRows.Scan(&expectedTotal); err != nil {
		totalRows.Close()
		return nil, nil, err
	}
	if err := totalRows.Close(); err != nil {
		return nil, nil, err
	}

	rows, err := reader.QueryContext(ctx, `SELECT id::text,question_no,question_type,score::float8,sort_order FROM question WHERE tenant_id=$1 AND exam_id=$2::uuid AND deleted_at IS NULL AND status<>'deleted' ORDER BY sort_order,question_no`, tenantID, examID)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	existing := []paperImportExistingQuestion{}
	for rows.Next() {
		var item paperImportExistingQuestion
		if err := rows.Scan(&item.id, &item.number, &item.kind, &item.score, &item.sortOrder); err != nil {
			return nil, nil, err
		}
		existing = append(existing, item)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	drafts, issues := reconcilePaperImportDrafts(drafts, existing, &expectedTotal, baseIssues)
	return drafts, issues, nil
}

func dedupeStrings(values []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" && !seen[value] {
			seen[value] = true
			out = append(out, value)
		}
	}
	return out
}

func (s *PostgresStore) FailPaperImport(ctx context.Context, tenantID, id, code string, issues []string) (PaperImportJob, error) {
	i, _ := json.Marshal(issues)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return PaperImportJob{}, err
	}
	defer tx.Rollback()
	var generation int64
	result, err := tx.ExecContext(ctx, `UPDATE paper_import_job SET status='failed',issues=$3,error_code=$4,updated_at=now() WHERE tenant_id=$1 AND id=$2::uuid AND status='processing' AND deleted_at IS NULL`, tenantID, id, i, code)
	if err != nil {
		return PaperImportJob{}, err
	}
	rows, rowsErr := result.RowsAffected()
	if rowsErr != nil {
		return PaperImportJob{}, rowsErr
	}
	if rows != 1 {
		return PaperImportJob{}, ErrConflict
	}
	if err = tx.QueryRowContext(ctx, `SELECT current_generation FROM paper_import_job WHERE tenant_id=$1 AND id=$2::uuid`, tenantID, id).Scan(&generation); err != nil {
		return PaperImportJob{}, err
	}
	runResult, err := tx.ExecContext(ctx, `UPDATE paper_import_run SET status='failed',dispatch_status=CASE WHEN dispatch_status='pending' THEN 'failed' ELSE dispatch_status END,error_code=$4,completed_at=now(),updated_at=now() WHERE tenant_id=$1 AND paper_import_id=$2::uuid AND generation=$3 AND status='processing'`, tenantID, id, generation, code)
	if err != nil {
		return PaperImportJob{}, err
	}
	runRows, rowsErr := runResult.RowsAffected()
	if rowsErr != nil {
		return PaperImportJob{}, rowsErr
	}
	if runRows != 1 {
		return PaperImportJob{}, ErrConflict
	}
	if err = tx.Commit(); err != nil {
		return PaperImportJob{}, err
	}
	return s.GetPaperImport(ctx, tenantID, id)
}

func (s *PostgresStore) CancelPaperImport(ctx context.Context, tenantID, id string) (PaperImportJob, error) {
	return s.cancelPaperImportGeneration(ctx, tenantID, id, 0)
}

func (s *PostgresStore) CancelPaperImportGeneration(ctx context.Context, tenantID, id string, expectedGeneration int64) (PaperImportJob, error) {
	if expectedGeneration <= 0 {
		return PaperImportJob{}, ErrInvalidInput
	}
	return s.cancelPaperImportGeneration(ctx, tenantID, id, expectedGeneration)
}

func (s *PostgresStore) cancelPaperImportGeneration(ctx context.Context, tenantID, id string, expectedGeneration int64) (PaperImportJob, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return PaperImportJob{}, err
	}
	defer tx.Rollback()
	var status string
	var generation int64
	if err = tx.QueryRowContext(ctx, `SELECT status,current_generation FROM paper_import_job WHERE tenant_id=$1 AND id=$2::uuid AND deleted_at IS NULL FOR UPDATE`, tenantID, id).Scan(&status, &generation); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return PaperImportJob{}, ErrNotFound
		}
		return PaperImportJob{}, err
	}
	if status != "processing" {
		return PaperImportJob{}, ErrConflict
	}
	if expectedGeneration > 0 && expectedGeneration != generation {
		return PaperImportJob{}, ErrConflict
	}
	issues, _ := json.Marshal([]string{"识别任务已手动停止"})
	if err = lockPaperImportTasksInTx(ctx, tx, tenantID, id); err != nil {
		return PaperImportJob{}, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE paper_import_job SET status='cancelled',issues=$3,error_code='paper_import_cancelled',updated_at=now() WHERE tenant_id=$1 AND id=$2::uuid`, tenantID, id, issues); err != nil {
		return PaperImportJob{}, err
	}
	if _, err = tx.ExecContext(ctx, `
UPDATE agent_worker_task_attempt AS attempt
SET status='cancelled',completed_at=now(),error_code='cancelled'
FROM agent_worker_task AS task
WHERE attempt.tenant_id=$1 AND attempt.task_id=task.id AND attempt.completed_at IS NULL
  AND task.tenant_id=$1 AND (
    (task.source_type='paper_import_job' AND task.source_id=$2::uuid)
    OR (task.source_type='paper_import_parse' AND EXISTS (
      SELECT 1 FROM paper_import_parse_input parse_input
      WHERE parse_input.tenant_id=task.tenant_id AND parse_input.id=task.source_id
        AND parse_input.paper_import_id=$2::uuid
    ))
  )
  AND task.status IN ('queued','leased','running')`, tenantID, id); err != nil {
		return PaperImportJob{}, err
	}
	if _, err = tx.ExecContext(ctx, `
UPDATE agent_worker_task
SET status='cancelled',cancelled_at=now(),completed_at=now(),updated_at=now(),revision=revision+1
WHERE tenant_id=$1 AND (
    (source_type='paper_import_job' AND source_id=$2::uuid)
    OR (source_type='paper_import_parse' AND EXISTS (
      SELECT 1 FROM paper_import_parse_input parse_input
      WHERE parse_input.tenant_id=agent_worker_task.tenant_id AND parse_input.id=agent_worker_task.source_id
        AND parse_input.paper_import_id=$2::uuid
    ))
  )
  AND status IN ('queued','leased','running')`, tenantID, id); err != nil {
		return PaperImportJob{}, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE paper_import_source SET processing_status='failed',updated_at=now() WHERE tenant_id=$1 AND paper_import_id=$2::uuid AND processing_status IN ('pending','processing') AND deleted_at IS NULL`, tenantID, id); err != nil {
		return PaperImportJob{}, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE paper_import_run SET status='cancelled',dispatch_status=CASE WHEN dispatch_status='pending' THEN 'not_required' ELSE dispatch_status END,error_code='paper_import_cancelled',completed_at=now(),updated_at=now() WHERE tenant_id=$1 AND paper_import_id=$2::uuid AND generation=$3 AND status='processing'`, tenantID, id, generation); err != nil {
		return PaperImportJob{}, err
	}
	if err = tx.Commit(); err != nil {
		return PaperImportJob{}, err
	}
	return s.GetPaperImport(ctx, tenantID, id)
}

func (s *PostgresStore) GetPaperImport(ctx context.Context, tenantID, id string) (PaperImportJob, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id::text,tenant_id::text,exam_id::text,COALESCE(exam_paper_id::text,''),COALESCE(paper_file_asset_id::text,''),COALESCE(answer_file_asset_id::text,''),status,subject,draft_questions,issues,error_code,created_by::text,created_at,updated_at,applied_at FROM paper_import_job WHERE tenant_id=$1 AND id=$2::uuid AND deleted_at IS NULL`, tenantID, id)
	job, err := scanPaperImport(row)
	if err != nil {
		return job, err
	}
	return s.loadPaperImportDetails(ctx, tenantID, job)
}

func (s *PostgresStore) ListPaperImports(ctx context.Context, tenantID, examID string) ([]PaperImportJob, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id::text,tenant_id::text,exam_id::text,COALESCE(exam_paper_id::text,''),COALESCE(paper_file_asset_id::text,''),COALESCE(answer_file_asset_id::text,''),status,subject,draft_questions,issues,error_code,created_by::text,created_at,updated_at,applied_at FROM paper_import_job WHERE tenant_id=$1 AND exam_id=$2 AND deleted_at IS NULL ORDER BY created_at DESC`, tenantID, examID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []PaperImportJob{}
	for rows.Next() {
		item, err := scanPaperImport(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		out[i], err = s.loadPaperImportDetails(ctx, tenantID, out[i])
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (s *PostgresStore) loadPaperImportDetails(ctx context.Context, tenantID string, job PaperImportJob) (PaperImportJob, error) {
	if err := s.hydratePaperImportRun(ctx, tenantID, &job); err != nil {
		return PaperImportJob{}, err
	}
	var q, a, so, r, si, usage []byte
	if err := s.db.QueryRowContext(ctx, `SELECT question_candidates,answer_candidates,solution_candidates,rubric_candidates,structured_issues,model_usage FROM paper_import_job WHERE tenant_id=$1 AND id=$2::uuid`, tenantID, job.ID).Scan(&q, &a, &so, &r, &si, &usage); err != nil {
		return PaperImportJob{}, err
	}
	_ = json.Unmarshal(q, &job.QuestionCandidates)
	_ = json.Unmarshal(a, &job.AnswerCandidates)
	_ = json.Unmarshal(so, &job.SolutionCandidates)
	_ = json.Unmarshal(r, &job.RubricCandidates)
	_ = json.Unmarshal(si, &job.StructuredIssues)
	_ = json.Unmarshal(usage, &job.ModelUsage)
	rows, err := s.db.QueryContext(ctx, `SELECT s.id::text,s.file_asset_id::text,s.document_index,s.role_hint,s.detected_role,s.role_confidence::float8,s.processing_status,f.original_name,f.content_type,s.created_at FROM paper_import_source s JOIN file_asset f ON f.tenant_id=s.tenant_id AND f.id=s.file_asset_id WHERE s.tenant_id=$1 AND s.paper_import_id=$2::uuid AND s.deleted_at IS NULL ORDER BY s.document_index`, tenantID, job.ID)
	if err != nil {
		return PaperImportJob{}, err
	}
	defer rows.Close()
	job.Sources = []PaperImportSource{}
	for rows.Next() {
		var item PaperImportSource
		if err := rows.Scan(&item.ID, &item.FileAssetID, &item.DocumentIndex, &item.RoleHint, &item.DetectedRole, &item.RoleConfidence, &item.ProcessingStatus, &item.OriginalName, &item.ContentType, &item.CreatedAt); err != nil {
			return PaperImportJob{}, err
		}
		job.Sources = append(job.Sources, item)
	}
	return job, rows.Err()
}

func (s *PostgresStore) ApplyPaperImport(ctx context.Context, tenantID, id, userID string) (PaperImportJob, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return PaperImportJob{}, err
	}
	defer tx.Rollback()
	job, err := scanPaperImport(tx.QueryRowContext(ctx, `SELECT id::text,tenant_id::text,exam_id::text,COALESCE(exam_paper_id::text,''),COALESCE(paper_file_asset_id::text,''),COALESCE(answer_file_asset_id::text,''),status,subject,draft_questions,issues,error_code,created_by::text,created_at,updated_at,applied_at FROM paper_import_job WHERE tenant_id=$1 AND id=$2::uuid AND deleted_at IS NULL FOR UPDATE`, tenantID, id))
	if errors.Is(err, sql.ErrNoRows) {
		return PaperImportJob{}, ErrNotFound
	}
	if err != nil {
		return PaperImportJob{}, err
	}
	if job.Status != "review_required" {
		return PaperImportJob{}, ErrConflict
	}
	var generation int64
	var resultGeneration sql.NullInt64
	var runID, runStatus, runSourceRevision, jobResultTaskID, runResultTaskID, jobResultHash, runResultHash string
	if err := tx.QueryRowContext(ctx, `SELECT job.current_generation,job.result_generation,COALESCE(job.result_task_id::text,''),COALESCE(job.result_payload_hash,''),
run.id::text,run.status,run.source_revision,COALESCE(run.result_task_id::text,''),COALESCE(run.result_payload_hash,'')
FROM paper_import_job job
JOIN paper_import_run run ON run.tenant_id=job.tenant_id AND run.paper_import_id=job.id AND run.generation=job.current_generation
WHERE job.tenant_id=$1 AND job.id=$2::uuid
FOR UPDATE OF run`, tenantID, id).Scan(&generation, &resultGeneration, &jobResultTaskID, &jobResultHash, &runID, &runStatus, &runSourceRevision, &runResultTaskID, &runResultHash); err != nil {
		return PaperImportJob{}, err
	}
	verifiedResult := resultGeneration.Valid && resultGeneration.Int64 == generation && jobResultTaskID != "" && jobResultTaskID == runResultTaskID && jobResultHash != "" && jobResultHash == runResultHash
	legacyHumanReview := runSourceRevision == "legacy-unverified" && jobResultTaskID == "" && runResultTaskID == ""
	if runStatus != "review_required" || (!verifiedResult && !legacyHumanReview) {
		return PaperImportJob{}, ErrConflict
	}
	var structured, candidateQuestions []byte
	if err := tx.QueryRowContext(ctx, `SELECT structured_issues,question_candidates FROM paper_import_job WHERE tenant_id=$1 AND id=$2::uuid`, tenantID, id).Scan(&structured, &candidateQuestions); err != nil {
		return PaperImportJob{}, err
	}
	_ = json.Unmarshal(structured, &job.StructuredIssues)
	_ = json.Unmarshal(candidateQuestions, &job.QuestionCandidates)
	if err := ensureExamPaperMutableTx(ctx, tx, tenantID, job.ExamID); err != nil {
		return PaperImportJob{}, err
	}
	var existing int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM question WHERE tenant_id=$1 AND exam_id=$2 AND deleted_at IS NULL AND status<>'deleted'`, tenantID, job.ExamID).Scan(&existing); err != nil {
		return PaperImportJob{}, err
	}
	job.Questions, job.Issues, err = reconcilePaperImportQuestions(ctx, tx, tenantID, job.ExamID, job.Questions, job.Issues)
	if err != nil {
		return PaperImportJob{}, err
	}
	if paperImportHasBlockingIssues(job) {
		return PaperImportJob{}, ErrInvalidInput
	}
	for index := range job.Questions {
		draft := &job.Questions[index]
		if err := validateQuestionInput(draft.QuestionNo, draft.QuestionType, draft.Score); err != nil {
			return PaperImportJob{}, ErrInvalidInput
		}
		if existing > 0 {
			if draft.MatchedQuestionID == "" || draft.MatchStatus == "extra" || draft.MatchStatus == "ambiguous" {
				continue
			}
			kp, _ := json.Marshal(draft.KnowledgePoints)
			refs, _ := json.Marshal(draft.SourceRefs)
			result, updateErr := tx.ExecContext(ctx, `UPDATE question SET exam_paper_id=NULLIF($3,'')::uuid,stem=$4,knowledge_points=$5,paper_import_id=$7::uuid,paper_import_candidate_id=NULLIF($8,''),paper_import_source_refs=$9,updated_at=now() WHERE tenant_id=$1 AND id=$2::uuid AND exam_id=$6::uuid AND deleted_at IS NULL AND status<>'deleted'`, tenantID, draft.MatchedQuestionID, job.ExamPaperID, draft.Stem, kp, job.ExamID, job.ID, draft.CandidateID, refs)
			if updateErr != nil {
				return PaperImportJob{}, updateErr
			}
			affected, affectedErr := result.RowsAffected()
			if affectedErr != nil {
				return PaperImportJob{}, affectedErr
			}
			if affected != 1 {
				return PaperImportJob{}, ErrConflict
			}
			// A core mismatch is intentionally review-only. The blueprint score and
			// type remain authoritative, and importing an answer/rubric for a
			// different question shape would create a second inconsistency.
			if draft.MatchStatus == "mismatch" {
				continue
			}
			if err := s.applyImportedAnswerSolutionAndRubric(ctx, tx, tenantID, job.ID, draft.MatchedQuestionID, userID, *draft); err != nil {
				return PaperImportJob{}, err
			}
			continue
		}
		kp, _ := json.Marshal(draft.KnowledgePoints)
		refs, _ := json.Marshal(draft.SourceRefs)
		area := []byte(`{}`)
		var questionID string
		if err := tx.QueryRowContext(ctx, `INSERT INTO question (tenant_id,exam_id,exam_paper_id,question_no,question_type,score,stem,knowledge_points,answer_area,sort_order,status,paper_import_id,paper_import_candidate_id,paper_import_source_refs) VALUES ($1,$2,NULLIF($3,'')::uuid,$4,$5,$6,$7,$8,$9,$10,'active',$11::uuid,NULLIF($12,''),$13) RETURNING id::text`, tenantID, job.ExamID, job.ExamPaperID, draft.QuestionNo, draft.QuestionType, draft.Score, draft.Stem, kp, area, index+1, job.ID, draft.CandidateID, refs).Scan(&questionID); err != nil {
			return PaperImportJob{}, err
		}
		draft.MatchedQuestionID = questionID
		draft.MatchStatus = "matched"
		if err := s.applyImportedAnswerSolutionAndRubric(ctx, tx, tenantID, job.ID, questionID, userID, *draft); err != nil {
			return PaperImportJob{}, err
		}
	}
	questionsJSON, _ := json.Marshal(job.Questions)
	issuesJSON, _ := json.Marshal(job.Issues)
	job, err = scanPaperImport(tx.QueryRowContext(ctx, `UPDATE paper_import_job SET status='applied',draft_questions=$3,issues=$4,applied_at=now(),updated_at=now() WHERE tenant_id=$1 AND id=$2::uuid RETURNING id::text,tenant_id::text,exam_id::text,COALESCE(exam_paper_id::text,''),COALESCE(paper_file_asset_id::text,''),COALESCE(answer_file_asset_id::text,''),status,subject,draft_questions,issues,error_code,created_by::text,created_at,updated_at,applied_at`, tenantID, id, questionsJSON, issuesJSON))
	if err != nil {
		return PaperImportJob{}, err
	}
	runResult, err := tx.ExecContext(ctx, `UPDATE paper_import_run SET status='applied',completed_at=COALESCE(completed_at,now()),updated_at=now() WHERE tenant_id=$1 AND id=$2::uuid AND generation=$3 AND status='review_required'`, tenantID, runID, generation)
	if err != nil {
		return PaperImportJob{}, err
	}
	runRows, rowsErr := runResult.RowsAffected()
	if rowsErr != nil {
		return PaperImportJob{}, rowsErr
	}
	if runRows != 1 {
		return PaperImportJob{}, ErrConflict
	}
	if err := tx.Commit(); err != nil {
		return PaperImportJob{}, err
	}
	return job, nil
}

func (s *PostgresStore) applyImportedAnswerSolutionAndRubric(ctx context.Context, tx *sql.Tx, tenantID, importID, questionID, userID string, draft PaperImportDraftQuestion) error {
	if draft.AnswerKey != nil {
		var count int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM question_answer_key WHERE tenant_id=$1 AND question_id=$2::uuid AND deleted_at IS NULL`, tenantID, questionID).Scan(&count); err != nil {
			return err
		}
		key, err := s.insertAnswerKey(ctx, tx, tenantID, questionID, userID, fmt.Sprintf("v%d", count+1), *draft.AnswerKey)
		if err != nil {
			return err
		}
		refs, _ := json.Marshal(draft.SourceRefs)
		if _, err = tx.ExecContext(ctx, `UPDATE question_answer_key SET paper_import_id=$3::uuid,paper_import_candidate_id=NULLIF($4,''),paper_import_source_refs=$5 WHERE tenant_id=$1 AND id=$2::uuid`, tenantID, key.ID, importID, draft.AnswerCandidateID, refs); err != nil {
			return err
		}
	}
	if draft.Solution != nil {
		steps, _ := json.Marshal(draft.Solution.Steps)
		refs, _ := json.Marshal(draft.Solution.SourceRefs)
		var count int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM question_solution WHERE tenant_id=$1 AND question_id=$2::uuid AND deleted_at IS NULL`, tenantID, questionID).Scan(&count); err != nil {
			return err
		}
		verification := "machine"
		if stringSet(draft.HumanConfirmedFields)["solution"] {
			verification = "human_confirmed"
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO question_solution(tenant_id,question_id,paper_import_id,solution_version,raw_text,steps,source_refs,verification_status,created_by) VALUES($1,$2::uuid,$3::uuid,$4,$5,$6,$7,$8,$9::uuid)`, tenantID, questionID, importID, fmt.Sprintf("v%d", count+1), draft.Solution.RawText, steps, refs, verification, userID); err != nil {
			return err
		}
	}
	if draft.Rubric == nil {
		return nil
	}
	status := draft.Rubric.Status
	if status == "" {
		status = "draft"
	}
	if !IsValidRubricStatus(status) {
		return ErrInvalidInput
	}
	if status == "locked" && !stringSet(draft.HumanConfirmedFields)["rubric"] {
		return ErrInvalidInput
	}
	if !ValidRubricEvidenceRequirements(draft.Rubric.Points) {
		return ErrInvalidInput
	}
	if !scoreEqual(SumRubricPoints(draft.Rubric.Points), draft.Score) || !scoreEqual(draft.Rubric.MaxScore, draft.Score) {
		return ErrRubricMismatch
	}
	var count int
	var locked bool
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*),COALESCE(BOOL_OR(status='locked'),false) FROM rubric_version WHERE tenant_id=$1 AND question_id=$2::uuid AND deleted_at IS NULL`, tenantID, questionID).Scan(&count, &locked); err != nil {
		return err
	}
	if locked {
		return ErrRubricLocked
	}
	points, _ := json.Marshal(draft.Rubric.Points)
	deductions, _ := json.Marshal(draft.Rubric.Deductions)
	examples, _ := json.Marshal(draft.Rubric.Examples)
	hash := contentHash(points, deductions, examples)
	var versionID string
	if err := tx.QueryRowContext(ctx, `INSERT INTO rubric_version (tenant_id,question_id,version,status,content_hash,created_by) VALUES ($1,$2,$3,$4,$5,$6) RETURNING id::text`, tenantID, questionID, fmt.Sprintf("v%d", count+1), status, hash, userID).Scan(&versionID); err != nil {
		return err
	}
	refs, _ := json.Marshal(draft.SourceRefs)
	_, err := tx.ExecContext(ctx, `INSERT INTO question_rubric (tenant_id,question_id,rubric_version_id,status,max_score,points,deductions,examples,created_by,paper_import_id,paper_import_candidate_id,paper_import_source_refs) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10::uuid,$11,$12)`, tenantID, questionID, versionID, status, draft.Rubric.MaxScore, points, deductions, examples, userID, importID, draft.RubricCandidateID, refs)
	return err
}

type paperImportScanner interface{ Scan(...any) error }

func scanPaperImport(row paperImportScanner) (PaperImportJob, error) {
	var out PaperImportJob
	var questions, issues []byte
	var applied sql.NullTime
	err := row.Scan(&out.ID, &out.TenantID, &out.ExamID, &out.ExamPaperID, &out.PaperFileAssetID, &out.AnswerFileAssetID, &out.Status, &out.Subject, &questions, &issues, &out.ErrorCode, &out.CreatedBy, &out.CreatedAt, &out.UpdatedAt, &applied)
	if errors.Is(err, sql.ErrNoRows) {
		return PaperImportJob{}, ErrNotFound
	}
	if err != nil {
		return PaperImportJob{}, err
	}
	if len(questions) > 0 {
		_ = json.Unmarshal(questions, &out.Questions)
	}
	if out.Questions == nil {
		out.Questions = []PaperImportDraftQuestion{}
	}
	if len(issues) > 0 {
		_ = json.Unmarshal(issues, &out.Issues)
	}
	if out.Issues == nil {
		out.Issues = []string{}
	}
	if applied.Valid {
		out.AppliedAt = &applied.Time
	}
	return out, nil
}

func (s *PostgresStore) CreateQuestion(ctx context.Context, tenantID string, examID string, userID string, input CreateQuestionInput) (Question, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Question{}, err
	}
	defer tx.Rollback()
	if err := ensureExamPaperMutableTx(ctx, tx, tenantID, examID); err != nil {
		return Question{}, err
	}
	if err := ensurePaperBelongsToExam(ctx, tx, tenantID, examID, input.ExamPaperID); err != nil {
		return Question{}, err
	}
	kp, _ := json.Marshal(input.KnowledgePoints)
	area, _ := json.Marshal(input.AnswerArea)
	sortOrder := input.SortOrder
	if sortOrder == 0 {
		_ = tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(sort_order), 0) + 1 FROM question WHERE tenant_id = $1 AND exam_id = $2`, tenantID, examID).Scan(&sortOrder)
	}
	row := tx.QueryRowContext(ctx, `
INSERT INTO question (tenant_id, exam_id, exam_paper_id, question_no, question_type, score, stem, knowledge_points, answer_area, sort_order, status)
VALUES ($1, $2, NULLIF($3, '')::uuid, $4, $5, $6, $7, $8, $9, $10, 'active')
RETURNING id::text, tenant_id::text, exam_id::text, COALESCE(exam_paper_id::text, ''), question_no, question_type, score::float8, COALESCE(stem, ''), knowledge_points, answer_area, sort_order, status
`, tenantID, examID, input.ExamPaperID, input.QuestionNo, input.QuestionType, input.Score, input.Stem, kp, area, sortOrder)
	var out Question
	if err := scanQuestion(row, &out); err != nil {
		return Question{}, err
	}
	if input.AnswerKey != nil {
		key, err := s.insertAnswerKey(ctx, tx, tenantID, out.ID, userID, "v1", *input.AnswerKey)
		if err != nil {
			return Question{}, err
		}
		out.AnswerKey = &key
	}
	if err := tx.Commit(); err != nil {
		return Question{}, err
	}
	return out, nil
}

func (s *PostgresStore) ListQuestions(ctx context.Context, tenantID string, examID string) ([]Question, error) {
	return listQuestions(ctx, s.db, tenantID, examID)
}

func listQuestions(ctx context.Context, queryer postgresQueryer, tenantID string, examID string) ([]Question, error) {
	rows, err := queryer.QueryContext(ctx, `
SELECT id::text, tenant_id::text, exam_id::text, COALESCE(exam_paper_id::text, ''), question_no, question_type, score::float8, COALESCE(stem, ''), knowledge_points, answer_area, sort_order, status
FROM question
WHERE tenant_id = $1 AND exam_id = $2 AND deleted_at IS NULL AND status <> 'deleted'
ORDER BY sort_order, question_no
`, tenantID, examID)
	if err != nil {
		return nil, err
	}
	out := []Question{}
	for rows.Next() {
		var item Question
		if err := scanQuestion(rows, &item); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	for index := range out {
		var bankContent []byte
		if err := queryer.QueryRowContext(ctx, `SELECT source_type,COALESCE(source_bank_item_id::text,''),COALESCE(source_bank_item_version_id::text,''),COALESCE(source_content_hash,''),bank_content FROM question WHERE tenant_id=$1 AND id=$2`, tenantID, out[index].ID).Scan(&out[index].SourceType, &out[index].SourceBankItemID, &out[index].SourceBankItemVersionID, &out[index].SourceContentHash, &bankContent); err != nil {
			return nil, err
		}
		if len(bankContent) > 0 {
			if err := json.Unmarshal(bankContent, &out[index].BankContent); err != nil {
				return nil, err
			}
		}
		if err := loadQuestionImportProvenance(ctx, queryer, tenantID, &out[index]); err != nil {
			return nil, err
		}
		if err := loadQuestionAssessmentArchetype(ctx, queryer, tenantID, &out[index]); err != nil {
			return nil, err
		}
		if key, ok, err := latestAnswerKey(ctx, queryer, tenantID, out[index].ID); err != nil {
			return nil, err
		} else if ok {
			out[index].AnswerKey = &key
		}
		if rubric, ok, err := latestRubric(ctx, queryer, tenantID, out[index].ID); err != nil {
			return nil, err
		} else if ok {
			out[index].Rubric = &rubric
		}
		if solution, ok, err := latestQuestionSolution(ctx, queryer, tenantID, out[index].ID); err != nil {
			return nil, err
		} else if ok {
			out[index].Solution = &solution
		}
	}
	return out, nil
}

func (s *PostgresStore) UpdateQuestion(ctx context.Context, tenantID string, id string, userID string, input UpdateQuestionInput) (Question, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Question{}, err
	}
	defer tx.Rollback()
	var current Question
	if err := scanQuestion(tx.QueryRowContext(ctx, `SELECT id::text,tenant_id::text,exam_id::text,COALESCE(exam_paper_id::text,''),question_no,question_type,score::float8,COALESCE(stem,''),knowledge_points,answer_area,sort_order,status FROM question WHERE tenant_id=$1 AND id=$2::uuid AND deleted_at IS NULL AND status<>'deleted' FOR UPDATE`, tenantID, id), &current); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Question{}, ErrNotFound
		}
		return Question{}, err
	}
	if err := ensureExamPaperMutableTx(ctx, tx, tenantID, current.ExamID); err != nil {
		return Question{}, err
	}
	merged := current
	if input.QuestionNo != nil {
		merged.QuestionNo = *input.QuestionNo
	}
	if input.QuestionType != nil {
		merged.QuestionType = *input.QuestionType
	}
	if input.Score != nil {
		merged.Score = *input.Score
	}
	if input.Stem != nil {
		merged.Stem = *input.Stem
	}
	if input.KnowledgePoints != nil {
		merged.KnowledgePoints = cloneStrings(*input.KnowledgePoints)
	}
	if input.AnswerArea != nil {
		merged.AnswerArea = cloneMap(*input.AnswerArea)
	}
	if input.SortOrder != nil {
		merged.SortOrder = *input.SortOrder
	}
	kp, _ := json.Marshal(merged.KnowledgePoints)
	area, _ := json.Marshal(merged.AnswerArea)
	row := tx.QueryRowContext(ctx, `
UPDATE question
SET question_no = $3, question_type = $4, score = $5, stem = $6, knowledge_points = $7, answer_area = $8, sort_order = $9, updated_at = now()
WHERE tenant_id = $1 AND id = $2 AND deleted_at IS NULL
RETURNING id::text, tenant_id::text, exam_id::text, COALESCE(exam_paper_id::text, ''), question_no, question_type, score::float8, COALESCE(stem, ''), knowledge_points, answer_area, sort_order, status
`, tenantID, id, merged.QuestionNo, merged.QuestionType, merged.Score, merged.Stem, kp, area, merged.SortOrder)
	var out Question
	if err := scanQuestion(row, &out); err != nil {
		return Question{}, err
	}
	if input.AnswerKey != nil {
		key, err := s.insertAnswerKey(ctx, tx, tenantID, out.ID, userID, "v2", *input.AnswerKey)
		if err != nil {
			return Question{}, err
		}
		out.AnswerKey = &key
	}
	if err := tx.Commit(); err != nil {
		return Question{}, err
	}
	return out, nil
}

func (s *PostgresStore) DeleteQuestion(ctx context.Context, tenantID string, id string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var examID string
	if err := tx.QueryRowContext(ctx, `SELECT exam_id::text FROM question WHERE tenant_id=$1 AND id=$2::uuid AND deleted_at IS NULL AND status<>'deleted' FOR UPDATE`, tenantID, id).Scan(&examID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	if err := ensureExamPaperMutableTx(ctx, tx, tenantID, examID); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `UPDATE question SET status = 'deleted', deleted_at = now(), updated_at = now() WHERE tenant_id = $1 AND id = $2 AND deleted_at IS NULL`, tenantID, id)
	if err != nil {
		return err
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		return ErrNotFound
	}
	return tx.Commit()
}

func (s *PostgresStore) CreateRubric(ctx context.Context, tenantID string, questionID string, userID string, input RubricInput) (Rubric, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Rubric{}, err
	}
	defer tx.Rollback()
	var question Question
	if err := scanQuestion(tx.QueryRowContext(ctx, `SELECT id::text,tenant_id::text,exam_id::text,COALESCE(exam_paper_id::text,''),question_no,question_type,score::float8,COALESCE(stem,''),knowledge_points,answer_area,sort_order,status FROM question WHERE tenant_id=$1 AND id=$2::uuid AND deleted_at IS NULL AND status<>'deleted' FOR UPDATE`, tenantID, questionID), &question); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Rubric{}, ErrNotFound
		}
		return Rubric{}, err
	}
	if err := ensureExamPaperMutableTx(ctx, tx, tenantID, question.ExamID); err != nil {
		return Rubric{}, err
	}
	var locked bool
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(BOOL_OR(status='locked'),false) FROM rubric_version WHERE tenant_id=$1 AND question_id=$2::uuid AND deleted_at IS NULL`, tenantID, questionID).Scan(&locked); err != nil {
		return Rubric{}, err
	}
	if locked {
		return Rubric{}, ErrRubricLocked
	}
	if !ValidRubricEvidenceRequirements(input.Points) {
		return Rubric{}, ErrInvalidInput
	}
	if !scoreEqual(SumRubricPoints(input.Points), question.Score) || !scoreEqual(input.MaxScore, question.Score) {
		return Rubric{}, ErrRubricMismatch
	}
	versionNo := 1
	_ = tx.QueryRowContext(ctx, `SELECT COUNT(*) + 1 FROM rubric_version WHERE tenant_id = $1 AND question_id = $2`, tenantID, questionID).Scan(&versionNo)
	version := fmt.Sprintf("v%d", versionNo)
	points, _ := json.Marshal(input.Points)
	deductions, _ := json.Marshal(input.Deductions)
	examples, _ := json.Marshal(input.Examples)
	status := input.Status
	if status == "" {
		status = "draft"
	}
	hash := contentHash(points, deductions, examples)
	var versionID string
	if err := tx.QueryRowContext(ctx, `
INSERT INTO rubric_version (tenant_id, question_id, version, status, content_hash, created_by)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING id::text
`, tenantID, questionID, version, status, hash, userID).Scan(&versionID); err != nil {
		return Rubric{}, err
	}
	row := tx.QueryRowContext(ctx, `
INSERT INTO question_rubric (tenant_id, question_id, rubric_version_id, status, max_score, points, deductions, examples, created_by)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
RETURNING id::text
`, tenantID, questionID, versionID, status, input.MaxScore, points, deductions, examples, userID)
	var id string
	if err := row.Scan(&id); err != nil {
		return Rubric{}, err
	}
	if err := tx.Commit(); err != nil {
		return Rubric{}, err
	}
	return Rubric{ID: id, QuestionID: questionID, Version: version, Status: status, MaxScore: input.MaxScore, Points: input.Points, Deductions: input.Deductions, Examples: input.Examples}, nil
}

func (s *PostgresStore) ValidateConfig(ctx context.Context, tenantID string, examID string) (ValidationResult, error) {
	result := ValidationResult{Valid: true, Issues: []ValidationIssue{}}
	var examTotal float64
	if err := s.db.QueryRowContext(ctx, `SELECT total_score::float8 FROM exam WHERE tenant_id = $1 AND id = $2 AND deleted_at IS NULL`, tenantID, examID).Scan(&examTotal); err != nil {
		return ValidationResult{}, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT question_no, question_type, score::float8, answer_area FROM question WHERE tenant_id = $1 AND exam_id = $2 AND deleted_at IS NULL AND status <> 'deleted'`, tenantID, examID)
	if err != nil {
		return ValidationResult{}, err
	}
	defer rows.Close()
	count := 0
	total := 0.0
	for rows.Next() {
		var no, kind string
		var score float64
		var area []byte
		if err := rows.Scan(&no, &kind, &score, &area); err != nil {
			return ValidationResult{}, err
		}
		count++
		total += score
		if no == "" || kind == "" {
			result.Issues = append(result.Issues, ValidationIssue{Code: "question_incomplete", Message: "题目缺少题号或题型"})
		}
		if len(area) == 0 || string(area) == "null" {
			result.Issues = append(result.Issues, ValidationIssue{Code: "answer_area_missing", Message: "第" + no + "题缺少答题区域"})
		}
	}
	if count == 0 {
		result.Issues = append(result.Issues, ValidationIssue{Code: "no_questions", Message: "试卷尚未配置题目"})
	}
	if !scoreEqual(total, examTotal) {
		result.Issues = append(result.Issues, ValidationIssue{Code: "total_score_mismatch", Message: fmt.Sprintf("题目总分 %.2f 分与考试总分 %.2f 分不一致", total, examTotal)})
	}
	result.Valid = len(result.Issues) == 0
	return result, rows.Err()
}

func (s *PostgresStore) insertAnswerKey(ctx context.Context, tx *sql.Tx, tenantID string, questionID string, userID string, version string, input AnswerKeyInput) (AnswerKey, error) {
	standard, _ := json.Marshal(input.StandardAnswer)
	equiv, _ := json.Marshal(input.EquivalentAnswers)
	tolerance, _ := json.Marshal(input.Tolerance)
	row := tx.QueryRowContext(ctx, `
INSERT INTO question_answer_key (tenant_id, question_id, answer_version, standard_answer, equivalent_answers, tolerance, created_by)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING id::text
`, tenantID, questionID, version, standard, equiv, tolerance, userID)
	var id string
	if err := row.Scan(&id); err != nil {
		return AnswerKey{}, err
	}
	return AnswerKey{ID: id, QuestionID: questionID, AnswerVersion: version, StandardAnswer: input.StandardAnswer, EquivalentAnswers: input.EquivalentAnswers, Tolerance: input.Tolerance}, nil
}

func ensurePaperBelongsToExam(ctx context.Context, tx *sql.Tx, tenantID string, examID string, paperID string) error {
	if paperID == "" {
		return nil
	}
	var exists int
	err := tx.QueryRowContext(ctx, `
SELECT 1
FROM exam_paper
WHERE tenant_id = $1 AND exam_id = $2 AND id::text = $3 AND deleted_at IS NULL
`, tenantID, examID, paperID).Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	return err
}

func ensureExamPaperMutableTx(ctx context.Context, tx *sql.Tx, tenantID, examID string) error {
	var status string
	if err := tx.QueryRowContext(ctx, `SELECT status FROM exam WHERE tenant_id=$1 AND id=$2::uuid AND deleted_at IS NULL FOR UPDATE`, tenantID, examID).Scan(&status); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	if status != "draft" && status != "configured" {
		return ErrExamFrozen
	}
	return nil
}

func latestRubric(ctx context.Context, queryer postgresQueryer, tenantID string, questionID string) (Rubric, bool, error) {
	row := queryer.QueryRowContext(ctx, `
SELECT qr.id::text, qr.question_id::text, rv.version, qr.status, qr.max_score::float8, qr.points, qr.deductions, qr.examples,
       COALESCE(qr.paper_import_id::text,''),COALESCE(qr.paper_import_candidate_id,''),qr.paper_import_source_refs
FROM question_rubric qr
JOIN rubric_version rv ON rv.tenant_id = qr.tenant_id AND rv.id = qr.rubric_version_id
WHERE qr.tenant_id = $1 AND qr.question_id = $2 AND qr.deleted_at IS NULL
ORDER BY qr.created_at DESC
LIMIT 1
`, tenantID, questionID)
	var out Rubric
	var points, deductions, examples, refs []byte
	if err := row.Scan(&out.ID, &out.QuestionID, &out.Version, &out.Status, &out.MaxScore, &points, &deductions, &examples, &out.PaperImportID, &out.PaperImportCandidateID, &refs); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Rubric{}, false, nil
		}
		return Rubric{}, false, err
	}
	_ = json.Unmarshal(points, &out.Points)
	_ = json.Unmarshal(deductions, &out.Deductions)
	_ = json.Unmarshal(examples, &out.Examples)
	_ = json.Unmarshal(refs, &out.PaperImportSourceRefs)
	return out, true, nil
}

func latestAnswerKey(ctx context.Context, queryer postgresQueryer, tenantID string, questionID string) (AnswerKey, bool, error) {
	row := queryer.QueryRowContext(ctx, `
SELECT id::text, question_id::text, answer_version, standard_answer, equivalent_answers, tolerance,
       COALESCE(paper_import_id::text,''),COALESCE(paper_import_candidate_id,''),paper_import_source_refs
FROM question_answer_key
WHERE tenant_id = $1 AND question_id = $2 AND deleted_at IS NULL
ORDER BY created_at DESC
LIMIT 1
`, tenantID, questionID)
	var out AnswerKey
	var standard, equivalent, tolerance, refs []byte
	if err := row.Scan(&out.ID, &out.QuestionID, &out.AnswerVersion, &standard, &equivalent, &tolerance, &out.PaperImportID, &out.PaperImportCandidateID, &refs); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return AnswerKey{}, false, nil
		}
		return AnswerKey{}, false, err
	}
	_ = json.Unmarshal(standard, &out.StandardAnswer)
	_ = json.Unmarshal(equivalent, &out.EquivalentAnswers)
	_ = json.Unmarshal(tolerance, &out.Tolerance)
	_ = json.Unmarshal(refs, &out.PaperImportSourceRefs)
	return out, true, nil
}

func loadQuestionImportProvenance(ctx context.Context, queryer postgresQueryer, tenantID string, question *Question) error {
	var refs []byte
	if err := queryer.QueryRowContext(ctx, `SELECT COALESCE(paper_import_id::text,''),COALESCE(paper_import_candidate_id,''),paper_import_source_refs FROM question WHERE tenant_id=$1 AND id=$2::uuid`, tenantID, question.ID).Scan(&question.PaperImportID, &question.PaperImportCandidateID, &refs); err != nil {
		return err
	}
	_ = json.Unmarshal(refs, &question.PaperImportSourceRefs)
	return nil
}

func loadQuestionAssessmentArchetype(ctx context.Context, queryer postgresQueryer, tenantID string, question *Question) error {
	return queryer.QueryRowContext(ctx, `SELECT COALESCE((SELECT archetype_code FROM question_assessment_config WHERE tenant_id=$1 AND question_id=$2::uuid),(SELECT assessment_archetype FROM question WHERE tenant_id=$1 AND id=$2::uuid),'')`, tenantID, question.ID).Scan(&question.AssessmentArchetype)
}

func latestQuestionSolution(ctx context.Context, queryer postgresQueryer, tenantID string, questionID string) (QuestionSolution, bool, error) {
	row := queryer.QueryRowContext(ctx, `
SELECT id::text,question_id::text,COALESCE(paper_import_id::text,''),solution_version,raw_text,steps,source_refs,verification_status
FROM question_solution
WHERE tenant_id=$1 AND question_id=$2::uuid AND deleted_at IS NULL
ORDER BY created_at DESC,id DESC
LIMIT 1
`, tenantID, questionID)
	var out QuestionSolution
	var steps, refs []byte
	if err := row.Scan(&out.ID, &out.QuestionID, &out.PaperImportID, &out.SolutionVersion, &out.RawText, &steps, &refs, &out.VerificationStatus); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return QuestionSolution{}, false, nil
		}
		return QuestionSolution{}, false, err
	}
	_ = json.Unmarshal(steps, &out.Steps)
	_ = json.Unmarshal(refs, &out.SourceRefs)
	return out, true, nil
}

type questionScanner interface {
	Scan(dest ...any) error
}

func scanQuestion(row questionScanner, out *Question) error {
	var kp, area []byte
	if err := row.Scan(&out.ID, &out.TenantID, &out.ExamID, &out.ExamPaperID, &out.QuestionNo, &out.QuestionType, &out.Score, &out.Stem, &kp, &area, &out.SortOrder, &out.Status); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	_ = json.Unmarshal(kp, &out.KnowledgePoints)
	if len(area) > 0 {
		_ = json.Unmarshal(area, &out.AnswerArea)
	}
	return nil
}

func contentHash(parts ...[]byte) string {
	hash := sha256.New()
	for _, part := range parts {
		_, _ = hash.Write(part)
	}
	return hex.EncodeToString(hash.Sum(nil))
}
