package capture

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
)

type collectionQueryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func (s *PostgresStore) GetCollectionSummary(ctx context.Context, tenantID, examID string) (CollectionSummary, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return CollectionSummary{}, err
	}
	defer tx.Rollback()
	out, err := collectionSummaryTx(ctx, tx, tenantID, examID)
	if err != nil {
		return out, err
	}
	return out, tx.Commit()
}

func collectionSummaryTx(ctx context.Context, q collectionQueryer, tenantID, examID string) (CollectionSummary, error) {
	out := CollectionSummary{ExamID: examID, Entries: []CollectionEntry{}, Issues: []CollectionIssue{}, Blockers: []string{}}
	err := q.QueryRowContext(ctx, `SELECT e.status,COALESCE(c.revision,0),c.completed_at,COALESCE(c.completed_by::text,''),
 COALESCE((SELECT t.page_count FROM exam_answer_sheet_template_binding b JOIN answer_sheet_template t ON t.tenant_id=b.tenant_id AND t.id=b.template_id WHERE b.tenant_id=e.tenant_id AND b.exam_id=e.id AND t.status='locked' AND t.deleted_at IS NULL),
 (SELECT max(t.page_count) FROM answer_sheet_template t WHERE t.tenant_id=e.tenant_id AND t.exam_id=e.id AND t.status='locked' AND t.deleted_at IS NULL),0)
 FROM exam e LEFT JOIN capture_collection c ON c.tenant_id=e.tenant_id AND c.exam_id=e.id WHERE e.tenant_id=$1 AND e.id=$2::uuid AND e.deleted_at IS NULL`, tenantID, examID).Scan(&out.ExamStatus, &out.Revision, &out.CompletedAt, &out.CompletedBy, &out.PagesPerStudent)
	if err != nil {
		return out, mapNotFound(err)
	}
	out.Closed = out.CompletedAt != nil
	// 冻结名册决定人数；实际页号来自有效页面，不能用文件数或 submission 的缓存页数冒充完整性。
	rows, err := q.QueryContext(ctx, `SELECT c.student_id::text,c.student_no_snapshot,c.student_name_snapshot,c.class_name_snapshot,
 COALESCE(a.status,'expected'),COALESCE(a.reason,''),COALESCE(s.n,0),COALESCE(s.id::text,''),COALESCE(s.identity_status,''),COALESCE(s.quality_status,''),
 COALESCE(p.batch_id,''),COALESCE(p.pages,0),COALESCE(p.ready,0),COALESCE(p.bad,0),COALESCE(p.numbers,0),COALESCE(p.min_no,0),COALESCE(p.max_no,0),COALESCE(p.group_bad,false)
 FROM exam_candidate_snapshot c
 LEFT JOIN exam_student_attendance a ON a.tenant_id=c.tenant_id AND a.exam_id=c.exam_id AND a.student_id=c.student_id AND a.deleted_at IS NULL
 LEFT JOIN LATERAL(SELECT x.*,count(*) OVER() n FROM submission x WHERE x.tenant_id=c.tenant_id AND x.exam_id=c.exam_id AND x.student_id=c.student_id AND x.deleted_at IS NULL ORDER BY x.created_at DESC,x.id LIMIT 1)s ON true
 LEFT JOIN LATERAL(SELECT min(cp.capture_batch_id::text) batch_id,count(*) pages,count(*) FILTER(WHERE cp.status='ready') ready,
 count(*) FILTER(WHERE cp.status IN ('needs_review','quality_rejected','failed')) bad,count(DISTINCT cp.assigned_page_no) numbers,min(cp.assigned_page_no) min_no,max(cp.assigned_page_no) max_no,
 bool_or(COALESCE(cp.page_identity->>'grouping_status','complete')<>'complete') group_bad
 FROM capture_page cp JOIN capture_file f ON f.tenant_id=cp.tenant_id AND f.id=cp.capture_file_id AND f.deleted_at IS NULL
 JOIN submission_page sp ON sp.tenant_id=cp.tenant_id AND sp.id=cp.submission_page_id AND sp.deleted_at IS NULL AND sp.page_no=cp.assigned_page_no
 WHERE cp.tenant_id=c.tenant_id AND cp.submission_id=s.id AND cp.deleted_at IS NULL AND cp.status<>'deleted')p ON true
 WHERE c.tenant_id=$1 AND c.exam_id=$2::uuid ORDER BY c.class_name_snapshot,c.student_no_snapshot,c.student_id`, tenantID, examID)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var x CollectionEntry
		var attendance, identity, quality string
		var n, ready, bad, numbers, minNo, maxNo int
		var groupBad bool
		if err = rows.Scan(&x.StudentID, &x.StudentNo, &x.StudentName, &x.ClassName, &attendance, &x.Reason, &n, &x.SubmissionID, &identity, &quality, &x.BatchID, &x.ActualPages, &ready, &bad, &numbers, &minNo, &maxNo, &groupBad); err != nil {
			rows.Close()
			return out, err
		}
		x.ExpectedPages = out.PagesPerStudent
		x.Status, x.Code = classifyCollectionEntry(attendance, n, identity, quality, x.ActualPages, x.ExpectedPages, ready, bad, numbers, minNo, maxNo, groupBad)
		out.Entries = append(out.Entries, x)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	issueRows, err := q.QueryContext(ctx, `SELECT f.id::text,'file',CASE WHEN f.status='failed' THEN 'file_failed' ELSE 'file_processing' END,f.original_name,b.id::text,'',f.id::text
 FROM capture_file f JOIN capture_batch b ON b.tenant_id=f.tenant_id AND b.id=f.capture_batch_id WHERE b.tenant_id=$1 AND b.exam_id=$2::uuid AND b.deleted_at IS NULL AND b.status<>'cancelled' AND f.deleted_at IS NULL AND f.status IN ('uploaded','queued','processing','failed')
 UNION ALL SELECT u.id::text,'upload','upload_incomplete',u.original_name,b.id::text,'','' FROM capture_upload_session u JOIN capture_batch b ON b.tenant_id=u.tenant_id AND b.id=u.capture_batch_id WHERE u.tenant_id=$1 AND u.exam_id=$2::uuid AND u.status<>'completed' AND b.status<>'cancelled' AND b.deleted_at IS NULL
 UNION ALL SELECT s.id::text,'submission',CASE WHEN s.student_id IS NULL THEN 'student_unidentified' ELSE 'student_not_in_roster' END,COALESCE(s.candidate_no,'未匹配答卷'),COALESCE((SELECT min(cp.capture_batch_id::text) FROM capture_page cp WHERE cp.tenant_id=s.tenant_id AND cp.submission_id=s.id AND cp.deleted_at IS NULL AND cp.status<>'deleted'),''),s.id::text,''
 FROM submission s WHERE s.tenant_id=$1 AND s.exam_id=$2::uuid AND s.deleted_at IS NULL AND NOT EXISTS(SELECT 1 FROM exam_candidate_snapshot c WHERE c.tenant_id=s.tenant_id AND c.exam_id=s.exam_id AND c.student_id=s.student_id)
 UNION ALL SELECT min(f.id::text),'batch','upload_group_incomplete','本次选择的文件尚未完整登记',b.id::text,'',min(f.id::text) FROM capture_file f JOIN capture_batch b ON b.tenant_id=f.tenant_id AND b.id=f.capture_batch_id WHERE b.tenant_id=$1 AND b.exam_id=$2::uuid AND b.status<>'cancelled' AND b.deleted_at IS NULL AND f.deleted_at IS NULL AND f.upload_group_id IS NOT NULL GROUP BY b.id,f.upload_group_id HAVING count(*)<max(f.upload_group_file_count)
 UNION ALL SELECT t.id::text,'batch','task_processing','导入任务尚在运行',b.id::text,'','' FROM agent_worker_task t JOIN capture_batch b ON b.tenant_id=t.tenant_id AND b.exam_id=$2::uuid AND b.status<>'cancelled' AND b.deleted_at IS NULL WHERE t.tenant_id=$1 AND t.status IN ('queued','leased','running') AND (
 (t.source_type='capture_file' AND EXISTS(SELECT 1 FROM capture_file f WHERE f.tenant_id=t.tenant_id AND f.id=t.source_id AND f.capture_batch_id=b.id AND f.deleted_at IS NULL)) OR
 (t.source_type='page_registration_run' AND EXISTS(SELECT 1 FROM page_registration_run r JOIN capture_page p ON p.tenant_id=r.tenant_id AND p.id=r.capture_page_id WHERE r.tenant_id=t.tenant_id AND r.id=t.source_id AND p.capture_batch_id=b.id AND p.deleted_at IS NULL AND p.status<>'deleted')) OR
 (t.source_type='page_template_match_run' AND EXISTS(SELECT 1 FROM page_template_match_run r JOIN capture_page p ON p.tenant_id=r.tenant_id AND p.id=r.capture_page_id WHERE r.tenant_id=t.tenant_id AND r.id=t.source_id AND p.capture_batch_id=b.id AND p.deleted_at IS NULL AND p.status<>'deleted')))
 ORDER BY 2,1`, tenantID, examID)
	if err != nil {
		return out, err
	}
	defer issueRows.Close()
	for issueRows.Next() {
		var x CollectionIssue
		if err = issueRows.Scan(&x.ID, &x.Kind, &x.Code, &x.Name, &x.BatchID, &x.SubmissionID, &x.FileID); err != nil {
			return out, err
		}
		out.Issues = append(out.Issues, x)
	}
	if err = issueRows.Err(); err != nil {
		return out, err
	}
	finishCollectionSummary(&out)
	return out, nil
}

func classifyCollectionEntry(attendance string, n int, identity, quality string, pages, expected, ready, bad, numbers, minNo, maxNo int, groupBad bool) (string, string) {
	switch {
	case attendance == "absent" && n == 0:
		return "absent", "absent"
	case attendance == "absent":
		return "needs_attention", "absent_has_submission"
	case n == 0:
		return "missing", "missing_submission"
	case n > 1:
		return "needs_attention", "duplicate_submission"
	case identity != "matched":
		return "needs_attention", "student_unidentified"
	case groupBad:
		return "needs_attention", "grouping_incomplete"
	case expected <= 0:
		return "needs_attention", "template_missing"
	case pages != expected || numbers != expected || minNo != 1 || maxNo != expected:
		return "needs_attention", "missing_pages"
	case quality == "failed" || bad > 0:
		return "needs_attention", "page_needs_review"
	case ready != expected:
		return "processing", "page_processing"
	default:
		return "ready", "import_complete"
	}
}

func (s *PostgresStore) CompleteCollection(ctx context.Context, tenantID, examID, actorID string, input CompleteCollectionInput) (CollectionSummary, error) {
	input.IdempotencyKey = strings.TrimSpace(input.IdempotencyKey)
	input.Reason = strings.TrimSpace(input.Reason)
	if input.IdempotencyKey == "" || len(input.IdempotencyKey) > 200 || input.Revision < 0 {
		return CollectionSummary{}, ErrInvalidInput
	}
	request, _ := json.Marshal(struct {
		Actor string
		Input CompleteCollectionInput
	}{actorID, input})
	digest := sha256.Sum256(request)
	requestHash := hex.EncodeToString(digest[:])
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return CollectionSummary{}, err
	}
	defer tx.Rollback()
	// 出勤和批次创建先锁考试；结束也采用相同顺序，避免与它们互相等待。
	var lockedExam string
	if err = tx.QueryRowContext(ctx, `SELECT id::text FROM exam WHERE tenant_id=$1 AND id=$2::uuid AND deleted_at IS NULL FOR UPDATE NOWAIT`, tenantID, examID).Scan(&lockedExam); err != nil {
		return CollectionSummary{}, ErrConflict
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO capture_collection(tenant_id,exam_id) VALUES($1,$2::uuid) ON CONFLICT DO NOTHING`, tenantID, examID); err != nil {
		return CollectionSummary{}, err
	}
	var revision int
	if err = tx.QueryRowContext(ctx, `SELECT revision FROM capture_collection WHERE tenant_id=$1 AND exam_id=$2::uuid FOR UPDATE`, tenantID, examID).Scan(&revision); err != nil {
		return CollectionSummary{}, err
	}
	// 网络重试先恢复原命令；后来的评分或补传不能让已成功的命令变成版本冲突。
	var priorHash string
	var result []byte
	err = tx.QueryRowContext(ctx, `SELECT request_hash,result FROM capture_collection_completion WHERE tenant_id=$1 AND exam_id=$2::uuid AND command_id=$3`, tenantID, examID, input.IdempotencyKey).Scan(&priorHash, &result)
	if err == nil {
		if priorHash != requestHash {
			return CollectionSummary{}, ErrConflict
		}
		var out CollectionSummary
		err = json.Unmarshal(result, &out)
		return out, err
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return CollectionSummary{}, err
	}
	if revision != input.Revision {
		return CollectionSummary{}, ErrConflict
	}
	// 老写入路径可能已持有批次锁；NOWAIT 让结束操作立即重试，避免与台账锁形成互等。
	rows, err := tx.QueryContext(ctx, `SELECT id FROM capture_batch WHERE tenant_id=$1 AND exam_id=$2::uuid AND deleted_at IS NULL ORDER BY id FOR UPDATE NOWAIT`, tenantID, examID)
	if err != nil {
		return CollectionSummary{}, ErrConflict
	}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return CollectionSummary{}, err
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return CollectionSummary{}, err
	}
	out, err := collectionSummaryTx(ctx, tx, tenantID, examID)
	if err != nil {
		return out, err
	}
	if !out.CanComplete {
		return out, ErrInvalidTransition
	}
	// 就绪批次统一封存；完全没有有效材料的批次取消，不强迫用户完成空批次。
	_, err = tx.ExecContext(ctx, `UPDATE capture_batch b SET status=CASE WHEN EXISTS(SELECT 1 FROM capture_file f WHERE f.tenant_id=b.tenant_id AND f.capture_batch_id=b.id AND f.deleted_at IS NULL AND f.status<>'duplicate') THEN 'completed' ELSE 'cancelled' END,completed_at=now(),revision=revision+1,updated_at=now() WHERE b.tenant_id=$1 AND b.exam_id=$2::uuid AND b.deleted_at IS NULL AND b.status NOT IN ('completed','cancelled')`, tenantID, examID)
	if err != nil {
		return out, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE capture_collection SET completed_at=now(),completed_by=$3::uuid,revision=revision+1 WHERE tenant_id=$1 AND exam_id=$2::uuid`, tenantID, examID, actorID); err != nil {
		return out, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE exam SET status='grading',revision=revision+1,updated_at=now() WHERE tenant_id=$1 AND id=$2::uuid AND status='collecting'`, tenantID, examID); err != nil {
		return out, err
	}
	out, err = collectionSummaryTx(ctx, tx, tenantID, examID)
	if err != nil {
		return out, err
	}
	result, _ = json.Marshal(out)
	if _, err = tx.ExecContext(ctx, `INSERT INTO capture_collection_completion(tenant_id,exam_id,command_id,request_hash,actor_id,result) VALUES($1,$2::uuid,$3,$4,$5::uuid,$6)`, tenantID, examID, input.IdempotencyKey, requestHash, actorID, result); err != nil {
		return out, err
	}
	return out, tx.Commit()
}
