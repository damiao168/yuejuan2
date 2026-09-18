package paper

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

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
