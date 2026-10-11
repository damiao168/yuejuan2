package exam

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"github.com/jackc/pgx/v5/pgconn"
)

// lockManagementArchiveSubjects 与正式成绩发布共用锁；事务持有到提交，阻止新任务和旧结果写回越过归档。
// 普通配置入口仍可能先锁业务行；遇到数据库死锁时整次回滚，由调用方刷新后重试。
func lockManagementArchiveSubjects(ctx context.Context, tx *sql.Tx, scope auth.AccessScope, kind, id string) error {
	if kind != "session" && kind != "standalone" {
		return ErrInvalidInput
	}
	query := `SELECT id::text FROM exam WHERE tenant_id=$1 AND deleted_at IS NULL AND `
	if kind == "session" {
		query += `exam_session_id::text=$2`
	} else {
		query += `id::text=$2 AND exam_session_id IS NULL`
	}
	rows, err := tx.QueryContext(ctx, query+` ORDER BY id`, scope.TenantID, id)
	if err != nil {
		return err
	}
	ids := []string{}
	for rows.Next() {
		var childID string
		if err := rows.Scan(&childID); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, childID)
	}
	rowErr := rows.Err()
	rows.Close()
	if rowErr != nil {
		return rowErr
	}
	for _, childID := range ids {
		if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1),hashtext($2))`, scope.TenantID, childID); err != nil {
			return err
		}
	}
	return nil
}

func (s *PostgresStore) CheckManagementArchive(ctx context.Context, scope auth.AccessScope, kind, id string) (ManagementArchiveCheck, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return ManagementArchiveCheck{}, err
	}
	defer tx.Rollback()
	item, err := s.loadManagementExamTx(ctx, tx, scope, kind, id, false)
	if err != nil {
		return ManagementArchiveCheck{}, err
	}
	check, err := checkManagementArchiveTx(ctx, tx, scope.TenantID, item)
	if err != nil {
		return ManagementArchiveCheck{}, err
	}
	return check, tx.Commit()
}

func baseManagementArchiveCheck(item ManagementExam) ManagementArchiveCheck {
	check := ManagementArchiveCheck{Blockers: []ManagementArchiveBlocker{}}
	if !item.CanManage {
		check.Blockers = append(check.Blockers, ManagementArchiveBlocker{Code: "whole_exam_permission_required", Message: "需要整场考试全部学科和参考班级的管理权限"})
	}
	if item.ArchivedAt != nil {
		check.Blockers = append(check.Blockers, ManagementArchiveBlocker{Code: "already_archived", Message: "整场考试已经归档，可在已归档列表恢复"})
	}
	if item.LegacyArchived {
		check.Blockers = append(check.Blockers, ManagementArchiveBlocker{Code: "legacy_archived", Message: "历史归档缺少原业务阶段，不能自动恢复"})
	}
	for _, id := range item.ArchivedSubjectIDs {
		check.Blockers = append(check.Blockers, ManagementArchiveBlocker{Code: "subject_already_archived", Message: "存在单独归档的学科，请先核实归档范围", ExamID: id})
	}
	for _, child := range item.Exams {
		if child.Status == "archived" {
			check.Blockers = append(check.Blockers, archiveSubjectBlocker(child, "legacy_archived", "历史归档学科缺少原业务阶段，不能自动归档或恢复整场考试"))
		}
	}
	return check
}

func archiveSubjectBlocker(child Exam, code, message string) ManagementArchiveBlocker {
	return ManagementArchiveBlocker{Code: code, Message: message, ExamID: child.ID, Subject: child.Subject}
}

// checkManagementArchiveTx 只依据已存业务事实判断；状态文字为 published 不等于存在正式发布成绩。
// 查询失败向上传递，不把缺失任务、申诉或发布信息当作已结束。
func checkManagementArchiveTx(ctx context.Context, tx *sql.Tx, tenantID string, item ManagementExam) (ManagementArchiveCheck, error) {
	checks, err := checkManagementArchivesTx(ctx, tx, tenantID, []ManagementExam{item})
	return checks[item.Kind+":"+item.ID], err
}

func checkManagementArchiveWithActiveTx(ctx context.Context, tx *sql.Tx, tenantID string, item ManagementExam, runtimeActive map[string]bool) (ManagementArchiveCheck, error) {
	check := baseManagementArchiveCheck(item)
	if len(check.Blockers) > 0 {
		return check, nil
	}
	allUnused, allPublished := true, true
	unpublished := []Exam{}
	for _, child := range item.Exams {
		var hasWork, published, active bool
		if err := tx.QueryRowContext(ctx, managementArchiveSubjectFactsSQL, tenantID, child.ID).Scan(&hasWork, &published, &active); err != nil {
			return ManagementArchiveCheck{}, err
		}
		active = active || runtimeActive[child.ID]
		allUnused = allUnused && !hasWork && (child.Status == "draft" || child.Status == "configured" || child.Status == "ready")
		allPublished = allPublished && published
		if !published {
			unpublished = append(unpublished, child)
		}
		if active {
			check.Blockers = append(check.Blockers, archiveSubjectBlocker(child, "unfinished_work", "仍有未结束的导入、处理、阅卷、复核、重评或申诉事项"))
		}
		windowOpen, err := managementArchiveWindowOpenTx(ctx, tx, tenantID, child.ID)
		if err != nil {
			return ManagementArchiveCheck{}, err
		}
		if windowOpen {
			check.Blockers = append(check.Blockers, archiveSubjectBlocker(child, "appeal_window_open", "正式成绩仍存在未关闭的申诉窗口"))
		}
	}
	appendOfficialReleaseArchiveBlockers(&check, unpublished, allUnused, allPublished)
	check.Allowed = len(check.Blockers) == 0 && len(item.Exams) > 0
	return check, nil
}

func appendOfficialReleaseArchiveBlockers(check *ManagementArchiveCheck, unpublished []Exam, allUnused, allPublished bool) {
	// 整场已开展且存在未发布学科时，保留原学科顺序追加正式发布阻挡项。
	if !allUnused && !allPublished {
		for _, child := range unpublished {
			check.Blockers = append(check.Blockers, archiveSubjectBlocker(child, "official_release_required", "整场考试已开展，需要全部学科完成正式成绩发布后归档"))
		}
	}
}

// 发现开放窗口后仍读完全部发布记录；后续损坏或查询错误不能被当作已核实的阻挡事实。
func managementArchiveWindowOpenTx(ctx context.Context, tx *sql.Tx, tenantID, examID string) (bool, error) {
	rows, err := tx.QueryContext(ctx, `SELECT appeal_window FROM score_release WHERE tenant_id=$1 AND exam_id=$2::uuid AND status='published'`, tenantID, examID)
	if err != nil {
		return false, err
	}
	windowOpen := false
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			rows.Close()
			return false, err
		}
		closed, err := managementArchiveAppealWindowClosed(raw, time.Now().UTC())
		if err != nil {
			rows.Close()
			return false, err
		}
		windowOpen = windowOpen || !closed
	}
	rowErr := rows.Err()
	rows.Close()
	if rowErr != nil {
		return false, rowErr
	}
	return windowOpen, nil
}

func managementArchiveAppealWindowClosed(raw []byte, now time.Time) (bool, error) {
	var window struct {
		Enabled  *bool      `json:"enabled"`
		ClosesAt *time.Time `json:"closes_at"`
	}
	if err := json.Unmarshal(raw, &window); err != nil {
		return false, fmt.Errorf("archive appeal window fact: %w", err)
	}
	// 缺少 enabled 不能推断为关闭；历史或损坏的发布信息必须先核实。
	if window.Enabled == nil {
		return false, nil
	}
	return !*window.Enabled || (window.ClosesAt != nil && !window.ClosesAt.After(now)), nil
}

const managementArchiveSubjectFactsSQL = `SELECT
 EXISTS(SELECT 1 FROM submission WHERE tenant_id=$1 AND exam_id=$2::uuid)
 OR EXISTS(SELECT 1 FROM capture_batch WHERE tenant_id=$1 AND exam_id=$2::uuid)
 OR EXISTS(SELECT 1 FROM capture_upload_session WHERE tenant_id=$1 AND exam_id=$2::uuid)
 OR EXISTS(SELECT 1 FROM scoring_run WHERE tenant_id=$1 AND exam_id=$2::uuid)
 OR EXISTS(SELECT 1 FROM review_task WHERE tenant_id=$1 AND exam_id=$2::uuid)
 OR EXISTS(SELECT 1 FROM score_release WHERE tenant_id=$1 AND exam_id=$2::uuid),
 EXISTS(SELECT 1 FROM score_release_current c JOIN score_release r ON r.tenant_id=c.tenant_id AND r.id=c.release_id AND r.exam_id=c.exam_id
   WHERE c.tenant_id=$1 AND c.exam_id=$2::uuid AND r.status='published' AND r.published_at IS NOT NULL),
 EXISTS(SELECT 1 FROM scoring_run WHERE tenant_id=$1 AND exam_id=$2::uuid AND deleted_at IS NULL AND status NOT IN ('completed','cancelled'))
 OR EXISTS(SELECT 1 FROM paper_import_job WHERE tenant_id=$1 AND exam_id=$2::uuid AND status='processing')
 OR EXISTS(SELECT 1 FROM ocr_task t JOIN submission s ON s.tenant_id=t.tenant_id AND s.id=t.submission_id
   WHERE t.tenant_id=$1 AND s.exam_id=$2::uuid AND t.deleted_at IS NULL AND t.status NOT IN ('completed','canceled'))
 OR EXISTS(SELECT 1 FROM review_task WHERE tenant_id=$1 AND exam_id=$2::uuid AND deleted_at IS NULL AND status NOT IN ('submitted','completed','cancelled'))
 OR EXISTS(SELECT 1 FROM arbitration_task WHERE tenant_id=$1 AND exam_id=$2::uuid AND deleted_at IS NULL AND status NOT IN ('submitted','cancelled'))
 OR EXISTS(SELECT 1 FROM double_mark_session WHERE tenant_id=$1 AND exam_id=$2::uuid AND deleted_at IS NULL AND status NOT IN ('auto_finalized','arbitrated','cancelled'))
 OR EXISTS(SELECT 1 FROM regrade_job WHERE tenant_id=$1 AND exam_id=$2::uuid AND status<>'cancelled' AND finalized_at IS NULL)
 OR EXISTS(SELECT 1 FROM question_appeal WHERE tenant_id=$1 AND exam_id=$2::uuid AND status NOT IN ('resolved','rejected'))
 OR EXISTS(SELECT 1 FROM appeal WHERE tenant_id=$1 AND exam_id=$2::uuid AND deleted_at IS NULL AND status<>'closed')
 OR EXISTS(SELECT 1 FROM score_release WHERE tenant_id=$1 AND exam_id=$2::uuid AND status='draft')
 OR EXISTS(SELECT 1 FROM operational_exception WHERE tenant_id=$1 AND exam_id=$2::uuid AND status IN ('open','assigned'))
 OR EXISTS(SELECT 1 FROM capture_batch WHERE tenant_id=$1 AND exam_id=$2::uuid AND deleted_at IS NULL AND status NOT IN ('completed','cancelled'))`

func validateManagementArchiveInput(input ManagementArchiveInput) error {
	if input.ExpectedRevision <= 0 || len(input.CommandID) < 8 || len(input.CommandID) > 128 || strings.TrimSpace(input.CommandID) != input.CommandID || len(input.Reason) > 2000 {
		return ErrInvalidInput
	}
	return nil
}

func managementArchiveHash(kind, id, action string, input ManagementArchiveInput) string {
	raw, _ := json.Marshal(struct {
		Kind, ID, Action string
		Input            ManagementArchiveInput
	}{kind, id, action, input})
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}

func requireManagementArchiveRevision(item ManagementExam, input ManagementArchiveInput) error {
	if input.ExpectedRevision != item.Revision {
		return ErrRevisionConflict
	}
	// 场次版本不随单科配置或新增学科同步增长；必须精确比对完整集合，旧页面不能漏科归档。
	if item.Kind == "session" || input.ExpectedSubjectRevisions != nil {
		if len(input.ExpectedSubjectRevisions) != len(item.Exams) {
			return ErrRevisionConflict
		}
		for _, child := range item.Exams {
			if input.ExpectedSubjectRevisions[child.ID] != child.Revision {
				return ErrRevisionConflict
			}
		}
	}
	return nil
}

func (s *PostgresStore) ArchiveManagementExam(ctx context.Context, scope auth.AccessScope, actor, kind, id string, input ManagementArchiveInput) (ManagementExam, error) {
	return s.mutateManagementArchive(ctx, scope, actor, kind, id, "archive", input)
}

func (s *PostgresStore) RestoreManagementExam(ctx context.Context, scope auth.AccessScope, actor, kind, id string, input ManagementArchiveInput) (ManagementExam, error) {
	return s.mutateManagementArchive(ctx, scope, actor, kind, id, "restore", input)
}

func (s *PostgresStore) RecoverManagementArchiveCommand(ctx context.Context, scope auth.AccessScope, actor, commandID string) (ManagementArchiveCommandResult, error) {
	result := ManagementArchiveCommandResult{CommandID: commandID, Status: "not_accepted"}
	var raw []byte
	var kind, id string
	err := s.db.QueryRowContext(ctx, `SELECT kind,target_id::text,result_json FROM exam_management_command WHERE tenant_id=$1 AND actor_id=$2::uuid AND command_id=$3`, scope.TenantID, actor, commandID).Scan(&kind, &id, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return result, nil
	}
	if err != nil {
		return result, err
	}
	current, err := s.GetManagementExam(ctx, scope, kind, id)
	if err != nil {
		return result, err
	}
	if !current.CanManage {
		return result, ErrScopeForbidden
	}
	var item ManagementExam
	if err := json.Unmarshal(raw, &item); err != nil {
		return result, err
	}
	result.Status, result.Exam = "succeeded", &item
	return result, nil
}

func (s *PostgresStore) mutateManagementArchive(ctx context.Context, scope auth.AccessScope, actor, kind, id, action string, input ManagementArchiveInput) (result ManagementExam, resultErr error) {
	if err := validateManagementArchiveInput(input); err != nil {
		return result, err
	}
	defer func() {
		var pgErr *pgconn.PgError
		if errors.As(resultErr, &pgErr) && (pgErr.Code == "40P01" || pgErr.Code == "40001") {
			resultErr = ErrRevisionConflict
		}
	}()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return result, err
	}
	defer tx.Rollback()
	hash := managementArchiveHash(kind, id, action, input)
	// 同一命令先串行，再查回执。归档后发生恢复也不能让迟到的旧归档重试再次执行。
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, scope.TenantID+":"+actor+":"+input.CommandID+":exam-management"); err != nil {
		return result, err
	}
	var actualHash string
	var receipt []byte
	err = tx.QueryRowContext(ctx, `SELECT request_hash,result_json FROM exam_management_command WHERE tenant_id=$1 AND actor_id=$2::uuid AND command_id=$3`, scope.TenantID, actor, input.CommandID).Scan(&actualHash, &receipt)
	if err == nil {
		if actualHash != hash {
			return result, ErrCommandConflict
		}
		result, err = s.replayManagementArchiveTx(ctx, tx, scope, kind, id, receipt)
		if err != nil {
			return result, err
		}
		return result, tx.Commit()
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return result, err
	}
	if err := lockManagementArchiveSubjects(ctx, tx, scope, kind, id); err != nil {
		return result, err
	}
	current, err := s.loadManagementExamTx(ctx, tx, scope, kind, id, true)
	if err != nil {
		return result, err
	}
	if !current.CanManage {
		return result, ErrScopeForbidden
	}
	// 父级行锁阻止新增学科后，再补取当前完整集合的锁，涵盖首次枚举期间完成的新增学科。
	if err := lockManagementArchiveSubjects(ctx, tx, scope, kind, id); err != nil {
		return result, err
	}
	if err := requireManagementArchiveRevision(current, input); err != nil {
		return result, err
	}
	if action == "archive" && current.ArchivedAt == nil {
		check, err := checkManagementArchiveTx(ctx, tx, scope.TenantID, current)
		if err != nil {
			return result, err
		}
		if !check.Allowed {
			return result, ErrArchiveBlocked
		}
	}
	if action == "restore" && !current.CanRestore {
		return result, ErrLocked
	}
	if action == "restore" || current.ArchivedAt == nil {
		query, args := managementArchiveUpdateQuery(scope.TenantID, actor, kind, id, action, input.Reason)
		if _, err := tx.ExecContext(ctx, query, args...); err != nil {
			return result, err
		}
	}
	result, err = s.loadManagementExamTx(ctx, tx, scope, kind, id, false)
	if err != nil {
		return result, err
	}
	check, err := checkManagementArchiveTx(ctx, tx, scope.TenantID, result)
	if err != nil {
		return result, err
	}
	applyManagementArchiveCheck(&result, check, true)
	raw, err := json.Marshal(result)
	if err != nil {
		return result, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO exam_management_command(tenant_id,actor_id,command_id,kind,target_id,action,request_hash,result_json) VALUES($1,$2::uuid,$3,$4,$5::uuid,$6,$7,$8::jsonb)`, scope.TenantID, actor, input.CommandID, kind, id, action, hash, raw); err != nil {
		return result, err
	}
	return result, tx.Commit()
}

// 旧回执只在当前整场权限仍成立时返回；恢复后的迟到重试不会再次改写归档状态。
func (s *PostgresStore) replayManagementArchiveTx(ctx context.Context, tx *sql.Tx, scope auth.AccessScope, kind, id string, receipt []byte) (result ManagementExam, err error) {
	current, err := s.loadManagementExamTx(ctx, tx, scope, kind, id, false)
	if err != nil {
		return result, err
	}
	if !current.CanManage {
		return result, ErrScopeForbidden
	}
	err = json.Unmarshal(receipt, &result)
	return result, err
}

func managementArchiveUpdateQuery(tenantID, actor, kind, id, action, reason string) (string, []any) {
	table := "exam"
	if kind == "session" {
		table = "exam_session"
	}
	change := `archived_at=now(),archived_by=$3::uuid,archive_reason=$4`
	args := []any{tenantID, id, actor, strings.TrimSpace(reason)}
	if action == "restore" {
		change = `archived_at=NULL,archived_by=NULL,archive_reason=''`
		args = args[:2]
	}
	return `UPDATE ` + table + ` SET ` + change + `,revision=revision+1,updated_at=now() WHERE tenant_id=$1 AND id::text=$2`, args
}
