package regrade

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

type PostgresStore struct{ db *sql.DB }

func NewPostgresStore(db *sql.DB) *PostgresStore { return &PostgresStore{db: db} }

func (s *PostgresStore) Create(ctx context.Context, tenantID, examID, questionID, actorID string, input CreateInput, sources []SourceItem) (Job, []Item, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Job{}, nil, err
	}
	defer tx.Rollback()
	// 按租户和考试串行创建任务，让幂等键查询和任务写入处于同一临界区。
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1), hashtext($2))`, tenantID, examID); err != nil {
		return Job{}, nil, err
	}
	if existing, found, err := s.idempotentTx(ctx, tx, tenantID, examID, input.IdempotencyKey); err != nil {
		return Job{}, nil, err
	} else if found {
		items, err := s.itemsTx(ctx, tx, tenantID, existing.ID)
		if err != nil {
			return Job{}, nil, err
		}
		return existing, items, nil
	}
	if input.AssigneeID != "" {
		if err := validateAssigneeTx(ctx, tx, tenantID, examID, input.AssigneeID); err != nil {
			return Job{}, nil, err
		}
	}
	// 重评必须指向一个不可变评估快照。操作人员留空时，在创建时解析最新快照；
	// 提供 ID 时则校验其确实属于该考试和题目，防止后续配置编辑静默改变进行中的修正。
	if err := tx.QueryRowContext(ctx, `
SELECT id::text
FROM exam_question_snapshot
WHERE tenant_id=$1::uuid AND exam_id=$2::uuid AND question_id=$3::uuid
  AND ($4='' OR id=$4::uuid)
ORDER BY snapshot_version DESC
LIMIT 1
`, tenantID, examID, questionID, input.NewRubricSnapshotID).Scan(&input.NewRubricSnapshotID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Job{}, nil, ErrInvalidInput
		}
		return Job{}, nil, err
	}
	// 发布不可变。在此事务内重新读取来源，使持久化条目群体绝不基于当前成绩事实。
	fresh, err := sourceItemsQuery(ctx, tx, tenantID, examID, questionID, input.SourceReleaseID, input.Selector)
	if err != nil {
		return Job{}, nil, err
	}
	if len(fresh) == 0 {
		return Job{}, nil, ErrNoAffectedItems
	}
	_ = sources // 服务预览输入不作为权威依据。
	selectorJSON, err := json.Marshal(input.Selector)
	if err != nil {
		return Job{}, nil, err
	}
	job, err := scanJob(tx.QueryRowContext(ctx, `
INSERT INTO regrade_job (tenant_id,exam_id,question_id,source_release_id,reason_code,reason_text,strategy,selector_json,new_rubric_snapshot_id,new_policy_version,severity_delta,idempotency_key,status,affected_count,created_by)
VALUES ($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5,$6,$7,$8::jsonb,NULLIF($9,'')::uuid,NULLIF($10,''),$11,$12,'awaiting_approval',$13,$14::uuid)
RETURNING `+jobReturning, tenantID, examID, questionID, input.SourceReleaseID, input.ReasonCode, input.ReasonText, input.Strategy, string(selectorJSON), input.NewRubricSnapshotID, input.NewPolicyVersion, *input.SeverityDelta, input.IdempotencyKey, len(fresh), actorID))
	if err != nil {
		return Job{}, nil, err
	}
	items := make([]Item, 0, len(fresh))
	for _, source := range fresh {
		item, err := scanItem(tx.QueryRowContext(ctx, `
INSERT INTO regrade_item (tenant_id,job_id,submission_id,old_final_grade_id,old_score,max_score,status,assigned_to)
VALUES ($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5,$6,'pending',NULLIF($7,'')::uuid)
RETURNING `+itemReturning, tenantID, job.ID, source.SubmissionID, source.OldFinalGradeID, source.OldScore, source.MaxScore, input.AssigneeID))
		if err != nil {
			return Job{}, nil, err
		}
		items = append(items, item)
	}
	if err := addEventTx(ctx, tx, tenantID, Event{JobID: job.ID, Type: "created", ActorID: actorID, Payload: map[string]any{"affected_count": len(items), "source_release_id": job.SourceReleaseID, "strategy": job.Strategy}}); err != nil {
		return Job{}, nil, err
	}
	if err := tx.Commit(); err != nil {
		return Job{}, nil, err
	}
	return job, items, nil
}

const jobColumns = `id::text,tenant_id::text,exam_id::text,question_id::text,source_release_id::text,reason_code,reason_text,strategy,selector_json,COALESCE(new_rubric_snapshot_id::text,''),COALESCE(new_policy_version,''),severity_delta::float8,idempotency_key,status,affected_count,created_by::text,COALESCE(approved_by::text,''),approved_at,COALESCE(finalized_by::text,''),finalized_at,created_at,updated_at`
const jobReturning = jobColumns
const itemColumns = `id::text,job_id::text,submission_id::text,old_final_grade_id::text,old_score::float8,max_score::float8,COALESCE(candidate_grade_id::text,''),candidate_score::float8,candidate_rubric_selections,candidate_comment,COALESCE(reviewed_grade_id::text,''),reviewed_score::float8,delta::float8,status,COALESCE(assigned_to::text,''),COALESCE(claimed_by::text,''),COALESCE(reviewed_by::text,''),review_note,revision,created_at,updated_at`
const itemReturning = itemColumns

type scanner interface{ Scan(...any) error }

func scanJob(row scanner) (Job, error) {
	var job Job
	var selector []byte
	var approved, finalized sql.NullTime
	if err := row.Scan(&job.ID, &job.TenantID, &job.ExamID, &job.QuestionID, &job.SourceReleaseID, &job.ReasonCode, &job.ReasonText, &job.Strategy, &selector, &job.NewRubricSnapshotID, &job.NewPolicyVersion, &job.SeverityDelta, &job.IdempotencyKey, &job.Status, &job.AffectedCount, &job.CreatedBy, &job.ApprovedBy, &approved, &job.FinalizedBy, &finalized, &job.CreatedAt, &job.UpdatedAt); err != nil {
		return Job{}, err
	}
	if err := json.Unmarshal(selector, &job.Selector); err != nil {
		return Job{}, err
	}
	if approved.Valid {
		value := approved.Time.UTC()
		job.ApprovedAt = &value
	}
	if finalized.Valid {
		value := finalized.Time.UTC()
		job.FinalizedAt = &value
	}
	job.CreatedAt, job.UpdatedAt = job.CreatedAt.UTC(), job.UpdatedAt.UTC()
	return job, nil
}
func scanItem(row scanner) (Item, error) {
	var item Item
	var candidate, reviewed, delta sql.NullFloat64
	var selectionsJSON []byte
	if err := row.Scan(&item.ID, &item.JobID, &item.SubmissionID, &item.OldFinalGradeID, &item.OldScore, &item.MaxScore, &item.CandidateGradeID, &candidate, &selectionsJSON, &item.CandidateComment, &item.ReviewedGradeID, &reviewed, &delta, &item.Status, &item.AssignedTo, &item.ClaimedBy, &item.ReviewedBy, &item.ReviewNote, &item.Revision, &item.CreatedAt, &item.UpdatedAt); err != nil {
		return Item{}, err
	}
	if candidate.Valid {
		value := candidate.Float64
		item.CandidateScore = &value
	}
	if err := json.Unmarshal(selectionsJSON, &item.CandidateRubricSelections); err != nil {
		return Item{}, err
	}
	if reviewed.Valid {
		value := reviewed.Float64
		item.ReviewedScore = &value
	}
	if delta.Valid {
		value := delta.Float64
		item.Delta = &value
	}
	item.CreatedAt, item.UpdatedAt = item.CreatedAt.UTC(), item.UpdatedAt.UTC()
	return item, nil
}

// A18 接入 ReleasePlan 消费方时，编译期结构检查可减少误替换为当前成绩写入器的风险。
var _ Store = (*PostgresStore)(nil)
