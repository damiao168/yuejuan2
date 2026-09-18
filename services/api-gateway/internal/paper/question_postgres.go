package paper

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

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
