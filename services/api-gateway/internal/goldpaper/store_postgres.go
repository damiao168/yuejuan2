package goldpaper

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	"edugrade-enterprise/services/api-gateway/internal/auth"

	"github.com/jackc/pgx/v5/pgconn"
)

type PostgresStore struct{ db *sql.DB }

func NewPostgresStore(db *sql.DB) *PostgresStore { return &PostgresStore{db: db} }

func (s *PostgresStore) Nominate(ctx context.Context, tenantID, examID, questionID, actorID string, input NominateInput) (GoldPaper, error) {
	versionInput := normalizeInput(CreateVersionInput{ReferenceScore: input.ReferenceScore, Explanation: input.Explanation, TraitScores: input.TraitScores, ErrorTags: input.ErrorTags, SourceGradeIDs: input.SourceGradeIDs})
	if tenantID == "" || examID == "" || questionID == "" || actorID == "" || input.SubmissionID == "" {
		return GoldPaper{}, ErrInvalidInput
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return GoldPaper{}, err
	}
	defer tx.Rollback()
	source, err := loadSource(ctx, tx, tenantID, examID, questionID, input.SubmissionID)
	if err != nil {
		return GoldPaper{}, err
	}
	if err := validateInput(versionInput, source.MaxScore); err != nil {
		return GoldPaper{}, err
	}
	if !containsAll(source.AvailableGradeIDs, versionInput.SourceGradeIDs) {
		return GoldPaper{}, ErrSourceGradeMissing
	}
	var goldID string
	pool, err := registerImagePoolTx(ctx, tx, tenantID, source.ImageBinding.SHA256, "")
	if err != nil {
		return GoldPaper{}, err
	}
	err = tx.QueryRowContext(ctx, `
INSERT INTO grading_gold_paper (tenant_id, exam_id, question_id, submission_id, nominated_by,quality_pool)
VALUES ($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5::uuid,$6)
RETURNING id::text
`, tenantID, examID, questionID, input.SubmissionID, actorID, pool).Scan(&goldID)
	if isUniqueViolation(err) {
		return GoldPaper{}, ErrConflict
	}
	if err != nil {
		return GoldPaper{}, err
	}
	if _, err := insertVersion(ctx, tx, tenantID, goldID, versionForInsert(source, actorID, 1, versionInput)); err != nil {
		return GoldPaper{}, err
	}
	if err := tx.Commit(); err != nil {
		return GoldPaper{}, err
	}
	return s.Get(ctx, tenantID, goldID)
}

func (s *PostgresStore) CreateVersion(ctx context.Context, tenantID, goldID, actorID string, input CreateVersionInput) (GoldPaper, error) {
	input = normalizeInput(input)
	if tenantID == "" || goldID == "" || actorID == "" {
		return GoldPaper{}, ErrInvalidInput
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return GoldPaper{}, err
	}
	defer tx.Rollback()
	var examID, questionID, submissionID, status, pool string
	var nextVersion int
	err = tx.QueryRowContext(ctx, `
SELECT exam_id::text,question_id::text,submission_id::text,status,quality_pool,
       COALESCE((SELECT max(version)+1 FROM grading_gold_paper_version v WHERE v.tenant_id=gp.tenant_id AND v.gold_paper_id=gp.id),1)
FROM grading_gold_paper gp
WHERE tenant_id=$1::uuid AND id=$2::uuid
FOR UPDATE
`, tenantID, goldID).Scan(&examID, &questionID, &submissionID, &status, &pool, &nextVersion)
	if errors.Is(err, sql.ErrNoRows) {
		return GoldPaper{}, ErrNotFound
	}
	if err != nil {
		return GoldPaper{}, err
	}
	if Status(status) == StatusRetired {
		return GoldPaper{}, ErrConflict
	}
	source, err := loadSource(ctx, tx, tenantID, examID, questionID, submissionID)
	if err != nil {
		return GoldPaper{}, err
	}
	if err := validateInput(input, source.MaxScore); err != nil {
		return GoldPaper{}, err
	}
	if !containsAll(source.AvailableGradeIDs, input.SourceGradeIDs) {
		return GoldPaper{}, ErrSourceGradeMissing
	}
	registered, err := registerImagePoolTx(ctx, tx, tenantID, source.ImageBinding.SHA256, pool)
	if err != nil {
		return GoldPaper{}, err
	}
	if registered != pool {
		return GoldPaper{}, ErrConflict
	}
	if _, err := insertVersion(ctx, tx, tenantID, goldID, versionForInsert(source, actorID, nextVersion, input)); err != nil {
		if isUniqueViolation(err) {
			return GoldPaper{}, ErrConflict
		}
		return GoldPaper{}, err
	}
	if err := tx.Commit(); err != nil {
		return GoldPaper{}, err
	}
	return s.Get(ctx, tenantID, goldID)
}

// 审批事务同时锁 Gold 主记录和目标版本，保证 active_version 与 approved_at 不会被并发请求拆开。
func (s *PostgresStore) Approve(ctx context.Context, tenantID, goldID, actorID string, versionNumber int) (GoldPaper, error) {
	if tenantID == "" || goldID == "" || actorID == "" || versionNumber <= 0 {
		return GoldPaper{}, ErrInvalidInput
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return GoldPaper{}, err
	}
	defer tx.Rollback()
	var status string
	if err := tx.QueryRowContext(ctx, `SELECT status FROM grading_gold_paper WHERE tenant_id=$1::uuid AND id=$2::uuid FOR UPDATE`, tenantID, goldID).Scan(&status); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return GoldPaper{}, ErrNotFound
		}
		return GoldPaper{}, err
	}
	if Status(status) == StatusRetired {
		return GoldPaper{}, ErrConflict
	}
	var approvedAt sql.NullTime
	var imageReady bool
	if err := tx.QueryRowContext(ctx, `
SELECT approved_at,(frozen_crop_file_asset_id IS NOT NULL AND image_invalidated_at IS NULL) FROM grading_gold_paper_version
WHERE tenant_id=$1::uuid AND gold_paper_id=$2::uuid AND version=$3
FOR UPDATE
`, tenantID, goldID, versionNumber).Scan(&approvedAt, &imageReady); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return GoldPaper{}, ErrNotFound
		}
		return GoldPaper{}, err
	}
	if approvedAt.Valid {
		return GoldPaper{}, ErrAlreadyApproved
	}
	if !imageReady {
		return GoldPaper{}, ErrImageUnavailable
	}
	if _, err := tx.ExecContext(ctx, `
UPDATE grading_gold_paper_version
SET approved_by=$3::uuid,approved_at=now()
WHERE tenant_id=$1::uuid AND gold_paper_id=$2::uuid AND version=$4 AND approved_at IS NULL
`, tenantID, goldID, actorID, versionNumber); err != nil {
		return GoldPaper{}, err
	}
	if _, err := tx.ExecContext(ctx, `
UPDATE grading_gold_paper SET active_version=$3,status='active',updated_at=now()
WHERE tenant_id=$1::uuid AND id=$2::uuid
`, tenantID, goldID, versionNumber); err != nil {
		return GoldPaper{}, err
	}
	if err := tx.Commit(); err != nil {
		return GoldPaper{}, err
	}
	return s.Get(ctx, tenantID, goldID)
}

func (s *PostgresStore) Retire(ctx context.Context, tenantID, goldID, actorID, reason string) (GoldPaper, error) {
	reason = strings.TrimSpace(reason)
	if tenantID == "" || goldID == "" || actorID == "" || reason == "" || len(reason) > 2000 {
		return GoldPaper{}, ErrInvalidInput
	}
	result, err := s.db.ExecContext(ctx, `
UPDATE grading_gold_paper
SET status='retired',retired_by=$3::uuid,retired_at=now(),retirement_reason=$4,updated_at=now()
WHERE tenant_id=$1::uuid AND id=$2::uuid AND status<>'retired'
`, tenantID, goldID, actorID, reason)
	if err != nil {
		return GoldPaper{}, err
	}
	if count, _ := result.RowsAffected(); count == 0 {
		if _, getErr := s.Get(ctx, tenantID, goldID); getErr != nil {
			return GoldPaper{}, getErr
		}
		return GoldPaper{}, ErrConflict
	}
	return s.Get(ctx, tenantID, goldID)
}

func (s *PostgresStore) Get(ctx context.Context, tenantID, id string) (GoldPaper, error) {
	items, err := s.query(ctx, tenantID, ` AND gp.id::text=$2 AND $3::text='' AND $4::text=''`, id, "", "")
	if err != nil {
		return GoldPaper{}, err
	}
	if len(items) == 0 {
		return GoldPaper{}, ErrNotFound
	}
	return items[0], nil
}

func (s *PostgresStore) List(ctx context.Context, tenantID string, filter ListFilter) ([]GoldPaper, error) {
	if filter.Status != "" && filter.Status != StatusPendingApproval && filter.Status != StatusActive && filter.Status != StatusRetired {
		return nil, ErrInvalidInput
	}
	accessArgs, err := filter.Access.SQLArgs(tenantID)
	if err != nil {
		return nil, err
	}
	args := append([]any{filter.ExamID, filter.QuestionID, string(filter.Status)}, accessArgs...)
	return s.query(ctx, tenantID, ` AND ($2='' OR gp.exam_id::text=$2) AND ($3='' OR gp.question_id::text=$3) AND ($4='' OR gp.status=$4) AND `+auth.ExamCollectionPredicate("gp.exam_id", 5), args...)
}

func (s *PostgresStore) ListActiveApproved(ctx context.Context, tenantID, examID, questionID string) ([]GoldPaper, error) {
	return s.query(ctx, tenantID, ` AND gp.exam_id::text=$2 AND gp.question_id::text=$3 AND gp.status='active' AND $4::text=''`, examID, questionID, "")
}

func (s *PostgresStore) Coverage(ctx context.Context, tenantID, examID, questionID string) (Coverage, error) {
	items, err := s.ListActiveApproved(ctx, tenantID, examID, questionID)
	if err != nil {
		return Coverage{}, err
	}
	var subject, archetype, risk string
	var maxScore float64
	err = s.db.QueryRowContext(ctx, `
SELECT COALESCE(eqs.profile_snapshot_json->>'subject_code',''),eqs.archetype_code,eqs.risk_tier,q.score::float8
FROM question q
JOIN LATERAL (
  SELECT * FROM exam_question_snapshot s
  WHERE s.tenant_id=q.tenant_id AND s.exam_id=q.exam_id AND s.question_id=q.id
  ORDER BY s.snapshot_version DESC LIMIT 1
) eqs ON true
WHERE q.tenant_id=$1::uuid AND q.exam_id=$2::uuid AND q.id=$3::uuid AND q.deleted_at IS NULL
`, tenantID, examID, questionID).Scan(&subject, &archetype, &risk, &maxScore)
	if errors.Is(err, sql.ErrNoRows) {
		return Coverage{}, ErrNotFound
	}
	if err != nil {
		return Coverage{}, err
	}
	return buildCoverage(examID, questionID, subject, archetype, risk, maxScore, items), nil
}

type sqlQueryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

// 提名事务共享锁住切片、配准和资产，来源更换需等待冻结身份落盘；版本读取随后只使用这一固定身份。
func loadSource(ctx context.Context, db sqlQueryer, tenantID, examID, questionID, submissionID string) (Source, error) {
	var source Source
	var rubricJSON []byte
	err := db.QueryRowContext(ctx, `
SELECT sub.exam_id::text,q.id::text,sub.id::text,seg.id::text,eqs.id::text,
       COALESCE(eqs.profile_snapshot_json->>'subject_code',''),eqs.archetype_code,eqs.risk_tier,COALESCE(q.question_no,''),
       q.score::float8,eqs.rubric_snapshot_json,
       asset.id::text,asset.hash_sha256,asset.owner_type,asset.owner_id::text
FROM submission sub
JOIN answer_segment seg ON seg.tenant_id=sub.tenant_id AND seg.submission_id=sub.id AND seg.question_id=$3::uuid AND seg.deleted_at IS NULL
JOIN question q ON q.tenant_id=sub.tenant_id AND q.exam_id=sub.exam_id AND q.id=seg.question_id AND q.deleted_at IS NULL
JOIN page_registration_run run ON run.tenant_id=seg.tenant_id AND run.id=seg.registration_run_id AND run.deleted_at IS NULL AND run.processing_status='completed'
JOIN file_asset asset ON asset.tenant_id=seg.tenant_id AND asset.id=seg.crop_file_asset_id
 AND asset.exam_id=sub.exam_id AND asset.hash_sha256=seg.crop_sha256
 AND asset.deleted_at IS NULL AND asset.lifecycle_status='active' AND asset.content_type LIKE 'image/%' AND asset.size_bytes BETWEEN 1 AND 67108864
 AND ((asset.owner_type='answer_segment_crop' AND asset.owner_id=seg.registration_run_id)
 OR (asset.owner_type='page_registration_correction_preview' AND EXISTS (
   SELECT 1 FROM page_registration_correction correction
   WHERE correction.tenant_id=seg.tenant_id AND correction.id=asset.owner_id
     AND correction.applied_registration_run_id=seg.registration_run_id AND correction.status='applied' AND correction.deleted_at IS NULL)))
JOIN LATERAL (
  SELECT * FROM exam_question_snapshot s
  WHERE s.tenant_id=q.tenant_id AND s.exam_id=q.exam_id AND s.question_id=q.id
  ORDER BY s.snapshot_version DESC LIMIT 1
) eqs ON true
WHERE sub.tenant_id=$1::uuid AND sub.exam_id=$2::uuid AND sub.id=$4::uuid AND sub.deleted_at IS NULL
 AND seg.processing_status='completed' AND (NOT COALESCE(seg.normalized_bbox ? 'answer_scope',false) OR seg.normalized_bbox->>'answer_scope'='question')
FOR SHARE OF seg,asset,run
`, tenantID, examID, questionID, submissionID).Scan(
		&source.ExamID, &source.QuestionID, &source.SubmissionID, &source.AnswerSegmentID, &source.SnapshotID,
		&source.SubjectCode, &source.ArchetypeCode, &source.RiskTier, &source.QuestionNo, &source.MaxScore, &rubricJSON,
		&source.ImageBinding.AssetID, &source.ImageBinding.SHA256, &source.ImageBinding.OwnerType, &source.ImageBinding.OwnerID,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return Source{}, ErrNotFound
	}
	if err != nil {
		return Source{}, err
	}
	source.ImageBinding.SegmentID = source.AnswerSegmentID
	if err := json.Unmarshal(rubricJSON, &source.RubricSnapshot); err != nil {
		return Source{}, err
	}
	rows, err := db.QueryContext(ctx, `
SELECT id::text FROM question_grade
WHERE tenant_id=$1::uuid AND answer_segment_id=$2::uuid AND deleted_at IS NULL
UNION
SELECT id::text FROM human_grade
WHERE tenant_id=$1::uuid AND answer_segment_id=$2::uuid AND deleted_at IS NULL
`, tenantID, source.AnswerSegmentID)
	if err != nil {
		return Source{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return Source{}, err
		}
		source.AvailableGradeIDs = append(source.AvailableGradeIDs, id)
	}
	return source, rows.Err()
}

// 来源快照和提名输入已由调用方校验；版本号仍由创建事务分配，不从请求输入取得。
func versionForInsert(source Source, actorID string, number int, input CreateVersionInput) Version {
	return Version{
		Version: number, ExamQuestionSnapshotID: source.SnapshotID, MaxScore: source.MaxScore,
		RubricSnapshot: source.RubricSnapshot, ReferenceScore: input.ReferenceScore,
		Explanation: input.Explanation, TraitScores: input.TraitScores,
		ErrorTags: input.ErrorTags, SourceGradeIDs: input.SourceGradeIDs, NominatedBy: actorID,
		ImageBinding: source.ImageBinding,
	}
}

// insertVersion 只写入提名事实；主记录和版本的提交或回滚仍由调用方事务负责。
func insertVersion(ctx context.Context, tx *sql.Tx, tenantID, goldID string, version Version) (string, error) {
	rubric, err := json.Marshal(version.RubricSnapshot)
	if err != nil {
		return "", ErrInvalidInput
	}
	traits, err := json.Marshal(version.TraitScores)
	if err != nil {
		return "", ErrInvalidInput
	}
	tags, err := json.Marshal(version.ErrorTags)
	if err != nil {
		return "", ErrInvalidInput
	}
	grades, err := json.Marshal(version.SourceGradeIDs)
	if err != nil {
		return "", ErrInvalidInput
	}
	var id string
	err = tx.QueryRowContext(ctx, `
INSERT INTO grading_gold_paper_version (
 tenant_id,gold_paper_id,version,exam_question_snapshot_id,reference_score,max_score,
 rubric_snapshot_json,explanation,trait_scores_json,error_tags_json,source_grade_ids_json,nominated_by,
 frozen_answer_segment_id,frozen_crop_file_asset_id,frozen_crop_sha256,frozen_crop_owner_type,frozen_crop_owner_id
) VALUES ($1::uuid,$2::uuid,$3,$4::uuid,$5,$6,$7::jsonb,$8,$9::jsonb,$10::jsonb,$11::jsonb,$12::uuid,
 NULLIF($13,'')::uuid,NULLIF($14,'')::uuid,NULLIF($15,''),NULLIF($16,''),NULLIF($17,'')::uuid)
RETURNING id::text
`, tenantID, goldID, version.Version, version.ExamQuestionSnapshotID, version.ReferenceScore, version.MaxScore, rubric, version.Explanation, traits, tags, grades, version.NominatedBy,
		version.ImageBinding.SegmentID, version.ImageBinding.AssetID, version.ImageBinding.SHA256, version.ImageBinding.OwnerType, version.ImageBinding.OwnerID).Scan(&id)
	return id, err
}

func (s *PostgresStore) query(ctx context.Context, tenantID, condition string, filterArgs ...any) ([]GoldPaper, error) {
	query := `
SELECT gp.id::text,gp.exam_id::text,gp.question_id::text,gp.submission_id::text,gp.quality_pool,
       COALESCE(gp.active_version,0),gp.status,gp.nominated_by::text,gp.retired_at,gp.retirement_reason,gp.created_at,gp.updated_at,
       COALESCE(eqs.profile_snapshot_json->>'subject_code',''),eqs.archetype_code,eqs.risk_tier,COALESCE(q.question_no,''),
       v.id::text,v.version,v.exam_question_snapshot_id::text,v.reference_score::float8,v.max_score::float8,
       v.rubric_snapshot_json,v.explanation,v.trait_scores_json,v.error_tags_json,v.source_grade_ids_json,
       v.nominated_by::text,v.approved_by::text,v.approved_at,v.created_at,
       COALESCE(v.frozen_answer_segment_id::text,''),COALESCE(v.frozen_crop_file_asset_id::text,''),COALESCE(v.frozen_crop_sha256,''),
       COALESCE(v.frozen_crop_owner_type,''),COALESCE(v.frozen_crop_owner_id::text,''),v.image_invalidated_at
FROM grading_gold_paper gp
JOIN grading_gold_paper_version v ON v.tenant_id=gp.tenant_id AND v.gold_paper_id=gp.id
JOIN exam_question_snapshot eqs ON eqs.tenant_id=v.tenant_id AND eqs.id=v.exam_question_snapshot_id
LEFT JOIN question q ON q.tenant_id=gp.tenant_id AND q.exam_id=gp.exam_id AND q.id=gp.question_id
WHERE gp.tenant_id=$1::uuid` + condition + `
ORDER BY gp.created_at,gp.id,v.version`
	args := append([]any{tenantID}, filterArgs...)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items, indexes := []GoldPaper{}, map[string]int{}
	for rows.Next() {
		var item GoldPaper
		var version Version
		var retiredAt, approvedAt, imageInvalidatedAt sql.NullTime
		var approvedBy sql.NullString
		var rubricJSON, traitsJSON, tagsJSON, gradesJSON []byte
		if err := rows.Scan(&item.ID, &item.ExamID, &item.QuestionID, &item.SubmissionID, &item.QualityPool, &item.ActiveVersion, &item.Status, &item.NominatedBy, &retiredAt, &item.RetirementReason, &item.CreatedAt, &item.UpdatedAt,
			&item.SubjectCode, &item.ArchetypeCode, &item.RiskTier, &item.QuestionNo, &version.ID, &version.Version, &version.ExamQuestionSnapshotID, &version.ReferenceScore, &version.MaxScore,
			&rubricJSON, &version.Explanation, &traitsJSON, &tagsJSON, &gradesJSON, &version.NominatedBy, &approvedBy, &approvedAt, &version.CreatedAt,
			&version.ImageBinding.SegmentID, &version.ImageBinding.AssetID, &version.ImageBinding.SHA256, &version.ImageBinding.OwnerType, &version.ImageBinding.OwnerID, &imageInvalidatedAt); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(rubricJSON, &version.RubricSnapshot); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(traitsJSON, &version.TraitScores); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(tagsJSON, &version.ErrorTags); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(gradesJSON, &version.SourceGradeIDs); err != nil {
			return nil, err
		}
		if retiredAt.Valid {
			value := retiredAt.Time.UTC()
			item.RetiredAt = &value
		}
		if approvedAt.Valid {
			value := approvedAt.Time.UTC()
			version.ApprovedAt = &value
			version.ApprovedBy = approvedBy.String
		}
		if imageInvalidatedAt.Valid {
			value := imageInvalidatedAt.Time.UTC()
			version.ImageInvalidatedAt = &value
		}
		if version.ImageBinding.Valid() && version.ImageInvalidatedAt == nil {
			version.AnswerImageURL = VersionImageURL(item.ID, version.Version)
		}
		item.CreatedAt, item.UpdatedAt, version.CreatedAt = item.CreatedAt.UTC(), item.UpdatedAt.UTC(), version.CreatedAt.UTC()
		index, ok := indexes[item.ID]
		if !ok {
			index = len(items)
			indexes[item.ID] = index
			item.Versions = []Version{}
			items = append(items, item)
		}
		items[index].Versions = append(items[index].Versions, version)
		if version.Version == item.ActiveVersion || item.ActiveVersion == 0 && version.AnswerImageURL != "" {
			items[index].AnswerImageURL = version.AnswerImageURL
		}
	}
	return items, rows.Err()
}

func isUniqueViolation(err error) bool {
	var value *pgconn.PgError
	return errors.As(err, &value) && value.Code == "23505"
}
