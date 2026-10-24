package reviewbookmark

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/paper"
	"edugrade-enterprise/services/api-gateway/internal/review"
)

type PostgresSourceProvider struct{ db *sql.DB }

func NewPostgresSourceProvider(db *sql.DB) *PostgresSourceProvider {
	return &PostgresSourceProvider{db: db}
}

type sourceQueryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// Source 和内容在同一个 RR 快照读取；不会把后续终分或其他教师意见加入回看。
func (p *PostgresSourceProvider) Resolve(ctx context.Context, user auth.User, taskID string) (Source, error) {
	if err := requireWork(user); err != nil {
		return Source{}, err
	}
	tx, err := p.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return Source{}, err
	}
	defer tx.Rollback()
	source, err := p.resolve(ctx, tx, user, taskID, false)
	if err != nil {
		return Source{}, err
	}
	if err = tx.Commit(); err != nil {
		return Source{}, err
	}
	return source, nil
}
func (p *PostgresSourceProvider) ResolveTx(ctx context.Context, tx *sql.Tx, user auth.User, taskID string, lock bool) (Source, error) {
	if err := requireWork(user); err != nil {
		return Source{}, err
	}
	return p.resolve(ctx, tx, user, taskID, lock)
}

const sourceIdentityJoin = `
JOIN exam e ON e.tenant_id=t.tenant_id AND e.id=t.exam_id AND e.deleted_at IS NULL
LEFT JOIN exam_session es ON es.tenant_id=e.tenant_id AND es.id=e.exam_session_id
JOIN school sch ON sch.tenant_id=e.tenant_id AND sch.id=e.school_id AND sch.status='active' AND sch.deleted_at IS NULL
JOIN app_user u ON u.tenant_id=e.tenant_id AND u.id=$2::uuid AND u.school_id=e.school_id AND u.status='active' AND u.deleted_at IS NULL
`
const sourcePermission = `EXISTS (
 SELECT 1 FROM user_role ur
 JOIN role r ON r.tenant_id=ur.tenant_id AND r.id=ur.role_id AND r.deleted_at IS NULL
 JOIN role_permission rp ON rp.tenant_id=r.tenant_id AND rp.role_id=r.id AND rp.deleted_at IS NULL
 JOIN permission perm ON perm.tenant_id=rp.tenant_id AND perm.id=rp.permission_id AND perm.deleted_at IS NULL AND perm.code='review:work'
 WHERE ur.tenant_id=u.tenant_id AND ur.user_id=u.id AND ur.deleted_at IS NULL)`

func (p *PostgresSourceProvider) resolve(ctx context.Context, q sourceQueryer, user auth.User, taskID string, lock bool) (Source, error) {
	var ambiguous bool
	if err := q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM review_task WHERE tenant_id=$1::uuid AND id::text=$2) AND EXISTS(SELECT 1 FROM seed_task WHERE tenant_id=$1::uuid AND id::text=$2)`, user.TenantID, taskID).Scan(&ambiguous); err != nil {
		return Source{}, err
	}
	if ambiguous {
		return Source{}, ErrNotFound
	}
	if lock {
		// 自动正式提交先锁父评分运行再锁任务；标记新增也用同一顺序，避免与写分和取消流程反向持锁。
		var runID sql.NullString
		err := q.QueryRowContext(ctx, `SELECT scoring_run_id::text FROM review_task WHERE tenant_id=$1::uuid AND id::text=$2 AND assigned_to=$3::uuid AND deleted_at IS NULL`, user.TenantID, taskID, user.ID).Scan(&runID)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return Source{}, err
		}
		if runID.Valid {
			var lockedID string
			if err = q.QueryRowContext(ctx, `SELECT id::text FROM scoring_run WHERE tenant_id=$1::uuid AND id=$2::uuid AND deleted_at IS NULL FOR UPDATE`, user.TenantID, runID.String).Scan(&lockedID); err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					return Source{}, ErrNotFound
				}
				return Source{}, err
			}
		}
	}
	// 作用域必须由数据库实时核对；会话中旧的 task IDs 或 role 声明不能延长材料访问权。
	query := `SELECT t.id::text,t.answer_segment_id::text,t.exam_id::text,e.name,e.subject,t.question_id::text,t.question_no,t.anonymous_code,t.status,COALESCE(e.archived_at IS NOT NULL OR e.status='archived' OR es.archived_at IS NOT NULL OR es.status='archived',false),qu.score::float8,COALESCE(seg.crop_sha256,''),COALESCE(eqs.id::text,'')
FROM review_task t ` + sourceIdentityJoin + `
JOIN answer_segment seg ON seg.tenant_id=t.tenant_id AND seg.id=t.answer_segment_id AND seg.question_id=t.question_id AND seg.submission_id=t.submission_id AND seg.deleted_at IS NULL
JOIN submission sub ON sub.tenant_id=t.tenant_id AND sub.id=t.submission_id AND sub.exam_id=t.exam_id AND sub.deleted_at IS NULL
JOIN question qu ON qu.tenant_id=t.tenant_id AND qu.id=t.question_id AND qu.exam_id=t.exam_id AND qu.deleted_at IS NULL
LEFT JOIN exam_question_snapshot eqs ON eqs.tenant_id=t.tenant_id AND eqs.exam_id=t.exam_id AND eqs.question_id=t.question_id
WHERE t.tenant_id=$1::uuid AND t.id::text=$3 AND t.assigned_to=$2::uuid AND t.deleted_at IS NULL AND ` + sourcePermission + `
AND (t.status IN ('assigned','in_progress','returned') OR (t.status IN ('submitted','completed') AND EXISTS (SELECT 1 FROM human_grade g WHERE g.tenant_id=t.tenant_id AND g.review_task_id=t.id AND g.reviewer_id=$2::uuid AND g.deleted_at IS NULL)))`
	if lock {
		query += ` FOR UPDATE OF t`
	}
	var source Source
	var status string
	err := q.QueryRowContext(ctx, query, user.TenantID, user.ID, taskID).Scan(&source.TaskID, &source.AnswerSegmentID, &source.ExamID, &source.ExamName, &source.Subject, &source.QuestionID, &source.QuestionNo, &source.AnonymousCode, &status, &source.Archived, &source.MaxScore, &source.ObservedCropSHA256, &source.ObservedSnapshotID)
	if err == nil {
		source.ReviewTaskID = source.TaskID
		source.State = publicState(status)
		return source, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return source, err
	}
	query = `SELECT t.id::text,t.source_image_url,t.exam_id::text,e.name,e.subject,t.question_id::text,t.question_no,t.anonymous_code,t.status,COALESCE(e.archived_at IS NOT NULL OR e.status='archived' OR es.archived_at IS NOT NULL OR es.status='archived',false),t.max_score::float8,t.exam_question_snapshot_id::text
FROM seed_task t ` + sourceIdentityJoin + `
JOIN question qu ON qu.tenant_id=t.tenant_id AND qu.id=t.question_id AND qu.exam_id=t.exam_id AND qu.deleted_at IS NULL
WHERE t.tenant_id=$1::uuid AND t.id::text=$3 AND t.grader_id=$2::uuid AND ` + sourcePermission + `
AND (t.status='in_progress' OR (t.status='completed' AND EXISTS(SELECT 1 FROM seed_observation o WHERE o.tenant_id=t.tenant_id AND o.seed_task_id=t.id AND o.grader_id=$2::uuid)))`
	if lock {
		query += ` FOR UPDATE OF t`
	}
	var imageURL string
	err = q.QueryRowContext(ctx, query, user.TenantID, user.ID, taskID).Scan(&source.TaskID, &imageURL, &source.ExamID, &source.ExamName, &source.Subject, &source.QuestionID, &source.QuestionNo, &source.AnonymousCode, &status, &source.Archived, &source.MaxScore, &source.ObservedSnapshotID)
	if errors.Is(err, sql.ErrNoRows) {
		return Source{}, ErrNotFound
	}
	if err != nil {
		return Source{}, err
	}
	source.SeedTaskID = source.TaskID
	source.State = publicState(status)
	source.AnswerSegmentID = segmentFromURL(imageURL)
	if source.AnswerSegmentID != "" {
		var hash string
		err = q.QueryRowContext(ctx, `SELECT COALESCE(s.crop_sha256,'') FROM answer_segment s JOIN submission sub ON sub.tenant_id=s.tenant_id AND sub.id=s.submission_id AND sub.deleted_at IS NULL WHERE s.tenant_id=$1::uuid AND s.id::text=$2 AND s.question_id::text=$3 AND sub.exam_id::text=$4 AND s.deleted_at IS NULL`, user.TenantID, source.AnswerSegmentID, source.QuestionID, source.ExamID).Scan(&hash)
		if err == nil {
			source.ObservedCropSHA256 = hash
		} else if errors.Is(err, sql.ErrNoRows) {
			return Source{}, ErrNotFound
		} else {
			return Source{}, err
		}
	} else {
		return Source{}, ErrNotFound
	}
	return source, nil
}
func publicState(status string) string {
	if status == "submitted" || status == "completed" {
		return "submitted"
	}
	return "processable"
}

func (p *PostgresSourceProvider) ResolveEvidence(ctx context.Context, user auth.User, taskID string) (Source, EvidenceMaterial, error) {
	if err := requireWork(user); err != nil {
		return Source{}, EvidenceMaterial{}, err
	}
	tx, err := p.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return Source{}, EvidenceMaterial{}, err
	}
	defer tx.Rollback()
	source, err := p.resolve(ctx, tx, user, taskID, false)
	if err != nil {
		return Source{}, EvidenceMaterial{}, err
	}
	material := EvidenceMaterial{SnapshotID: source.ObservedSnapshotID}
	var stem string
	var options, rubricRaw []byte
	// 当前快照不另存题干和选项；它们由考试 ready 后的数据库冻结触发器保护。
	// 草稿/配置期即使有人提前生成了快照，题目仍可变，不能当作冻结材料回看。
	err = tx.QueryRowContext(ctx, `SELECT COALESCE(q.stem,''),q.options,eqs.rubric_snapshot_json FROM question q JOIN exam e ON e.tenant_id=q.tenant_id AND e.id=q.exam_id JOIN exam_question_snapshot eqs ON eqs.tenant_id=q.tenant_id AND eqs.exam_id=q.exam_id AND eqs.question_id=q.id WHERE q.tenant_id=$1::uuid AND q.id::text=$2 AND eqs.id::text=$3 AND q.deleted_at IS NULL AND e.status NOT IN ('draft','configured')`, user.TenantID, source.QuestionID, source.ObservedSnapshotID).Scan(&stem, &options, &rubricRaw)
	if errors.Is(err, sql.ErrNoRows) {
		return Source{}, material, ErrEvidenceUnavailable
	}
	if err != nil {
		return Source{}, material, err
	}
	frozen := review.BookmarkMaterial{SnapshotID: material.SnapshotID, Question: paper.Question{Stem: stem, Score: source.MaxScore}, Rubric: paper.Rubric{MaxScore: source.MaxScore}}
	if err = json.Unmarshal(options, &frozen.Question.Options); err != nil {
		return Source{}, material, err
	}
	if err = json.Unmarshal(rubricRaw, &frozen.Rubric); err != nil {
		return Source{}, material, err
	}
	if frozen.Rubric.MaxScore == 0 {
		frozen.Rubric.MaxScore = source.MaxScore
	}
	material.Question = questionProjection(frozen)
	if source.State == "submitted" {
		op := OwnSubmission{MaxScore: source.MaxScore, RubricSelections: []review.RubricSelection{}}
		var selections []byte
		if source.ReviewTaskID != "" {
			err = tx.QueryRowContext(ctx, `SELECT score::float8,max_score::float8,rubric_selections,created_at FROM human_grade WHERE tenant_id=$1::uuid AND review_task_id::text=$2 AND reviewer_id=$3::uuid AND deleted_at IS NULL ORDER BY created_at DESC,id DESC LIMIT 1`, user.TenantID, taskID, user.ID).Scan(&op.Score, &op.MaxScore, &selections, &op.SubmittedAt)
			if err == nil {
				err = json.Unmarshal(selections, &op.RubricSelections)
			}
		} else {
			err = tx.QueryRowContext(ctx, `SELECT submitted_score::float8,rubric_selection_json,observed_at FROM seed_observation WHERE tenant_id=$1::uuid AND seed_task_id::text=$2 AND grader_id=$3::uuid`, user.TenantID, taskID, user.ID).Scan(&op.Score, &selections, &op.SubmittedAt)
			if err == nil {
				var raw map[string]any
				err = json.Unmarshal(selections, &raw)
				op.RubricSelections = seedSelections(raw)
			}
		}
		if err != nil {
			return Source{}, material, ErrNotFound
		}
		material.OwnSubmission = &op
	}
	if source.AnswerSegmentID != "" {
		var processing, registration string
		err = tx.QueryRowContext(ctx, `SELECT COALESCE(seg.crop_file_asset_id::text,''),COALESCE(seg.crop_sha256,''),COALESCE(seg.registration_run_id::text,''),COALESCE(c.id::text,''),seg.processing_status,COALESCE(r.processing_status,'')
FROM answer_segment seg JOIN submission sub ON sub.tenant_id=seg.tenant_id AND sub.id=seg.submission_id AND sub.deleted_at IS NULL
LEFT JOIN page_registration_run r ON r.tenant_id=seg.tenant_id AND r.id=seg.registration_run_id AND r.deleted_at IS NULL
LEFT JOIN page_registration_correction c ON c.tenant_id=seg.tenant_id AND c.applied_registration_run_id=seg.registration_run_id AND c.status='applied' AND c.deleted_at IS NULL
WHERE seg.tenant_id=$1::uuid AND seg.id::text=$2 AND seg.question_id::text=$3 AND sub.exam_id::text=$4 AND seg.deleted_at IS NULL`, user.TenantID, source.AnswerSegmentID, source.QuestionID, source.ExamID).Scan(&material.Version.AssetID, &material.Version.SHA256, &material.Version.RegistrationID, &material.Version.CorrectionID, &processing, &registration)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return Source{}, material, err
		}
		material.ImageAvailable = err == nil && processing == "completed" && registration == "completed" && material.Version.AssetID != "" && material.Version.SHA256 != ""
		// 现有 Seed 没有公开识别文字。两种来源统一省略文字，避免用字段有无暴露暗样本，
		// 也避免把没有裁剪版本绑定的识别结果误配到当前图片。
	}
	if err = tx.Commit(); err != nil {
		return Source{}, material, err
	}
	return source, material, nil
}

var _ TransactionalSourceProvider = (*PostgresSourceProvider)(nil)
