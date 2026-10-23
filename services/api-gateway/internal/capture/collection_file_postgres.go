package capture

import (
	"context"
	"database/sql"
	"github.com/google/uuid"
	"strings"
)

// 仅供排除命令恢复时检查考试作用域，包含已排除记录；不能用于重新开放旧文件下载或处理。
func (s *PostgresStore) GetCollectionFileExam(ctx context.Context, tenantID, fileID string) (string, error) {
	var examID string
	err := s.db.QueryRowContext(ctx, `SELECT b.exam_id::text FROM capture_file f JOIN capture_batch b ON b.tenant_id=f.tenant_id AND b.id=f.capture_batch_id WHERE f.tenant_id=$1 AND f.id=$2::uuid AND b.deleted_at IS NULL`, tenantID, fileID).Scan(&examID)
	return examID, mapNotFound(err)
}

func (s *PostgresStore) ExcludeFile(ctx context.Context, tenantID, fileID, actorID string, input ExcludeFileInput) (Batch, error) {
	input.Reason = strings.TrimSpace(input.Reason)
	if input.Reason == "" || len(input.Reason) > 500 {
		return Batch{}, ErrInvalidInput
	}
	if input.ReplacementFileID != "" {
		if _, err := uuid.Parse(input.ReplacementFileID); err != nil {
			return Batch{}, ErrInvalidInput
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Batch{}, err
	}
	defer tx.Rollback()
	var batchID, groupID, priorReason, priorActor, priorReplacement string
	var excludedAt sql.NullTime
	err = tx.QueryRowContext(ctx, `SELECT capture_batch_id::text,COALESCE(upload_group_id::text,''),deleted_at,COALESCE(exclusion_reason,''),COALESCE(excluded_by::text,''),COALESCE(replaced_by_file_id::text,'') FROM capture_file WHERE tenant_id=$1 AND id=$2::uuid`, tenantID, fileID).Scan(&batchID, &groupID, &excludedAt, &priorReason, &priorActor, &priorReplacement)
	if err != nil {
		return Batch{}, mapNotFound(err)
	}
	// 绑定成功但响应丢失时恢复原结果；不同原因、替代目标或操作者不能复用这次处置。
	if excludedAt.Valid {
		if priorReason != input.Reason || priorActor != actorID || priorReplacement != input.ReplacementFileID {
			return Batch{}, ErrConflict
		}
		return scanBatch(tx.QueryRowContext(ctx, `SELECT `+batchColumns+` FROM capture_batch WHERE tenant_id=$1 AND id=$2::uuid AND deleted_at IS NULL`, tenantID, batchID))
	}
	if err = ensureBatchWritableTx(ctx, tx, tenantID, batchID); err != nil {
		return Batch{}, err
	}
	if input.ReplacementFileID != "" {
		var valid bool
		err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM capture_file f JOIN capture_batch b ON b.tenant_id=f.tenant_id AND b.id=f.capture_batch_id JOIN capture_batch old ON old.tenant_id=b.tenant_id AND old.exam_id=b.exam_id WHERE old.id=$2::uuid AND f.tenant_id=$1 AND f.id=$3::uuid AND f.id<>$4::uuid AND f.deleted_at IS NULL AND (NULLIF($5,'')::uuid IS NULL OR f.upload_group_id IS DISTINCT FROM NULLIF($5,'')::uuid))`, tenantID, batchID, input.ReplacementFileID, fileID, groupID).Scan(&valid)
		if err != nil {
			return Batch{}, err
		}
		if !valid {
			return Batch{}, ErrInvalidInput
		}
	}
	// 一次选择是完整分组单位；只移除其中一张会改变余下学生的分组，必须整体退出有效输入。
	rows, err := tx.QueryContext(ctx, `SELECT id::text FROM capture_file WHERE tenant_id=$1 AND capture_batch_id=$2::uuid AND deleted_at IS NULL AND (id=$3::uuid OR upload_group_id=NULLIF($4,'')::uuid) ORDER BY id FOR UPDATE`, tenantID, batchID, fileID, groupID)
	if err != nil {
		return Batch{}, err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return Batch{}, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return Batch{}, err
	}
	for _, id := range ids {
		segments, lockErr := tx.QueryContext(ctx, `SELECT a.id FROM answer_segment a JOIN capture_page p ON p.tenant_id=a.tenant_id AND p.submission_page_id=a.submission_page_id WHERE p.tenant_id=$1 AND p.capture_file_id=$2::uuid ORDER BY a.id FOR UPDATE OF a NOWAIT`, tenantID, id)
		if lockErr != nil {
			return Batch{}, ErrConflict
		}
		for segments.Next() {
			var segmentID string
			if lockErr = segments.Scan(&segmentID); lockErr != nil {
				segments.Close()
				return Batch{}, lockErr
			}
		}
		lockErr = segments.Err()
		segments.Close()
		if lockErr != nil {
			return Batch{}, lockErr
		}
		var graded bool
		err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM capture_page p JOIN answer_segment a ON a.tenant_id=p.tenant_id AND a.submission_page_id=p.submission_page_id WHERE p.tenant_id=$1 AND p.capture_file_id=$2::uuid AND (
   EXISTS(SELECT 1 FROM ai_grade g WHERE g.tenant_id=a.tenant_id AND g.answer_segment_id=a.id AND g.deleted_at IS NULL) OR
   EXISTS(SELECT 1 FROM human_grade g WHERE g.tenant_id=a.tenant_id AND g.answer_segment_id=a.id AND g.deleted_at IS NULL) OR
   EXISTS(SELECT 1 FROM question_grade g WHERE g.tenant_id=a.tenant_id AND g.answer_segment_id=a.id AND g.deleted_at IS NULL) OR
   EXISTS(SELECT 1 FROM final_grade g WHERE g.tenant_id=a.tenant_id AND g.answer_segment_id=a.id AND g.deleted_at IS NULL) OR
   EXISTS(SELECT 1 FROM subjective_grading_run g WHERE g.tenant_id=a.tenant_id AND g.answer_segment_id=a.id AND g.deleted_at IS NULL) OR
   EXISTS(SELECT 1 FROM scoring_run_item g WHERE g.tenant_id=a.tenant_id AND g.answer_segment_id=a.id)))`, tenantID, id).Scan(&graded)
		if err != nil {
			return Batch{}, err
		}
		if graded {
			return Batch{}, ErrFileHasGrades
		}
		// 排除页面和任务在同一事务内完成；旧租约回调只能被拒绝，不能把已排除的材料重新激活。
		if _, err = tx.ExecContext(ctx, `UPDATE agent_worker_task SET status='cancelled',lease_token=NULL,lease_expires_at=NULL,completed_at=now(),updated_at=now() WHERE tenant_id=$1 AND status IN ('queued','leased','running') AND ((source_type='capture_file' AND source_id=$2::uuid) OR (source_type='page_registration_run' AND source_id IN(SELECT r.id FROM page_registration_run r JOIN capture_page p ON p.tenant_id=r.tenant_id AND p.id=r.capture_page_id WHERE p.tenant_id=$1 AND p.capture_file_id=$2::uuid)) OR (source_type='page_template_match_run' AND source_id IN(SELECT r.id FROM page_template_match_run r JOIN capture_page p ON p.tenant_id=r.tenant_id AND p.id=r.capture_page_id WHERE p.tenant_id=$1 AND p.capture_file_id=$2::uuid)))`, tenantID, id); err != nil {
			return Batch{}, err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE answer_segment SET processing_status='invalidated',deleted_at=now(),updated_at=now() WHERE tenant_id=$1 AND submission_page_id IN(SELECT submission_page_id FROM capture_page WHERE tenant_id=$1 AND capture_file_id=$2::uuid) AND deleted_at IS NULL`, tenantID, id); err != nil {
			return Batch{}, err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE submission_page SET deleted_at=now(),updated_at=now() WHERE tenant_id=$1 AND id IN(SELECT submission_page_id FROM capture_page WHERE tenant_id=$1 AND capture_file_id=$2::uuid) AND deleted_at IS NULL`, tenantID, id); err != nil {
			return Batch{}, err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE capture_page SET status='deleted',revision=revision+1,updated_at=now() WHERE tenant_id=$1 AND capture_file_id=$2::uuid AND deleted_at IS NULL`, tenantID, id); err != nil {
			return Batch{}, err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE capture_file SET exclusion_reason=$3,excluded_by=$4::uuid,replaced_by_file_id=NULLIF($5,'')::uuid,deleted_at=now(),updated_at=now() WHERE tenant_id=$1 AND id=$2::uuid`, tenantID, id, input.Reason, actorID, input.ReplacementFileID); err != nil {
			return Batch{}, err
		}
	}
	_, err = tx.ExecContext(ctx, `UPDATE submission s SET actual_page_count=(SELECT count(*) FROM submission_page p WHERE p.tenant_id=s.tenant_id AND p.submission_id=s.id AND p.deleted_at IS NULL),deleted_at=CASE WHEN NOT EXISTS(SELECT 1 FROM submission_page p WHERE p.tenant_id=s.tenant_id AND p.submission_id=s.id AND p.deleted_at IS NULL) THEN now() ELSE s.deleted_at END,updated_at=now() WHERE s.tenant_id=$1 AND s.id IN(SELECT p.submission_id FROM capture_page p WHERE p.tenant_id=$1 AND p.capture_batch_id=$2::uuid) AND s.deleted_at IS NULL`, tenantID, batchID)
	if err != nil {
		return Batch{}, err
	}
	if input.ReplacementFileID != "" {
		// 完整重传常包含旧组中未损坏的同一张图。旧组退出后释放新组的重复标记，不能让替代流程卡死。
		_, err = tx.ExecContext(ctx, `UPDATE capture_file target SET status='uploaded',error_code=NULL,updated_at=now()
 WHERE target.tenant_id=$1 AND target.deleted_at IS NULL AND target.status='duplicate'
 AND (target.id=$2::uuid OR target.upload_group_id=(SELECT upload_group_id FROM capture_file WHERE tenant_id=$1 AND id=$2::uuid))
 AND target.sha256 IN(SELECT sha256 FROM capture_file WHERE tenant_id=$1 AND (id=$3::uuid OR upload_group_id=NULLIF($4,'')::uuid))
 AND NOT EXISTS(SELECT 1 FROM capture_file other WHERE other.tenant_id=target.tenant_id AND other.capture_batch_id=target.capture_batch_id AND other.id<>target.id AND other.sha256=target.sha256 AND other.deleted_at IS NULL AND other.status IN ('uploaded','queued','processing','completed'))`, tenantID, input.ReplacementFileID, fileID, groupID)
		if err != nil {
			return Batch{}, err
		}
	}
	if err = aggregateBatchTx(ctx, tx, tenantID, batchID); err != nil {
		return Batch{}, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO capture_operation(tenant_id,capture_batch_id,operation,target_type,target_id,actor_id,reason,before_state,after_state) VALUES($1,$2::uuid,'delete','capture_file',$3::uuid,$4::uuid,$5,'{}',jsonb_build_object('replacement_file_id',$6::text,'upload_group_id',$7::text))`, tenantID, batchID, fileID, actorID, input.Reason, input.ReplacementFileID, groupID); err != nil {
		return Batch{}, err
	}
	out, err := scanBatch(tx.QueryRowContext(ctx, `SELECT `+batchColumns+` FROM capture_batch WHERE tenant_id=$1 AND id=$2::uuid`, tenantID, batchID))
	if err != nil {
		return Batch{}, err
	}
	return out, tx.Commit()
}

var _ CollectionStore = (*PostgresStore)(nil)
var _ collectionQueryer = (*sql.Tx)(nil)
