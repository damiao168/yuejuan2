package reviewannotation

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5/pgconn"
)

type PostgresStore struct{ db *sql.DB }

func NewPostgresStore(db *sql.DB) *PostgresStore { return &PostgresStore{db: db} }

// 创建时从任务和答案段关联表读取不可变归属，避免客户端伪造 segment/page ID。
func (s *PostgresStore) CreateAnnotation(ctx context.Context, tenantID, reviewTaskID, actorID string, input CreateAnnotationInput) (Annotation, error) {
	input = normalizeAnnotationInput(input)
	if tenantID == "" || reviewTaskID == "" || actorID == "" || validateAnnotationInput(input) != nil {
		return Annotation{}, ErrInvalidInput
	}
	payload, err := json.Marshal(input.Payload)
	if err != nil {
		return Annotation{}, ErrInvalidInput
	}
	return scanAnnotation(s.db.QueryRowContext(ctx, `
INSERT INTO review_annotation (
  tenant_id, review_task_id, answer_segment_id, submission_page_id,
  annotation_type, coordinate_space, x, y, width, height, payload, content,
  visibility, created_by, updated_by
)
SELECT rt.tenant_id, rt.id, rt.answer_segment_id, seg.submission_page_id,
       $4, $5, $6, $7, $8, $9, $10, $11, $12, $3::uuid, $3::uuid
FROM review_task rt
JOIN answer_segment seg
  ON seg.tenant_id = rt.tenant_id AND seg.id = rt.answer_segment_id AND seg.deleted_at IS NULL
WHERE rt.tenant_id = $1::uuid AND rt.id::text = $2 AND rt.deleted_at IS NULL
RETURNING `+annotationColumns()+`
`, tenantID, reviewTaskID, actorID, input.Type, input.Geometry.CoordinateSpace,
		input.Geometry.X, input.Geometry.Y, input.Geometry.Width, input.Geometry.Height,
		payload, input.Content, input.Visibility))
}

func (s *PostgresStore) ListAnnotations(ctx context.Context, tenantID, reviewTaskID string) ([]Annotation, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT `+annotationColumns()+`
FROM review_annotation
WHERE tenant_id = $1::uuid AND review_task_id::text = $2 AND deleted_at IS NULL
ORDER BY created_at, id
`, tenantID, reviewTaskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Annotation{}
	for rows.Next() {
		item, err := scanAnnotation(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *PostgresStore) GetAnnotation(ctx context.Context, tenantID, id string) (Annotation, error) {
	return scanAnnotation(s.db.QueryRowContext(ctx, `
SELECT `+annotationColumns()+`
FROM review_annotation
WHERE tenant_id = $1::uuid AND id::text = $2 AND deleted_at IS NULL
`, tenantID, id))
}

// 学生列表只在考试已发布且批注标记为 student_after_publish 时返回，查询结果再转换为安全 DTO。
func (s *PostgresStore) ListStudentAnnotations(ctx context.Context, tenantID, submissionID string) ([]StudentAnnotation, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT ra.id::text, ra.answer_segment_id::text, ra.submission_page_id::text,
       ra.annotation_type, ra.coordinate_space, ra.x::float8, ra.y::float8,
       ra.width::float8, ra.height::float8, ra.content, ra.created_at, ra.updated_at
FROM review_annotation ra
JOIN answer_segment seg
  ON seg.tenant_id = ra.tenant_id AND seg.id = ra.answer_segment_id AND seg.deleted_at IS NULL
JOIN submission sub
  ON sub.tenant_id = seg.tenant_id AND sub.id = seg.submission_id AND sub.deleted_at IS NULL
JOIN exam e
  ON e.tenant_id = sub.tenant_id AND e.id = sub.exam_id AND e.deleted_at IS NULL
WHERE ra.tenant_id = $1::uuid AND sub.id::text = $2
  AND e.status = 'published'
  AND ra.visibility = 'student_after_publish' AND ra.deleted_at IS NULL
ORDER BY ra.created_at, ra.id
`, tenantID, submissionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []StudentAnnotation{}
	for rows.Next() {
		var item StudentAnnotation
		if err := rows.Scan(
			&item.ID, &item.AnswerSegmentID, &item.SubmissionPageID, &item.Type,
			&item.Geometry.CoordinateSpace, &item.Geometry.X, &item.Geometry.Y,
			&item.Geometry.Width, &item.Geometry.Height, &item.Content,
			&item.CreatedAt, &item.UpdatedAt,
		); err != nil {
			return nil, err
		}
		item.CreatedAt, item.UpdatedAt = item.CreatedAt.UTC(), item.UpdatedAt.UTC()
		out = append(out, item)
	}
	return out, rows.Err()
}

// 学生批注只能来自当前发布版本中该学生的 release_item 和指定题目；
// 查询不接受提交 ID，未发布或未授权题目自然返回空结果。
func (s *PostgresStore) ListStudentQuestionAnnotations(ctx context.Context, tenantID, examID, studentID, questionID string) ([]StudentAnnotation, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT ra.id::text, ra.answer_segment_id::text, ra.submission_page_id::text,
       ra.annotation_type, ra.coordinate_space, ra.x::float8, ra.y::float8,
       ra.width::float8, ra.height::float8, ra.content, ra.created_at, ra.updated_at
FROM score_release_current current_release
JOIN score_release release
  ON release.tenant_id = current_release.tenant_id
 AND release.id = current_release.release_id
 AND release.exam_id = current_release.exam_id
JOIN score_release_item release_item
  ON release_item.tenant_id = release.tenant_id
 AND release_item.release_id = release.id
 AND release_item.student_id = $3::uuid
JOIN score_release_question release_question
  ON release_question.tenant_id = release.tenant_id
 AND release_question.release_id = release.id
 AND release_question.submission_id = release_item.submission_id
 AND release_question.question_id = $4::uuid
JOIN answer_segment segment
  ON segment.tenant_id = release_question.tenant_id
 AND segment.submission_id = release_question.submission_id
 AND segment.question_id = release_question.question_id
 AND segment.deleted_at IS NULL
JOIN review_annotation ra
  ON ra.tenant_id = segment.tenant_id
 AND ra.answer_segment_id = segment.id
WHERE current_release.tenant_id = $1::uuid
  AND current_release.exam_id = $2::uuid
  AND release.status = 'published'
  AND COALESCE((release.visibility_policy ->> 'show_question_scores')::boolean, false)
  AND ra.visibility = 'student_after_publish'
  AND ra.deleted_at IS NULL
ORDER BY ra.created_at, ra.id
`, tenantID, examID, studentID, questionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanStudentAnnotations(rows)
}

// UPDATE 把 revision 作为并发条件；影响行数为零时再区分资源不存在和版本过期。
func (s *PostgresStore) UpdateAnnotation(ctx context.Context, tenantID, id, actorID string, input UpdateAnnotationInput) (Annotation, error) {
	normalized := normalizeAnnotationInput(CreateAnnotationInput{
		Type: input.Type, Geometry: input.Geometry, Payload: input.Payload,
		Content: input.Content, Visibility: input.Visibility,
	})
	if tenantID == "" || id == "" || actorID == "" || input.ExpectedRevision <= 0 || validateAnnotationInput(normalized) != nil {
		return Annotation{}, ErrInvalidInput
	}
	payload, err := json.Marshal(normalized.Payload)
	if err != nil {
		return Annotation{}, ErrInvalidInput
	}
	item, err := scanAnnotation(s.db.QueryRowContext(ctx, `
UPDATE review_annotation
SET annotation_type = $4, coordinate_space = $5,
    x = $6, y = $7, width = $8, height = $9, payload = $10, content = $11,
    visibility = $12, updated_by = $3::uuid, revision = revision + 1, updated_at = now()
WHERE tenant_id = $1::uuid AND id::text = $2 AND revision = $13 AND deleted_at IS NULL
RETURNING `+annotationColumns()+`
`, tenantID, id, actorID, normalized.Type, normalized.Geometry.CoordinateSpace,
		normalized.Geometry.X, normalized.Geometry.Y, normalized.Geometry.Width, normalized.Geometry.Height,
		payload, normalized.Content, normalized.Visibility, input.ExpectedRevision))
	if !errors.Is(err, ErrNotFound) {
		return item, err
	}
	return Annotation{}, s.annotationMutationError(ctx, tenantID, id)
}

type studentAnnotationRows interface {
	Next() bool
	Scan(...any) error
	Err() error
}

func scanStudentAnnotations(rows studentAnnotationRows) ([]StudentAnnotation, error) {
	out := []StudentAnnotation{}
	for rows.Next() {
		var item StudentAnnotation
		if err := rows.Scan(
			&item.ID, &item.AnswerSegmentID, &item.SubmissionPageID, &item.Type,
			&item.Geometry.CoordinateSpace, &item.Geometry.X, &item.Geometry.Y,
			&item.Geometry.Width, &item.Geometry.Height, &item.Content,
			&item.CreatedAt, &item.UpdatedAt,
		); err != nil {
			return nil, err
		}
		item.CreatedAt, item.UpdatedAt = item.CreatedAt.UTC(), item.UpdatedAt.UTC()
		out = append(out, item)
	}
	return out, rows.Err()
}

// 删除沿用 revision 条件；更新或删除竞争时统一映射为版本冲突，调用方需重新读取。
func (s *PostgresStore) DeleteAnnotation(ctx context.Context, tenantID, id, actorID string, expectedRevision int64) error {
	if tenantID == "" || id == "" || actorID == "" || expectedRevision <= 0 {
		return ErrInvalidInput
	}
	var deletedID string
	err := s.db.QueryRowContext(ctx, `
UPDATE review_annotation
SET deleted_at = now(), updated_at = now(), updated_by = $3::uuid, revision = revision + 1
WHERE tenant_id = $1::uuid AND id::text = $2 AND revision = $4 AND deleted_at IS NULL
RETURNING id::text
`, tenantID, id, actorID, expectedRevision).Scan(&deletedID)
	if errors.Is(err, sql.ErrNoRows) {
		return s.annotationMutationError(ctx, tenantID, id)
	}
	return err
}

func (s *PostgresStore) CreateCommentTemplate(ctx context.Context, tenantID, actorID string, input CreateCommentTemplateInput) (CommentTemplate, error) {
	input.Title, input.Content, input.Shortcut = normalizeTemplate(input.Title, input.Content, input.Shortcut)
	if tenantID == "" || actorID == "" || validateTemplate(input.Title, input.Content, input.Shortcut) != nil {
		return CommentTemplate{}, ErrInvalidInput
	}
	item, err := scanCommentTemplate(s.db.QueryRowContext(ctx, `
INSERT INTO review_comment_template (
  tenant_id, owner_id, title, content, shortcut, created_by, updated_by
)
VALUES ($1::uuid, $2::uuid, $3, $4, $5, $2::uuid, $2::uuid)
RETURNING `+commentTemplateColumns()+`
`, tenantID, actorID, input.Title, input.Content, input.Shortcut))
	if isUniqueViolation(err) {
		return CommentTemplate{}, ErrShortcutConflict
	}
	return item, err
}

func (s *PostgresStore) ListCommentTemplates(ctx context.Context, tenantID, ownerID string) ([]CommentTemplate, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT `+commentTemplateColumns()+`
FROM review_comment_template
WHERE tenant_id = $1::uuid AND owner_id = $2::uuid AND deleted_at IS NULL
ORDER BY usage_count DESC, updated_at DESC, id
`, tenantID, ownerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []CommentTemplate{}
	for rows.Next() {
		item, err := scanCommentTemplate(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *PostgresStore) GetCommentTemplate(ctx context.Context, tenantID, ownerID, id string) (CommentTemplate, error) {
	return scanCommentTemplate(s.db.QueryRowContext(ctx, `
SELECT `+commentTemplateColumns()+`
FROM review_comment_template
WHERE tenant_id = $1::uuid AND owner_id = $2::uuid AND id::text = $3 AND deleted_at IS NULL
`, tenantID, ownerID, id))
}

func (s *PostgresStore) UpdateCommentTemplate(ctx context.Context, tenantID, ownerID, id string, input UpdateCommentTemplateInput) (CommentTemplate, error) {
	input.Title, input.Content, input.Shortcut = normalizeTemplate(input.Title, input.Content, input.Shortcut)
	if tenantID == "" || ownerID == "" || id == "" || input.ExpectedRevision <= 0 || validateTemplate(input.Title, input.Content, input.Shortcut) != nil {
		return CommentTemplate{}, ErrInvalidInput
	}
	item, err := scanCommentTemplate(s.db.QueryRowContext(ctx, `
UPDATE review_comment_template
SET title = $4, content = $5, shortcut = $6, updated_by = $2::uuid,
    revision = revision + 1, updated_at = now()
WHERE tenant_id = $1::uuid AND owner_id = $2::uuid AND id::text = $3
  AND revision = $7 AND deleted_at IS NULL
RETURNING `+commentTemplateColumns()+`
`, tenantID, ownerID, id, input.Title, input.Content, input.Shortcut, input.ExpectedRevision))
	if isUniqueViolation(err) {
		return CommentTemplate{}, ErrShortcutConflict
	}
	if !errors.Is(err, ErrNotFound) {
		return item, err
	}
	return CommentTemplate{}, s.templateMutationError(ctx, tenantID, ownerID, id)
}

func (s *PostgresStore) DeleteCommentTemplate(ctx context.Context, tenantID, ownerID, id string, expectedRevision int64) error {
	if tenantID == "" || ownerID == "" || id == "" || expectedRevision <= 0 {
		return ErrInvalidInput
	}
	var deletedID string
	err := s.db.QueryRowContext(ctx, `
UPDATE review_comment_template
SET deleted_at = now(), updated_at = now(), updated_by = $2::uuid, revision = revision + 1
WHERE tenant_id = $1::uuid AND owner_id = $2::uuid AND id::text = $3
  AND revision = $4 AND deleted_at IS NULL
RETURNING id::text
`, tenantID, ownerID, id, expectedRevision).Scan(&deletedID)
	if errors.Is(err, sql.ErrNoRows) {
		return s.templateMutationError(ctx, tenantID, ownerID, id)
	}
	return err
}

// 数据库直接递增使用次数并返回新版本，快捷键不存在时不会创建或修改其他模板。
func (s *PostgresStore) UseCommentTemplate(ctx context.Context, tenantID, ownerID, shortcut string) (CommentTemplate, error) {
	_, _, shortcut = normalizeTemplate("", "", shortcut)
	if shortcut == "" {
		return CommentTemplate{}, ErrInvalidInput
	}
	return scanCommentTemplate(s.db.QueryRowContext(ctx, `
UPDATE review_comment_template
SET usage_count = usage_count + 1, revision = revision + 1, updated_at = now(), updated_by = $2::uuid
WHERE tenant_id = $1::uuid AND owner_id = $2::uuid AND shortcut = $3 AND deleted_at IS NULL
RETURNING `+commentTemplateColumns()+`
`, tenantID, ownerID, shortcut))
}

func (s *PostgresStore) annotationMutationError(ctx context.Context, tenantID, id string) error {
	var exists bool
	if err := s.db.QueryRowContext(ctx, `SELECT EXISTS (
  SELECT 1 FROM review_annotation WHERE tenant_id = $1::uuid AND id::text = $2 AND deleted_at IS NULL
)`, tenantID, id).Scan(&exists); err != nil {
		return err
	}
	if exists {
		return ErrRevisionConflict
	}
	return ErrNotFound
}

func (s *PostgresStore) templateMutationError(ctx context.Context, tenantID, ownerID, id string) error {
	var exists bool
	if err := s.db.QueryRowContext(ctx, `SELECT EXISTS (
  SELECT 1 FROM review_comment_template
  WHERE tenant_id = $1::uuid AND owner_id = $2::uuid AND id::text = $3 AND deleted_at IS NULL
)`, tenantID, ownerID, id).Scan(&exists); err != nil {
		return err
	}
	if exists {
		return ErrRevisionConflict
	}
	return ErrNotFound
}

type scanner interface{ Scan(...any) error }

func scanAnnotation(row scanner) (Annotation, error) {
	var item Annotation
	var payload []byte
	err := row.Scan(
		&item.ID, &item.TenantID, &item.ReviewTaskID, &item.AnswerSegmentID,
		&item.SubmissionPageID, &item.Type, &item.Geometry.CoordinateSpace,
		&item.Geometry.X, &item.Geometry.Y, &item.Geometry.Width, &item.Geometry.Height,
		&payload, &item.Content, &item.Visibility, &item.Revision,
		&item.CreatedBy, &item.UpdatedBy, &item.CreatedAt, &item.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return Annotation{}, ErrNotFound
	}
	if err != nil {
		return Annotation{}, err
	}
	if err := json.Unmarshal(payload, &item.Payload); err != nil {
		return Annotation{}, err
	}
	item.CreatedAt, item.UpdatedAt = item.CreatedAt.UTC(), item.UpdatedAt.UTC()
	return item, nil
}

func scanCommentTemplate(row scanner) (CommentTemplate, error) {
	var item CommentTemplate
	err := row.Scan(
		&item.ID, &item.TenantID, &item.OwnerID, &item.Title, &item.Content,
		&item.Shortcut, &item.UsageCount, &item.Revision, &item.CreatedAt, &item.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return CommentTemplate{}, ErrNotFound
	}
	if err != nil {
		return CommentTemplate{}, err
	}
	item.CreatedAt, item.UpdatedAt = item.CreatedAt.UTC(), item.UpdatedAt.UTC()
	return item, nil
}

func annotationColumns() string {
	return `id::text, tenant_id::text, review_task_id::text, answer_segment_id::text,
submission_page_id::text, annotation_type, coordinate_space, x::float8, y::float8,
width::float8, height::float8, payload, content, visibility, revision,
created_by::text, updated_by::text, created_at, updated_at`
}

func commentTemplateColumns() string {
	return `id::text, tenant_id::text, owner_id::text, title, content, shortcut,
usage_count, revision, created_at, updated_at`
}

func isUniqueViolation(err error) bool {
	var pgError *pgconn.PgError
	return errors.As(err, &pgError) && pgError.Code == "23505"
}
