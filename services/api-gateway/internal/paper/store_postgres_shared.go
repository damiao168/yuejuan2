package paper

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
)

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
