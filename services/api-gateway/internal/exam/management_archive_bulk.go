package exam

import (
	"context"
	"database/sql"
	"encoding/json"

	"edugrade-enterprise/services/api-gateway/internal/auth"
)

// checkManagementArchivesTx 共用列表快照，运行任务的多级来源只追溯一次。
// 仅为有整场权限且尚未归档的对象检查任务，结果不会暴露其他学校或隐藏学科。
func checkManagementArchivesTx(ctx context.Context, tx *sql.Tx, tenantID string, items []ManagementExam) (map[string]ManagementArchiveCheck, error) {
	checks := map[string]ManagementArchiveCheck{}
	ids := []string{}
	for _, item := range items {
		check := baseManagementArchiveCheck(item)
		checks[item.Kind+":"+item.ID] = check
		if len(check.Blockers) == 0 {
			for _, child := range item.Exams {
				ids = append(ids, child.ID)
			}
		}
	}
	active := map[string]bool{}
	if len(ids) > 0 {
		raw, err := json.Marshal(normalizedClassIDs(ids))
		if err != nil {
			return nil, err
		}
		rows, err := tx.QueryContext(ctx, managementArchiveRuntimeActiveSQL, tenantID, string(raw))
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return nil, err
			}
			active[id] = true
		}
		rowErr := rows.Err()
		rows.Close()
		if rowErr != nil {
			return nil, rowErr
		}
	}
	for _, item := range items {
		key := item.Kind + ":" + item.ID
		if len(checks[key].Blockers) > 0 {
			continue
		}
		check, err := checkManagementArchiveWithActiveTx(ctx, tx, tenantID, item, active)
		if err != nil {
			return nil, err
		}
		checks[key] = check
	}
	return checks, nil
}

func (s *PostgresStore) CheckManagementArchives(ctx context.Context, scope auth.AccessScope, items []ManagementExam) (map[string]ManagementArchiveCheck, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	current := []ManagementExam{}
	for _, item := range items {
		fresh, err := s.loadManagementExamTx(ctx, tx, scope, item.Kind, item.ID, false)
		if err != nil {
			return nil, err
		}
		current = append(current, fresh)
	}
	checks, err := checkManagementArchivesTx(ctx, tx, scope.TenantID, current)
	if err != nil {
		return nil, err
	}
	return checks, tx.Commit()
}

func (s *MemoryStore) CheckManagementArchives(_ context.Context, scope auth.AccessScope, items []ManagementExam) (map[string]ManagementArchiveCheck, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	checks := map[string]ManagementArchiveCheck{}
	for _, item := range items {
		current, err := s.managementExamLocked(scope, item.Kind, item.ID)
		if err != nil {
			return nil, err
		}
		checks[item.Kind+":"+item.ID] = s.checkManagementArchiveLocked(current)
	}
	return checks, nil
}

// MATERIALIZED 保证每类运行任务只做一次来源追溯，不随本页的整场数重复。
// 无外键的来源沿迁移中白名单追溯；新增来源必须同步白名单，查询失败不能当成没有活跃任务。
const managementArchiveRuntimeActiveSQL = `WITH active AS MATERIALIZED (
 SELECT unnest(exam_archive_row_exams('agent_worker_task'::regclass,to_jsonb(t))) AS exam_id
 FROM agent_worker_task t WHERE t.tenant_id=$1::uuid AND t.status NOT IN ('succeeded','cancelled')
 UNION ALL
 SELECT unnest(exam_archive_row_exams('subjective_grading_run'::regclass,to_jsonb(t)))
 FROM subjective_grading_run t WHERE t.tenant_id=$1::uuid AND t.deleted_at IS NULL AND t.status<>'succeeded'
 AND NOT EXISTS(SELECT 1 FROM subjective_grading_batch b WHERE b.tenant_id=t.tenant_id AND b.id=t.batch_id AND b.status='cancelled')
 UNION ALL
 SELECT unnest(exam_archive_row_exams('orchestration_run'::regclass,to_jsonb(t)))
 FROM orchestration_run t WHERE t.tenant_id=$1::uuid AND t.deleted_at IS NULL AND t.status NOT IN ('completed','failed')
 UNION ALL
 SELECT unnest(exam_archive_row_exams('agent_task'::regclass,to_jsonb(t)))
 FROM agent_task t WHERE t.tenant_id=$1::uuid AND t.deleted_at IS NULL AND t.status NOT IN ('succeeded','failed')
 UNION ALL
 SELECT unnest(exam_archive_row_exams('subjective_grading_batch'::regclass,to_jsonb(t)))
 FROM subjective_grading_batch t WHERE t.tenant_id=$1::uuid AND t.deleted_at IS NULL AND t.status NOT IN ('completed','cancelled')
 UNION ALL
 SELECT unnest(exam_archive_row_exams('omr_run'::regclass,to_jsonb(t)))
 FROM omr_run t WHERE t.tenant_id=$1::uuid AND t.deleted_at IS NULL AND t.status NOT IN ('completed','invalidated')
 UNION ALL
 SELECT unnest(exam_archive_row_exams('page_registration_run'::regclass,to_jsonb(t)))
 FROM page_registration_run t WHERE t.tenant_id=$1::uuid AND t.deleted_at IS NULL AND t.processing_status NOT IN ('completed','invalidated')
 UNION ALL
 SELECT unnest(exam_archive_row_exams('page_template_match_run'::regclass,to_jsonb(t)))
 FROM page_template_match_run t WHERE t.tenant_id=$1::uuid AND t.deleted_at IS NULL AND t.processing_status<>'completed'
)
SELECT DISTINCT exam_id::text FROM active
WHERE exam_id IN (SELECT jsonb_array_elements_text($2::jsonb)::uuid)`
