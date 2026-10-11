package exam

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/auth"
)

func managementScopeSQL(alias string) string {
	return `$2::boolean OR ` + alias + `.school_id::text IN (SELECT jsonb_array_elements_text($3::jsonb))
OR ` + alias + `.id::text IN (SELECT jsonb_array_elements_text($4::jsonb))
OR EXISTS (SELECT 1 FROM exam_class mc WHERE mc.tenant_id=` + alias + `.tenant_id AND mc.exam_id=` + alias + `.id AND mc.deleted_at IS NULL
 AND mc.class_id::text IN (SELECT jsonb_array_elements_text($5::jsonb)))`
}

func managementScopeArgs(scope auth.AccessScope) []any {
	args := scopeQueryArgs(scope)
	if !scope.IsSchoolWide() && !scope.TenantWide {
		args[2] = "[]"
	}
	return args
}

func (s *PostgresStore) GetManagementExam(ctx context.Context, scope auth.AccessScope, kind, id string) (ManagementExam, error) {
	// 父级、全部学科和确认事实使用同一快照；配置并发变化不会拼成半旧半新的详情。
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return ManagementExam{}, err
	}
	defer tx.Rollback()
	out, err := s.loadManagementExamTx(ctx, tx, scope, kind, id, false)
	if err != nil {
		return ManagementExam{}, err
	}
	return out, tx.Commit()
}

// loadManagementExamTx 的锁定模式先锁场次，再按 ID 锁所有子科；调用方负责归档共享 advisory 锁。
// 即使调用者只有部分科目权限，也加载完整集合以拒绝整场写入，返回值始终剔除未授权学科。
func (s *PostgresStore) loadManagementExamTx(ctx context.Context, tx *sql.Tx, scope auth.AccessScope, kind, id string, lock bool) (ManagementExam, error) {
	if kind != "session" && kind != "standalone" {
		return ManagementExam{}, ErrInvalidInput
	}
	out := ManagementExam{ID: id, Kind: kind, Exams: []Exam{}}
	if kind == "session" {
		if err := loadManagementSessionTx(ctx, tx, scope.TenantID, id, lock, &out); err != nil {
			return ManagementExam{}, err
		}
	}
	total, rules, facts, err := s.loadManagementSubjectsTx(ctx, tx, scope, kind, id, lock, &out)
	if err != nil {
		return ManagementExam{}, err
	}
	if len(out.Exams) == 0 {
		return ManagementExam{}, ErrNotFound
	}
	if kind == "standalone" {
		// 历史单科考试没有场次年级；只有全部参考班级属于同一年级时才填入，不能随意取第一班。
		if err := tx.QueryRowContext(ctx, `SELECT CASE WHEN count(DISTINCT c.grade_id)=1 THEN min(c.grade_id::text) ELSE '' END
FROM exam_class ec JOIN school_class c ON c.tenant_id=ec.tenant_id AND c.id=ec.class_id AND c.deleted_at IS NULL
WHERE ec.tenant_id=$1 AND ec.exam_id::text=$2 AND ec.deleted_at IS NULL`, scope.TenantID, id).Scan(&out.GradeID); err != nil {
			return ManagementExam{}, err
		}
	}
	completeManagementExam(&out, scope, total, rules, facts)
	return out, nil
}

func loadManagementSessionTx(ctx context.Context, tx *sql.Tx, tenantID, id string, lock bool, out *ManagementExam) error {
	query := `SELECT school_id::text,grade_id::text,name,exam_type,revision,created_by::text,created_at,updated_at,status='archived',
archived_at,COALESCE(archived_by::text,''),COALESCE(archive_reason,'') FROM exam_session
WHERE tenant_id=$1 AND id::text=$2 AND deleted_at IS NULL`
	if lock {
		query += " FOR UPDATE"
	}
	if err := tx.QueryRowContext(ctx, query, tenantID, id).Scan(&out.SchoolID, &out.GradeID, &out.Name, &out.ExamType, &out.Revision, &out.CreatedBy, &out.CreatedAt, &out.UpdatedAt, &out.LegacyArchived, &out.ArchivedAt, &out.ArchivedBy, &out.ArchiveReason); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	return nil
}

func (s *PostgresStore) loadManagementSubjectsTx(ctx context.Context, tx *sql.Tx, scope auth.AccessScope, kind, id string, lock bool, out *ManagementExam) (int, map[string]string, map[string]managementSubjectFacts, error) {
	query := `SELECT e.id::text,e.tenant_id::text,e.school_id::text,e.name,e.subject,e.exam_type,e.total_score::float8,
e.status,e.grading_mode,e.appeal_enabled,e.publish_policy,e.created_by::text,e.revision,e.created_at,e.updated_at,
COALESCE((SELECT array_agg(ec.class_id::text ORDER BY ec.class_id) FROM exam_class ec WHERE ec.tenant_id=e.tenant_id AND ec.exam_id=e.id AND ec.deleted_at IS NULL),'{}'),
e.candidate_rule, EXISTS(SELECT 1 FROM exam_readiness_snapshot rs WHERE rs.tenant_id=e.tenant_id AND rs.exam_id=e.id AND rs.status='passed' AND rs.invalidated_at IS NULL),
EXISTS(SELECT 1 FROM score_release_current sc JOIN score_release sr ON sr.tenant_id=sc.tenant_id AND sr.id=sc.release_id AND sr.exam_id=sc.exam_id
 WHERE sc.tenant_id=e.tenant_id AND sc.exam_id=e.id AND sr.status='published' AND sr.published_at IS NOT NULL),
(` + managementScopeSQL("e") + `),e.archived_at,COALESCE(e.archived_by::text,''),COALESCE(e.archive_reason,'')
FROM exam e WHERE e.tenant_id=$1 AND e.deleted_at IS NULL`
	if kind == "session" {
		query += ` AND e.exam_session_id::text=$6`
	} else {
		query += ` AND e.id::text=$6 AND e.exam_session_id IS NULL`
	}
	query += " ORDER BY e.id"
	if lock {
		query += " FOR UPDATE OF e"
	}
	rows, err := tx.QueryContext(ctx, query, append(managementScopeArgs(scope), id)...)
	if err != nil {
		return 0, nil, nil, err
	}
	defer rows.Close()
	total := 0
	rules, facts := map[string]string{}, map[string]managementSubjectFacts{}
	for rows.Next() {
		var child Exam
		var rule string
		var ready, published, allowed bool
		var archive ManagementArchiveMetadata
		if err := rows.Scan(&child.ID, &child.TenantID, &child.SchoolID, &child.Name, &child.Subject, &child.ExamType, &child.TotalScore, &child.Status, &child.GradingMode, &child.AppealEnabled, &child.PublishPolicy, &child.CreatedBy, &child.Revision, &child.CreatedAt, &child.UpdatedAt, examTextArray(&child.ClassIDs), &rule, &ready, &published, &allowed, &archive.ArchivedAt, &archive.ArchivedBy, &archive.ArchiveReason); err != nil {
			return 0, nil, nil, err
		}
		// 不可见子科仍计入整场总数，完整范围判断不能只依赖返回的授权集合。
		total++
		if !allowed {
			continue
		}
		appendManagementSubject(out, child, archive)
		rules[child.ID] = rule
		facts[child.ID] = managementSubjectFacts{Ready: ready, Published: published, Available: true}
	}
	if err := rows.Err(); err != nil {
		return 0, nil, nil, err
	}
	rows.Close()
	return total, rules, facts, nil
}

func appendManagementSubject(out *ManagementExam, child Exam, archive ManagementArchiveMetadata) {
	if out.Kind == "session" {
		child.SessionID, child.SessionName, child.SessionGradeID = out.ID, out.Name, out.GradeID
	} else {
		out.SchoolID, out.Name, out.ExamType, out.Revision = child.SchoolID, child.Name, child.ExamType, child.Revision
		out.CreatedBy, out.CreatedAt, out.UpdatedAt, out.ManagementArchiveMetadata = child.CreatedBy, child.CreatedAt, child.UpdatedAt, archive
	}
	if out.Kind == "session" && archive.ArchivedAt != nil {
		out.ArchivedSubjectIDs = append(out.ArchivedSubjectIDs, child.ID)
	}
	if out.Kind == "standalone" && child.Status == "archived" {
		out.LegacyArchived = true
	}
	out.Exams = append(out.Exams, child)
}

func (s *PostgresStore) ListManagementExams(ctx context.Context, scope auth.AccessScope, filter ManagementListFilter) ([]ManagementExam, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	out := []ManagementExam{}
	limit := filter.Limit
	if limit <= 0 {
		limit = 51
	}
	cursorAt, cursorID := filter.CursorAt, filter.CursorID
	for len(out) < limit {
		query, args := managementListQuery(scope, filter, cursorAt, cursorID, limit)
		keys, err := readManagementListKeysTx(ctx, tx, query, args)
		if err != nil {
			return nil, err
		}
		out, err = s.appendManagementListKeysTx(ctx, tx, scope, filter, keys, limit, out)
		if err != nil {
			return nil, err
		}
		if len(keys) < limit || len(out) == limit {
			break
		}
		// 状态过滤可能使返回项不足一页，游标仍按最后扫描的整场键推进。
		last := keys[len(keys)-1]
		cursorAt, cursorID = last.createdAt, last.id
	}
	// 归档资格与整场数据使用同一读取快照；活跃任务集合只算一次，避免每场重复扫描同租户任务。
	if _, err := tx.ExecContext(ctx, "SAVEPOINT management_archive_check"); err != nil {
		return nil, err
	}
	checks, checkErr := checkManagementArchivesTx(ctx, tx, scope.TenantID, out)
	if checkErr != nil {
		// SQL 检查失败会使 PostgreSQL 事务不可提交；回退到检查前才能保留已读列表并禁用归档。
		if _, err := tx.ExecContext(ctx, "ROLLBACK TO SAVEPOINT management_archive_check"); err != nil {
			return nil, err
		}
	}
	if _, err := tx.ExecContext(ctx, "RELEASE SAVEPOINT management_archive_check"); err != nil {
		return nil, err
	}
	applyManagementArchiveChecks(out, checks, checkErr == nil)
	return out, tx.Commit()
}

type managementListKey struct {
	id, kind  string
	createdAt time.Time
}

func managementListQuery(scope auth.AccessScope, filter ManagementListFilter, cursorAt time.Time, cursorID string, limit int) (string, []any) {
	query := `SELECT DISTINCT COALESCE(es.id,e.id)::text,CASE WHEN es.id IS NULL THEN 'standalone' ELSE 'session' END,COALESCE(es.created_at,e.created_at)
FROM exam e LEFT JOIN exam_session es ON es.tenant_id=e.tenant_id AND es.id=e.exam_session_id AND es.deleted_at IS NULL
WHERE e.tenant_id=$1 AND e.deleted_at IS NULL AND (e.exam_session_id IS NULL OR es.id IS NOT NULL) AND (` + managementScopeSQL("e") + `)`
	args := managementScopeArgs(scope)
	add := func(fragment string, value any) {
		args = append(args, value)
		query += fmt.Sprintf(fragment, len(args))
	}
	if filter.SchoolID != "" {
		add(" AND e.school_id::text=$%d", filter.SchoolID)
	}
	if filter.GradeID != "" {
		args = append(args, filter.GradeID)
		placeholder := len(args)
		query += fmt.Sprintf(` AND (es.grade_id::text=$%d OR (es.id IS NULL AND EXISTS (
SELECT 1 FROM exam_class egc JOIN school_class gc ON gc.tenant_id=egc.tenant_id AND gc.id=egc.class_id AND gc.deleted_at IS NULL
WHERE egc.tenant_id=e.tenant_id AND egc.exam_id=e.id AND egc.deleted_at IS NULL AND gc.grade_id::text=$%d)))`, placeholder, placeholder)
	}
	if filter.ExamType != "" {
		add(" AND COALESCE(es.exam_type,e.exam_type)=$%d", filter.ExamType)
	}
	if filter.Search != "" {
		add(" AND position(lower($%d) in lower(COALESCE(es.name,e.name)))>0", filter.Search)
	}
	if !cursorAt.IsZero() && cursorID != "" {
		args = append(args, cursorAt, cursorID)
		query += fmt.Sprintf(" AND (COALESCE(es.created_at,e.created_at),COALESCE(es.id,e.id)::text)<($%d,$%d)", len(args)-1, len(args))
	}
	query += " ORDER BY 3 DESC,1 DESC"
	args = append(args, limit)
	query += fmt.Sprintf(" LIMIT $%d", len(args))
	return query, args
}

func readManagementListKeysTx(ctx context.Context, tx *sql.Tx, query string, args []any) ([]managementListKey, error) {
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	keys := []managementListKey{}
	for rows.Next() {
		var value managementListKey
		if err := rows.Scan(&value.id, &value.kind, &value.createdAt); err != nil {
			rows.Close()
			return nil, err
		}
		keys = append(keys, value)
	}
	rowErr := rows.Err()
	rows.Close()
	if rowErr != nil {
		return nil, rowErr
	}
	return keys, nil
}

func (s *PostgresStore) appendManagementListKeysTx(ctx context.Context, tx *sql.Tx, scope auth.AccessScope, filter ManagementListFilter, keys []managementListKey, limit int, out []ManagementExam) ([]ManagementExam, error) {
	// 分页选择整场键后才取子科，状态筛选只过滤整场，不会截断或代选其中一科。
	for _, key := range keys {
		item, err := s.loadManagementExamTx(ctx, tx, scope, key.kind, key.id, false)
		if err != nil {
			return nil, err
		}
		if managementMatchesFilter(item, filter) {
			out = append(out, item)
			if len(out) == limit {
				break
			}
		}
	}
	return out, nil
}

func (s *PostgresStore) UpdateManagementExam(ctx context.Context, scope auth.AccessScope, kind, id string, input ManagementUpdateInput) (ManagementExam, error) {
	if err := validateManagementUpdate(input); err != nil {
		return ManagementExam{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ManagementExam{}, err
	}
	defer tx.Rollback()
	// 与发布和归档采用同一锁顺序；先获取业务锁再锁父级、子级行，避免交叉等待。
	if err := lockManagementArchiveSubjects(ctx, tx, scope, kind, id); err != nil {
		return ManagementExam{}, err
	}
	current, err := s.loadManagementExamTx(ctx, tx, scope, kind, id, true)
	if err != nil {
		return ManagementExam{}, err
	}
	// 全部子科的版本、冻结状态和班级范围校验通过后，才能开始当前事务的写入。
	inherited, err := s.validateManagementUpdateTx(ctx, tx, scope, current, input)
	if err != nil {
		return ManagementExam{}, err
	}
	for _, child := range current.Exams {
		if err := s.updateManagementChildTx(ctx, tx, scope, kind, child, input, inherited[child.ID]); err != nil {
			return ManagementExam{}, err
		}
	}
	if kind == "session" {
		if err := updateManagementSessionTx(ctx, tx, scope.TenantID, id, input); err != nil {
			return ManagementExam{}, err
		}
	}
	out, err := s.loadManagementExamTx(ctx, tx, scope, kind, id, false)
	if err != nil {
		return ManagementExam{}, err
	}
	return out, tx.Commit()
}

func (s *PostgresStore) validateManagementUpdateTx(ctx context.Context, tx *sql.Tx, scope auth.AccessScope, current ManagementExam, input ManagementUpdateInput) (map[string]bool, error) {
	if !current.CanManage {
		return nil, ErrScopeForbidden
	}
	if err := requireManagementRevisions(current, input); err != nil {
		return nil, err
	}
	if !current.CanEdit {
		return nil, ErrLocked
	}
	if input.ClassIDs != nil && len(current.InheritedSubjectIDs) == 0 {
		return nil, ErrInvalidInput
	}
	if input.ClassIDs != nil {
		if err := requireManagementClasses(ctx, tx, scope.TenantID, current.SchoolID, current.GradeID, *input.ClassIDs); err != nil {
			return nil, err
		}
	}
	inherited := map[string]bool{}
	for _, id := range current.InheritedSubjectIDs {
		inherited[id] = true
	}
	if err := validateManagementPostgresChildren(scope, current.Exams, input, inherited); err != nil {
		return nil, err
	}
	return inherited, nil
}

func validateManagementPostgresChildren(scope auth.AccessScope, children []Exam, input ManagementUpdateInput, inherited map[string]bool) error {
	for _, child := range children {
		if input.ExamType != nil && *input.ExamType != child.ExamType && !CanChangeCandidateRoster(child.Status) {
			return ErrLocked
		}
		if input.ClassIDs != nil && inherited[child.ID] && !classIDSetsEqual(child.ClassIDs, *input.ClassIDs) {
			if !CanChangeCandidateRoster(child.Status) {
				return ErrCandidatesFrozen
			}
			if !managementScopeAllowsRequestedClasses(scope, child.SchoolID, *input.ClassIDs) {
				return ErrScopeForbidden
			}
		}
	}
	return nil
}

func (s *PostgresStore) updateManagementChildTx(ctx context.Context, tx *sql.Tx, scope auth.AccessScope, kind string, child Exam, input ManagementUpdateInput, inherited bool) error {
	clauses, args := []string{}, []any{scope.TenantID, child.ID}
	add := func(column string, value any) {
		args = append(args, value)
		clauses = append(clauses, fmt.Sprintf("%s=$%d", column, len(args)))
	}
	if input.Name != nil {
		name := *input.Name
		if kind == "session" {
			name = fmt.Sprintf("%s · %s", *input.Name, subjectDisplayName(child.Subject))
		}
		if name != child.Name {
			add("name", name)
		}
	}
	if input.ExamType != nil && *input.ExamType != child.ExamType {
		add("exam_type", *input.ExamType)
	}
	clauses = append(clauses, "revision=revision+1", "updated_at=now()")
	if _, err := tx.ExecContext(ctx, `UPDATE exam SET `+strings.Join(clauses, ",")+` WHERE tenant_id=$1 AND id::text=$2 AND deleted_at IS NULL`, args...); err != nil {
		return err
	}
	if input.ClassIDs != nil && inherited && !classIDSetsEqual(child.ClassIDs, *input.ClassIDs) {
		// 班级替换和候选快照刷新留在当前事务内，任一失败由调用方整体回滚。
		if err := s.replaceClasses(ctx, tx, scope, child.ID, normalizedClassIDs(*input.ClassIDs)); err != nil {
			return err
		}
		if err := s.refreshExamCandidateSnapshot(ctx, tx, scope.TenantID, child.ID); err != nil {
			return err
		}
	}
	return nil
}

func updateManagementSessionTx(ctx context.Context, tx *sql.Tx, tenantID, id string, input ManagementUpdateInput) error {
	clauses, args := []string{"revision=revision+1", "updated_at=now()"}, []any{tenantID, id}
	if input.Name != nil {
		args = append(args, *input.Name)
		clauses = append(clauses, fmt.Sprintf("name=$%d", len(args)))
	}
	if input.ExamType != nil {
		args = append(args, *input.ExamType)
		clauses = append(clauses, fmt.Sprintf("exam_type=$%d", len(args)))
	}
	if _, err := tx.ExecContext(ctx, `UPDATE exam_session SET `+strings.Join(clauses, ",")+` WHERE tenant_id=$1 AND id::text=$2 AND deleted_at IS NULL`, args...); err != nil {
		return err
	}
	return nil
}

func requireManagementClasses(ctx context.Context, tx *sql.Tx, tenantID, schoolID, gradeID string, classIDs []string) error {
	// 历史单科也只能在原学校及已关联年级内调整参考班级，不能通过班级输入暗中迁移考试。
	for _, classID := range normalizedClassIDs(classIDs) {
		var exists int
		err := tx.QueryRowContext(ctx, `SELECT 1 FROM school_class
WHERE tenant_id=$1 AND id::text=$2 AND school_id::text=$3 AND ($4='' OR grade_id::text=$4)
 AND status='active' AND deleted_at IS NULL`, tenantID, classID, schoolID, gradeID).Scan(&exists)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrInvalidInput
		}
		if err != nil {
			return err
		}
	}
	return nil
}
