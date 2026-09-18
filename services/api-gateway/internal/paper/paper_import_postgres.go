package paper

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"strings"
)

func (s *PostgresStore) CreatePaperImport(ctx context.Context, tenantID, examID, userID string, input CreatePaperImportInput) (PaperImportJob, error) {
	if input.CommandID == "" {
		input.CommandID = uuid.NewString()
	}
	sources := normalizePaperImportSourceInputs(input)
	if strings.TrimSpace(input.Subject) == "" || validateCreatePaperImportSources(sources) != nil {
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
	canonicalSubject, subjectErr := normalizePaperImportSubject(authoritativeSubject, input.Subject)
	if subjectErr != nil {
		return PaperImportJob{}, ErrInvalidInput
	}
	// The browser value is only a consistency assertion. Persist the canonical
	// subject read from the exam record so a forged/stale request cannot route OCR.
	input.Subject = canonicalSubject
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
	for _, source := range sources {
		var valid bool
		if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM file_asset WHERE tenant_id=$1 AND id=$2::uuid AND deleted_at IS NULL AND (exam_id IS NULL OR exam_id=$3::uuid))`, tenantID, source.FileAssetID, examID).Scan(&valid); err != nil {
			return PaperImportJob{}, err
		}
		if !valid {
			return PaperImportJob{}, ErrInvalidInput
		}
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
	if err = validateReplacePaperImportSources(input.Sources, active); err != nil {
		return PaperImportJob{}, err
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
