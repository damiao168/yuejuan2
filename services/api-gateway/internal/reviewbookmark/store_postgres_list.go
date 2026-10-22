package reviewbookmark

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"

	"edugrade-enterprise/services/api-gateway/internal/auth"
)

// 普通任务与暗样本只在内部合并。授权集合先形成，清单、计数、筛选项都不能看到失权来源。
// 同校关系来自实时账号与考试，而不是活动任务派生的 AccessScope。
const authorizedBookmarkCTE = `WITH owner_access AS (
 SELECT u.id,u.tenant_id,u.school_id FROM app_user u
 WHERE u.tenant_id=$1::uuid AND u.id=$2::uuid AND u.status='active' AND u.deleted_at IS NULL
 AND u.school_id IS NOT NULL AND EXISTS (
  SELECT 1 FROM user_role ur JOIN role r ON r.tenant_id=ur.tenant_id AND r.id=ur.role_id AND r.deleted_at IS NULL
  JOIN role_permission rp ON rp.tenant_id=r.tenant_id AND rp.role_id=r.id AND rp.deleted_at IS NULL
  JOIN permission p ON p.tenant_id=rp.tenant_id AND p.id=rp.permission_id AND p.deleted_at IS NULL AND p.code='review:work'
  WHERE ur.tenant_id=u.tenant_id AND ur.user_id=u.id AND ur.deleted_at IS NULL)
), authorized AS (
 SELECT b.id,b.task_id,b.reason,b.revision,b.marked_at,e.id AS exam_id,e.name AS exam_name,
 t.question_id,t.question_no,e.subject,t.anonymous_code,
 CASE WHEN t.status IN ('submitted','completed') THEN 'submitted' ELSE 'processable' END AS state,
 (e.archived_at IS NOT NULL OR e.status='archived' OR es.archived_at IS NOT NULL OR es.status='archived') AS archived
 FROM review_bookmark b JOIN owner_access u ON u.tenant_id=b.tenant_id AND u.id=b.owner_id
 JOIN review_task t ON t.tenant_id=b.tenant_id AND t.id=b.review_task_id AND t.deleted_at IS NULL AND t.assigned_to=u.id
 JOIN exam e ON e.tenant_id=t.tenant_id AND e.id=t.exam_id AND e.deleted_at IS NULL AND e.school_id=u.school_id
 JOIN school sch ON sch.tenant_id=e.tenant_id AND sch.id=e.school_id AND sch.status='active' AND sch.deleted_at IS NULL
 JOIN question q ON q.tenant_id=t.tenant_id AND q.id=t.question_id AND q.exam_id=e.id AND q.deleted_at IS NULL
 JOIN answer_segment a ON a.tenant_id=t.tenant_id AND a.id=t.answer_segment_id AND a.question_id=q.id AND a.submission_id=t.submission_id AND a.deleted_at IS NULL
 JOIN submission sub ON sub.tenant_id=t.tenant_id AND sub.id=t.submission_id AND sub.exam_id=e.id AND sub.deleted_at IS NULL
 LEFT JOIN exam_session es ON es.tenant_id=e.tenant_id AND es.id=e.exam_session_id
 WHERE b.deleted_at IS NULL AND NOT EXISTS(SELECT 1 FROM seed_task peer WHERE peer.tenant_id=t.tenant_id AND peer.id=t.id)
 AND (t.status IN ('assigned','in_progress','returned') OR
  (t.status IN ('submitted','completed') AND EXISTS (SELECT 1 FROM human_grade g WHERE g.tenant_id=t.tenant_id AND g.review_task_id=t.id AND g.reviewer_id=u.id AND g.deleted_at IS NULL)))
 UNION ALL
 SELECT b.id,b.task_id,b.reason,b.revision,b.marked_at,e.id,e.name,t.question_id,t.question_no,e.subject,t.anonymous_code,
 CASE WHEN t.status='completed' THEN 'submitted' ELSE 'processable' END,
 (e.archived_at IS NOT NULL OR e.status='archived' OR es.archived_at IS NOT NULL OR es.status='archived')
 FROM review_bookmark b JOIN owner_access u ON u.tenant_id=b.tenant_id AND u.id=b.owner_id
 JOIN seed_task t ON t.tenant_id=b.tenant_id AND t.id=b.seed_task_id AND t.grader_id=u.id
 JOIN exam e ON e.tenant_id=t.tenant_id AND e.id=t.exam_id AND e.deleted_at IS NULL AND e.school_id=u.school_id
 JOIN school sch ON sch.tenant_id=e.tenant_id AND sch.id=e.school_id AND sch.status='active' AND sch.deleted_at IS NULL
 JOIN question q ON q.tenant_id=t.tenant_id AND q.id=t.question_id AND q.exam_id=e.id AND q.deleted_at IS NULL
 JOIN answer_segment a ON a.tenant_id=t.tenant_id AND t.source_image_url='/api/v1/answer-segments/'||a.id::text||'/image' AND a.question_id=q.id AND a.deleted_at IS NULL
 JOIN submission sub ON sub.tenant_id=a.tenant_id AND sub.id=a.submission_id AND sub.exam_id=e.id AND sub.deleted_at IS NULL
 LEFT JOIN exam_session es ON es.tenant_id=e.tenant_id AND es.id=e.exam_session_id
 WHERE b.deleted_at IS NULL AND NOT EXISTS(SELECT 1 FROM review_task peer WHERE peer.tenant_id=t.tenant_id AND peer.id=t.id)
 AND (t.status='in_progress' OR (t.status='completed' AND EXISTS (
  SELECT 1 FROM seed_observation o WHERE o.tenant_id=t.tenant_id AND o.seed_task_id=t.id AND o.grader_id=u.id)))
 ) `

const bookmarkFilterSQL = ` ($3='' OR exam_id::text=$3) AND ($4='' OR question_id::text=$4) AND ($5='' OR subject=$5) AND ($6='all' OR state=$6) AND ($7='' OR strpos(lower(exam_name || ' ' || question_no || ' ' || anonymous_code),lower($7))>0) `

func (s *PostgresStore) List(ctx context.Context, user auth.User, filter ListFilter) (ListResult, error) {
	result := ListResult{Items: []ListItem{}, Exams: []ExamOption{}, Questions: []QuestionOption{}}
	if err := requireWork(user); err != nil {
		return result, err
	}
	filter, err := normalizeFilter(filter)
	if err != nil {
		return result, err
	}
	digest := filterDigest(user, filter)
	var cursor listCursor
	if filter.Cursor != "" {
		raw, e := base64.RawURLEncoding.DecodeString(filter.Cursor)
		if e != nil || json.Unmarshal(raw, &cursor) != nil || cursor.Filter != digest || cursor.ID == "" || cursor.At.IsZero() {
			return result, ErrInvalid
		}
	}
	// RR 保证本页、总数和选项来自同一授权快照，不能因查询之间转派而混入不一致事实。
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return result, err
	}
	defer tx.Rollback()
	args := []any{user.TenantID, user.ID, filter.ExamID, filter.QuestionID, filter.Subject, filter.State, filter.Q}
	if err = tx.QueryRowContext(ctx, authorizedBookmarkCTE+`SELECT count(*) FROM authorized WHERE `+bookmarkFilterSQL, args...).Scan(&result.TotalCount); err != nil {
		return result, err
	}
	rows, err := tx.QueryContext(ctx, authorizedBookmarkCTE+`SELECT DISTINCT exam_id::text,exam_name FROM authorized ORDER BY exam_id::text`, user.TenantID, user.ID)
	if err != nil {
		return result, err
	}
	for rows.Next() {
		var option ExamOption
		if err = rows.Scan(&option.ID, &option.Name); err != nil {
			rows.Close()
			return result, err
		}
		result.Exams = append(result.Exams, option)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return result, err
	}
	rows, err = tx.QueryContext(ctx, authorizedBookmarkCTE+`SELECT DISTINCT question_id::text,question_no FROM authorized WHERE ($3='' OR exam_id::text=$3) ORDER BY question_id::text`, user.TenantID, user.ID, filter.ExamID)
	if err != nil {
		return result, err
	}
	for rows.Next() {
		var option QuestionOption
		if err = rows.Scan(&option.ID, &option.QuestionNo); err != nil {
			rows.Close()
			return result, err
		}
		result.Questions = append(result.Questions, option)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return result, err
	}
	var at any
	var id string
	if filter.Cursor != "" {
		at = cursor.At
		id = cursor.ID
	}
	args = append(args, at, id, filter.Limit+1)
	rows, err = tx.QueryContext(ctx, authorizedBookmarkCTE+`SELECT id::text,task_id::text,reason,revision,marked_at,exam_id::text,exam_name,question_id::text,question_no,subject,anonymous_code,state,COALESCE(archived,false) FROM authorized WHERE `+bookmarkFilterSQL+` AND ($8::timestamptz IS NULL OR (marked_at,id::text)<($8::timestamptz,$9)) ORDER BY marked_at DESC,id::text DESC LIMIT $10`, args...)
	if err != nil {
		return result, err
	}
	for rows.Next() {
		var item ListItem
		var bookmarkID string
		var archived bool
		if err = rows.Scan(&bookmarkID, &item.TaskID, &item.Reason, &item.Revision, &item.MarkedAt, &item.ExamID, &item.ExamName, &item.QuestionID, &item.QuestionNo, &item.Subject, &item.AnonymousCode, &item.State, &archived); err != nil {
			rows.Close()
			return result, err
		}
		if len(result.Items) == filter.Limit {
			last := result.Items[len(result.Items)-1]
			raw, _ := json.Marshal(listCursor{At: *last.MarkedAt, ID: *last.ID, Filter: digest})
			result.NextCursor = base64.RawURLEncoding.EncodeToString(raw)
			break
		}
		item.ID = &bookmarkID
		item.Marked = true
		item.CanEditReason = !archived
		result.Items = append(result.Items, item)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return result, err
	}
	return result, tx.Commit()
}

func (s *PostgresStore) Cleanup(ctx context.Context, user auth.User) ([]CleanupItem, error) {
	items := []CleanupItem{}
	if err := requireAccount(user); err != nil {
		return items, err
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return items, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, authorizedBookmarkCTE+`SELECT b.id::text,b.revision FROM review_bookmark b WHERE b.tenant_id=$1::uuid AND b.owner_id=$2::uuid AND b.deleted_at IS NULL AND NOT EXISTS(SELECT 1 FROM authorized a WHERE a.id=b.id) ORDER BY b.id`, user.TenantID, user.ID)
	if err != nil {
		return items, err
	}
	for rows.Next() {
		var item CleanupItem
		if err = rows.Scan(&item.BookmarkID, &item.Revision); err != nil {
			rows.Close()
			return items, err
		}
		items = append(items, item)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return items, err
	}
	return items, tx.Commit()
}
