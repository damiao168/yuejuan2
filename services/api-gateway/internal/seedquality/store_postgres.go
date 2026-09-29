package seedquality

import (
	"context"
	"database/sql"
	"edugrade-enterprise/services/api-gateway/internal/commandreceipt"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

type PostgresStore struct{ db *sql.DB }

func NewPostgresStore(db *sql.DB) *PostgresStore { return &PostgresStore{db: db} }

func (s *PostgresStore) PutPolicy(ctx context.Context, tenantID, examID, questionID, actorID string, input PutPolicyInput, fingerprint string) (Policy, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Policy{}, err
	}
	defer tx.Rollback()
	current, err := scanPolicy(tx.QueryRowContext(ctx, policySelect+`
WHERE tenant_id=$1::uuid AND exam_id=$2::uuid AND question_id=$3::uuid
FOR UPDATE`, tenantID, examID, questionID))
	switch {
	case errors.Is(err, ErrNotFound) && input.ExpectedRevision != 0:
		return Policy{}, ErrConflict
	case err == nil && current.Revision != input.ExpectedRevision:
		return Policy{}, ErrConflict
	case err != nil && !errors.Is(err, ErrNotFound):
		return Policy{}, err
	}
	var policy Policy
	if errors.Is(err, ErrNotFound) {
		policy, err = scanPolicy(tx.QueryRowContext(ctx, `
INSERT INTO seed_sampling_policy
  (tenant_id,exam_id,question_id,rate,min_interval,max_interval,active_gold_fingerprint,status,created_by)
VALUES ($1::uuid,$2::uuid,$3::uuid,$4,$5,$6,$7,$8,$9::uuid)
RETURNING id::text,exam_id::text,question_id::text,rate::float8,min_interval,max_interval,
 active_gold_fingerprint,status,revision,created_by::text,created_at,updated_at`,
			tenantID, examID, questionID, input.Rate, input.MinInterval, input.MaxInterval, fingerprint, input.Status, actorID))
	} else {
		policy, err = scanPolicy(tx.QueryRowContext(ctx, `
UPDATE seed_sampling_policy
SET rate=$4,min_interval=$5,max_interval=$6,active_gold_fingerprint=$7,status=$8,
 revision=revision+1,updated_at=now()
WHERE tenant_id=$1::uuid AND exam_id=$2::uuid AND question_id=$3::uuid AND revision=$9
RETURNING id::text,exam_id::text,question_id::text,rate::float8,min_interval,max_interval,
 active_gold_fingerprint,status,revision,created_by::text,created_at,updated_at`,
			tenantID, examID, questionID, input.Rate, input.MinInterval, input.MaxInterval, fingerprint, input.Status, input.ExpectedRevision))
	}
	if err != nil {
		return Policy{}, err
	}
	if err := tx.Commit(); err != nil {
		return Policy{}, err
	}
	return policy, nil
}

func (s *PostgresStore) GetPolicy(ctx context.Context, tenantID, examID, questionID string) (Policy, error) {
	return scanPolicy(s.db.QueryRowContext(ctx, policySelect+`
WHERE tenant_id=$1::uuid AND exam_id=$2::uuid AND question_id=$3::uuid`, tenantID, examID, questionID))
}

// 策略、阅卷人游标和待处理任务在同一事务内检查和更新，确保并发请求最多创建一个 Seed 任务。
func (s *PostgresStore) AdvanceAndMaybeCreate(ctx context.Context, tenantID string, decision IssueDecision) (Task, bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Task{}, false, err
	}
	defer tx.Rollback()
	var revision int64
	var status PolicyStatus
	err = tx.QueryRowContext(ctx, `SELECT revision,status FROM seed_sampling_policy
WHERE tenant_id=$1::uuid AND id=$2::uuid FOR UPDATE`, tenantID, decision.Policy.ID).Scan(&revision, &status)
	if errors.Is(err, sql.ErrNoRows) {
		return Task{}, false, ErrNotFound
	}
	if err != nil {
		return Task{}, false, err
	}
	if revision != decision.Policy.Revision || status != PolicyActive {
		return Task{}, false, ErrConflict
	}

	open, err := scanTask(tx.QueryRowContext(ctx, seedTaskSelect+`
WHERE tenant_id=$1::uuid AND exam_id=$2::uuid AND question_id=$3::uuid
 AND grader_id=$4::uuid AND status='in_progress'`, tenantID, decision.Policy.ExamID, decision.Policy.QuestionID, decision.GraderID))
	if err == nil {
		if err := tx.Commit(); err != nil {
			return Task{}, false, err
		}
		return open, true, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return Task{}, false, err
	}

	_, err = tx.ExecContext(ctx, `INSERT INTO seed_sampling_cursor
 (tenant_id,exam_id,question_id,grader_id,claims_since_seed,force_at_interval)
VALUES ($1::uuid,$2::uuid,$3::uuid,$4::uuid,0,$5)
ON CONFLICT (tenant_id,exam_id,question_id,grader_id) DO NOTHING`,
		tenantID, decision.Policy.ExamID, decision.Policy.QuestionID, decision.GraderID, decision.NextInterval)
	if err != nil {
		return Task{}, false, err
	}
	var claims, forceAt int
	err = tx.QueryRowContext(ctx, `SELECT claims_since_seed,force_at_interval FROM seed_sampling_cursor
WHERE tenant_id=$1::uuid AND exam_id=$2::uuid AND question_id=$3::uuid AND grader_id=$4::uuid
FOR UPDATE`, tenantID, decision.Policy.ExamID, decision.Policy.QuestionID, decision.GraderID).Scan(&claims, &forceAt)
	if err != nil {
		return Task{}, false, err
	}
	claims++
	due := claims >= forceAt || claims >= decision.Policy.MinInterval && decision.Probability < decision.Policy.Rate
	if !due {
		_, err = tx.ExecContext(ctx, `UPDATE seed_sampling_cursor SET claims_since_seed=$5,updated_at=now()
WHERE tenant_id=$1::uuid AND exam_id=$2::uuid AND question_id=$3::uuid AND grader_id=$4::uuid`,
			tenantID, decision.Policy.ExamID, decision.Policy.QuestionID, decision.GraderID, claims)
		if err != nil {
			return Task{}, false, err
		}
		if err := tx.Commit(); err != nil {
			return Task{}, false, err
		}
		return Task{}, false, nil
	}
	_, err = tx.ExecContext(ctx, `UPDATE seed_sampling_cursor
SET claims_since_seed=0,force_at_interval=$5,updated_at=now()
WHERE tenant_id=$1::uuid AND exam_id=$2::uuid AND question_id=$3::uuid AND grader_id=$4::uuid`,
		tenantID, decision.Policy.ExamID, decision.Policy.QuestionID, decision.GraderID, decision.NextInterval)
	if err != nil {
		return Task{}, false, err
	}
	expected, _ := json.Marshal(cloneObject(decision.Gold.ExpectedCriteria))
	now := decision.Now.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	var id string
	err = tx.QueryRowContext(ctx, `
INSERT INTO seed_task
 (tenant_id,exam_id,question_id,question_no,grader_id,gold_paper_id,gold_version,
  exam_question_snapshot_id,anonymous_code,source_image_url,reference_score,max_score,
  archetype_code,expected_criteria_json,created_at,updated_at)
VALUES ($1::uuid,$2::uuid,$3::uuid,$4,$5::uuid,$6::uuid,$7,$8::uuid,'pending',$9,$10,$11,$12,$13,$14,$14)
RETURNING id::text`, tenantID, decision.Policy.ExamID, decision.Policy.QuestionID, decision.QuestionNo,
		decision.GraderID, decision.Gold.GoldPaperID, decision.Gold.GoldVersion, decision.Gold.SnapshotID,
		decision.Gold.AnswerImageURL, decision.Gold.ReferenceScore, decision.Gold.MaxScore,
		decision.Gold.ArchetypeCode, expected, now).Scan(&id)
	if err != nil {
		return Task{}, false, err
	}
	_, err = tx.ExecContext(ctx, `UPDATE seed_task SET anonymous_code=$3 WHERE tenant_id=$1::uuid AND id=$2::uuid`, tenantID, id, anonymousCode(id))
	if err != nil {
		return Task{}, false, err
	}
	task, err := scanTask(tx.QueryRowContext(ctx, seedTaskSelect+` WHERE tenant_id=$1::uuid AND id=$2::uuid`, tenantID, id))
	if err != nil {
		return Task{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return Task{}, false, err
	}
	return task, true, nil
}

func (s *PostgresStore) GetTask(ctx context.Context, tenantID, id string) (Task, error) {
	return scanTask(s.db.QueryRowContext(ctx, seedTaskSelect+` WHERE tenant_id=$1::uuid AND id=$2::uuid`, tenantID, id))
}

// 提交事务先处理命令幂等记录，再锁定任务并写观察结果，网络重试不会重复计入质量样本。
func (s *PostgresStore) CompleteTask(ctx context.Context, tenantID, taskID, graderID string, input SubmitInput, observation Observation) (Task, Observation, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Task{}, Observation{}, err
	}
	defer tx.Rollback()
	var replay commandResult
	if found, err := commandreceipt.Load(ctx, tx, tenantID, graderID, "review.submit", taskID, input, &replay); err != nil || found {
		return Task{ID: replay.Task.ID, Status: replay.Task.Status, Revision: replay.Task.Revision}, Observation{}, err
	}
	task, err := scanTask(tx.QueryRowContext(ctx, seedTaskSelect+` WHERE tenant_id=$1::uuid AND id=$2::uuid FOR UPDATE`, tenantID, taskID))
	if err != nil {
		return Task{}, Observation{}, err
	}
	if task.AssignedTo != graderID {
		return Task{}, Observation{}, ErrSeedTaskForbidden
	}
	if task.Status != "in_progress" || task.Revision != input.ExpectedRevision {
		return Task{}, Observation{}, ErrConflict
	}
	selections, _ := json.Marshal(cloneObject(observation.RubricSelections))
	traits, criteria := nullableJSON(observation.TraitObservation), nullableJSON(observation.CriterionObservation)
	var agreement any
	if observation.RubricAgreement != nil {
		agreement = *observation.RubricAgreement
	}
	observation, err = scanObservation(tx.QueryRowContext(ctx, `
INSERT INTO seed_observation
 (tenant_id,seed_task_id,exam_id,question_id,grader_id,gold_paper_id,gold_version,
  submitted_score,reference_score,rubric_selection_json,error,rubric_agreement,
  observation_kind,trait_observation_json,criterion_observation_json,observed_at)
VALUES ($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5::uuid,$6::uuid,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)
RETURNING id::text,exam_id::text,question_id::text,grader_id::text,gold_paper_id::text,gold_version,
 submitted_score::float8,reference_score::float8,rubric_selection_json,error::float8,
 rubric_agreement::float8,observation_kind,trait_observation_json,criterion_observation_json,observed_at`,
		tenantID, taskID, task.ExamID, task.QuestionID, graderID, task.GoldPaperID, task.GoldVersion,
		observation.SubmittedScore, observation.ReferenceScore, selections, observation.AbsoluteError,
		agreement, observation.ObservationKind, traits, criteria, observation.ObservedAt))
	if err != nil {
		return Task{}, Observation{}, err
	}
	observation.MaxScore = task.MaxScore
	task, err = scanTask(tx.QueryRowContext(ctx, `UPDATE seed_task
SET status='completed',revision=revision+1,updated_at=$4
WHERE tenant_id=$1::uuid AND id=$2::uuid AND grader_id=$3::uuid
RETURNING `+seedTaskColumns, tenantID, taskID, graderID, observation.ObservedAt))
	if err != nil {
		return Task{}, Observation{}, err
	}
	if err := commandreceipt.Save(ctx, tx, tenantID, graderID, "review.submit", taskID, input, seedCommandResult(task)); err != nil {
		return Task{}, Observation{}, err
	}
	if err := tx.Commit(); err != nil {
		return Task{}, Observation{}, err
	}
	return task, observation, nil
}

func (s *PostgresStore) ListObservations(ctx context.Context, tenantID string, filter ObservationFilter) ([]Observation, error) {
	query := `SELECT o.id::text,o.exam_id::text,o.question_id::text,o.grader_id::text,o.gold_paper_id::text,o.gold_version,
 o.submitted_score::float8,o.reference_score::float8,o.rubric_selection_json,o.error::float8,
 o.rubric_agreement::float8,o.observation_kind,o.trait_observation_json,o.criterion_observation_json,o.observed_at,
 t.max_score::float8
FROM seed_observation o
JOIN seed_task t ON t.tenant_id=o.tenant_id AND t.id=o.seed_task_id
WHERE o.tenant_id=$1::uuid
 AND ($2='' OR o.exam_id::text=$2) AND ($3='' OR o.question_id::text=$3) AND ($4='' OR o.grader_id::text=$4)
ORDER BY o.observed_at DESC,o.id DESC LIMIT $5`
	rows, err := s.db.QueryContext(ctx, query, tenantID, filter.ExamID, filter.QuestionID, filter.GraderID, filter.Limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Observation{}
	for rows.Next() {
		item, err := scanObservationWithMaxScore(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

const policySelect = `SELECT id::text,exam_id::text,question_id::text,rate::float8,min_interval,max_interval,
 active_gold_fingerprint,status,revision,created_by::text,created_at,updated_at FROM seed_sampling_policy `

const seedTaskColumns = `id::text,exam_id::text,question_id::text,question_no,anonymous_code,source_image_url,
 status,grader_id::text,max_score::float8,revision,created_at,updated_at,gold_paper_id::text,gold_version,
 exam_question_snapshot_id::text,reference_score::float8,expected_criteria_json,archetype_code`
const seedTaskSelect = `SELECT ` + seedTaskColumns + ` FROM seed_task `

type scanner interface{ Scan(...any) error }

func scanPolicy(row scanner) (Policy, error) {
	var out Policy
	if err := row.Scan(&out.ID, &out.ExamID, &out.QuestionID, &out.Rate, &out.MinInterval, &out.MaxInterval,
		&out.ActiveGoldFingerprint, &out.Status, &out.Revision, &out.CreatedBy, &out.CreatedAt, &out.UpdatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Policy{}, ErrNotFound
		}
		return Policy{}, err
	}
	out.CreatedAt, out.UpdatedAt = out.CreatedAt.UTC(), out.UpdatedAt.UTC()
	return out, nil
}

func scanTask(row scanner) (Task, error) {
	var out Task
	var expected []byte
	if err := row.Scan(&out.ID, &out.ExamID, &out.QuestionID, &out.QuestionNo, &out.AnonymousCode, &out.SourceImageURL,
		&out.Status, &out.AssignedTo, &out.MaxScore, &out.Revision, &out.CreatedAt, &out.UpdatedAt, &out.GoldPaperID,
		&out.GoldVersion, &out.SnapshotID, &out.ReferenceScore, &expected, &out.ArchetypeCode); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Task{}, ErrNotFound
		}
		return Task{}, err
	}
	if err := json.Unmarshal(expected, &out.ExpectedCriteria); err != nil {
		return Task{}, fmt.Errorf("decode seed expected criteria: %w", err)
	}
	out.Source, out.Priority = "manual", 0
	out.AnswerImageURL = "/api/v1/review-tasks/" + out.ID + "/segment-image"
	out.CreatedAt, out.UpdatedAt = out.CreatedAt.UTC(), out.UpdatedAt.UTC()
	return out, nil
}

func scanObservation(row scanner) (Observation, error) {
	var out Observation
	var selections []byte
	var agreement sql.NullFloat64
	var traits, criteria []byte
	if err := row.Scan(&out.ID, &out.ExamID, &out.QuestionID, &out.GraderID, &out.GoldPaperID, &out.GoldVersion,
		&out.SubmittedScore, &out.ReferenceScore, &selections, &out.AbsoluteError, &agreement, &out.ObservationKind,
		&traits, &criteria, &out.ObservedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Observation{}, ErrNotFound
		}
		return Observation{}, err
	}
	if err := json.Unmarshal(selections, &out.RubricSelections); err != nil {
		return Observation{}, err
	}
	if len(traits) > 0 {
		if err := json.Unmarshal(traits, &out.TraitObservation); err != nil {
			return Observation{}, err
		}
	}
	if len(criteria) > 0 {
		if err := json.Unmarshal(criteria, &out.CriterionObservation); err != nil {
			return Observation{}, err
		}
	}
	if agreement.Valid {
		value := agreement.Float64
		out.RubricAgreement = &value
	}
	out.ObservedAt = out.ObservedAt.UTC()
	return out, nil
}

func scanObservationWithMaxScore(row scanner) (Observation, error) {
	var out Observation
	var selections []byte
	var agreement sql.NullFloat64
	var traits, criteria []byte
	if err := row.Scan(&out.ID, &out.ExamID, &out.QuestionID, &out.GraderID, &out.GoldPaperID, &out.GoldVersion,
		&out.SubmittedScore, &out.ReferenceScore, &selections, &out.AbsoluteError, &agreement, &out.ObservationKind,
		&traits, &criteria, &out.ObservedAt, &out.MaxScore); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Observation{}, ErrNotFound
		}
		return Observation{}, err
	}
	if err := json.Unmarshal(selections, &out.RubricSelections); err != nil {
		return Observation{}, err
	}
	if len(traits) > 0 {
		if err := json.Unmarshal(traits, &out.TraitObservation); err != nil {
			return Observation{}, err
		}
	}
	if len(criteria) > 0 {
		if err := json.Unmarshal(criteria, &out.CriterionObservation); err != nil {
			return Observation{}, err
		}
	}
	if agreement.Valid {
		value := agreement.Float64
		out.RubricAgreement = &value
	}
	out.ObservedAt = out.ObservedAt.UTC()
	return out, nil
}

func nullableJSON(value map[string]any) any {
	if value == nil {
		return nil
	}
	payload, _ := json.Marshal(value)
	return payload
}
