package capture

import (
	"context"
	"encoding/json"
	"strings"
)

// 队列同时返回考试冻结名册和批次中的答卷，人工绑定只能从这份名册选择学生。
func (s *PostgresStore) GetMatchingQueue(ctx context.Context, tenantID, batchID string) (MatchingQueue, error) {
	var queue MatchingQueue
	queue.BatchID = batchID
	if err := s.db.QueryRowContext(ctx, `SELECT exam_id::text FROM capture_batch WHERE tenant_id=$1 AND id=$2::uuid AND deleted_at IS NULL`, tenantID, batchID).Scan(&queue.ExamID); err != nil {
		return MatchingQueue{}, mapNotFound(err)
	}
	rows, err := s.db.QueryContext(ctx, `SELECT student_id::text,student_no_snapshot,student_name_snapshot,class_id_snapshot::text,class_name_snapshot
FROM exam_candidate_snapshot
WHERE tenant_id=$1 AND exam_id=$2::uuid
ORDER BY class_name_snapshot,student_no_snapshot`, tenantID, queue.ExamID)
	if err != nil {
		return MatchingQueue{}, err
	}
	for rows.Next() {
		var x StudentCandidate
		if err = rows.Scan(&x.ID, &x.StudentNo, &x.Name, &x.ClassID, &x.ClassName); err != nil {
			rows.Close()
			return MatchingQueue{}, err
		}
		queue.Candidates = append(queue.Candidates, x)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return MatchingQueue{}, err
	}
	if err = rows.Close(); err != nil {
		return MatchingQueue{}, err
	}
	subs, err := s.db.QueryContext(ctx, `SELECT DISTINCT s.id::text,COALESCE(s.student_id::text,''),COALESCE(s.candidate_no,''),s.identity_status,s.identity_revision,s.identity_evidence
FROM capture_page cp JOIN submission s ON s.tenant_id=cp.tenant_id AND s.id=cp.submission_id
WHERE cp.tenant_id=$1 AND cp.capture_batch_id=$2::uuid AND cp.deleted_at IS NULL AND s.deleted_at IS NULL ORDER BY 1`, tenantID, batchID)
	if err != nil {
		return MatchingQueue{}, err
	}
	for subs.Next() {
		var x MatchingSubmission
		var evidence []byte
		if err = subs.Scan(&x.ID, &x.StudentID, &x.CandidateNo, &x.IdentityStatus, &x.IdentityRevision, &evidence); err != nil {
			subs.Close()
			return MatchingQueue{}, err
		}
		if err = json.Unmarshal(evidence, &x.IdentityEvidence); err != nil {
			subs.Close()
			return MatchingQueue{}, err
		}
		if x.IdentityEvidence == nil {
			x.IdentityEvidence = map[string]any{}
		}
		pages, pageErr := s.listSubmissionCapturePages(ctx, tenantID, batchID, x.ID)
		if pageErr != nil {
			subs.Close()
			return MatchingQueue{}, pageErr
		}
		x.Pages = pages
		queue.Submissions = append(queue.Submissions, x)
	}
	if err = subs.Err(); err != nil {
		subs.Close()
		return MatchingQueue{}, err
	}
	if err = subs.Close(); err != nil {
		return MatchingQueue{}, err
	}
	if queue.Candidates == nil {
		queue.Candidates = []StudentCandidate{}
	}
	if queue.Submissions == nil {
		queue.Submissions = []MatchingSubmission{}
	}
	return queue, nil
}

func (s *PostgresStore) listSubmissionCapturePages(ctx context.Context, tenantID, batchID, submissionID string) ([]Page, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+pageColumns+` FROM capture_page WHERE tenant_id=$1 AND capture_batch_id=$2::uuid AND submission_id=$3::uuid AND deleted_at IS NULL ORDER BY sequence_no`, tenantID, batchID, submissionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Page{}
	for rows.Next() {
		x, e := scanPage(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

func (s *PostgresStore) ConfirmStudentMatch(ctx context.Context, tenantID, submissionID, actorID string, input ConfirmStudentMatchInput) (MatchingSubmission, error) {
	if input.StudentID == "" || input.Revision <= 0 {
		return MatchingSubmission{}, ErrInvalidInput
	}
	return s.applyStudentIdentity(ctx, tenantID, submissionID, actorID, input.Revision, "matched", input.StudentID, input.Reason)
}

func (s *PostgresStore) MarkStudentUnknown(ctx context.Context, tenantID, submissionID, actorID string, input MarkStudentUnknownInput) (MatchingSubmission, error) {
	if input.Revision <= 0 || strings.TrimSpace(input.Reason) == "" {
		return MatchingSubmission{}, ErrInvalidInput
	}
	return s.applyStudentIdentity(ctx, tenantID, submissionID, actorID, input.Revision, "unknown", "", input.Reason)
}

func (s *PostgresStore) applyStudentIdentity(ctx context.Context, tenantID, submissionID, actorID string, revision int, status, studentID, reason string) (MatchingSubmission, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return MatchingSubmission{}, err
	}
	defer tx.Rollback()
	var examID, batchID, currentStatus string
	var currentRevision int
	err = tx.QueryRowContext(ctx, `SELECT s.exam_id::text,cp.capture_batch_id::text,s.identity_status,s.identity_revision FROM submission s JOIN capture_page cp ON cp.tenant_id=s.tenant_id AND cp.submission_id=s.id AND cp.deleted_at IS NULL WHERE s.tenant_id=$1 AND s.id=$2::uuid AND s.deleted_at IS NULL LIMIT 1 FOR UPDATE OF s`, tenantID, submissionID).Scan(&examID, &batchID, &currentStatus, &currentRevision)
	if err != nil {
		return MatchingSubmission{}, mapNotFound(err)
	}
	if err = ensureBatchWritableTx(ctx, tx, tenantID, batchID); err != nil {
		return MatchingSubmission{}, err
	}
	if currentRevision != revision {
		return MatchingSubmission{}, ErrConflict
	}
	candidateNo := ""
	candidateName := ""
	// 人工绑定也必须来自本次考试冻结名册，不能直接接受当前学籍中的任意学生。
	if status == "matched" {
		err = tx.QueryRowContext(ctx, `SELECT student_no_snapshot,student_name_snapshot FROM exam_candidate_snapshot WHERE tenant_id=$1 AND exam_id=$2::uuid AND student_id=$3::uuid`, tenantID, examID, studentID).Scan(&candidateNo, &candidateName)
		if err != nil {
			return MatchingSubmission{}, mapNotFound(err)
		}
	}
	evidence, _ := json.Marshal(map[string]any{"method": "manual_confirmation", "actor_id": actorID, "reason": strings.TrimSpace(reason), "student_name": candidateName})
	var out MatchingSubmission
	var raw []byte
	err = tx.QueryRowContext(ctx, `UPDATE submission SET student_id=NULLIF($3,'')::uuid,candidate_no=NULLIF($4,''),identity_status=$5,identity_revision=identity_revision+1,identity_evidence=$6,updated_at=now() WHERE tenant_id=$1 AND id=$2::uuid RETURNING id::text,COALESCE(student_id::text,''),COALESCE(candidate_no,''),identity_status,identity_revision,identity_evidence`, tenantID, submissionID, studentID, candidateNo, status, evidence).Scan(&out.ID, &out.StudentID, &out.CandidateNo, &out.IdentityStatus, &out.IdentityRevision, &raw)
	if err != nil {
		if strings.Contains(err.Error(), "uq_submission_exam_student_active") || strings.Contains(err.Error(), "idx_submission_candidate") {
			return MatchingSubmission{}, ErrConflict
		}
		return MatchingSubmission{}, err
	}
	_ = json.Unmarshal(raw, &out.IdentityEvidence)
	before, _ := json.Marshal(map[string]any{"identity_status": currentStatus, "identity_revision": currentRevision})
	after, _ := json.Marshal(out)
	_, err = tx.ExecContext(ctx, `INSERT INTO capture_operation(tenant_id,capture_batch_id,operation,target_type,target_id,actor_id,reason,before_state,after_state) VALUES($1,$2::uuid,'student_match','submission',$3::uuid,$4::uuid,$5,$6,$7)`, tenantID, batchID, submissionID, actorID, reason, before, after)
	if err != nil {
		return MatchingSubmission{}, err
	}
	if err = s.aggregateBatchTx(ctx, tx, tenantID, batchID); err != nil {
		return MatchingSubmission{}, err
	}
	if err = tx.Commit(); err != nil {
		return MatchingSubmission{}, err
	}
	return out, nil
}

func (s *PostgresStore) ConfirmPageMatch(ctx context.Context, tenantID, pageID, actorID string, input ConfirmPageMatchInput) (Page, error) {
	if input.Revision <= 0 || input.PageNo <= 0 {
		return Page{}, ErrInvalidInput
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Page{}, err
	}
	defer tx.Rollback()
	current, err := scanPage(tx.QueryRowContext(ctx, `SELECT `+pageColumns+` FROM capture_page WHERE tenant_id=$1 AND id=$2::uuid AND deleted_at IS NULL FOR UPDATE`, tenantID, pageID))
	if err != nil {
		return Page{}, err
	}
	if err = ensureBatchWritableTx(ctx, tx, tenantID, current.CaptureBatchID); err != nil {
		return Page{}, err
	}
	if current.Revision != input.Revision {
		return Page{}, ErrConflict
	}
	out, err := scanPage(tx.QueryRowContext(ctx, `UPDATE capture_page SET assigned_page_no=$3::int,revision=revision+1,manual_override=jsonb_build_object('page_no',$3::int,'actor_id',$4::text,'reason',$5::text),page_identity=page_identity||jsonb_build_object('barcode_status','manual_override'),status=CASE WHEN status='needs_review' THEN 'normalized' ELSE status END,updated_at=now() WHERE tenant_id=$1 AND id=$2::uuid RETURNING `+pageColumns, tenantID, pageID, input.PageNo, actorID, strings.TrimSpace(input.Reason)))
	if err != nil {
		return Page{}, err
	}
	// 页码决定模板坐标，人工改页码后原配准和切片都失效，必须重新处理。
	if _, err = tx.ExecContext(ctx, `UPDATE page_registration_run SET processing_status='invalidated',updated_at=now() WHERE tenant_id=$1 AND capture_page_id=$2::uuid AND processing_status<>'invalidated'`, tenantID, pageID); err != nil {
		return Page{}, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE answer_segment SET processing_status='invalidated',status='needs_manual_review',updated_at=now() WHERE tenant_id=$1 AND registration_run_id IN(SELECT id FROM page_registration_run WHERE tenant_id=$1 AND capture_page_id=$2::uuid)`, tenantID, pageID); err != nil {
		return Page{}, err
	}
	if err = s.aggregateBatchTx(ctx, tx, tenantID, current.CaptureBatchID); err != nil {
		return Page{}, err
	}
	if err = tx.Commit(); err != nil {
		return Page{}, err
	}
	return out, nil
}
