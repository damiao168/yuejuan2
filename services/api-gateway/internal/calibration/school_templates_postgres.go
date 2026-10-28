package calibration

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"github.com/google/uuid"
)

func schoolTemplateTargetQuery(ctx context.Context, query policyQueryer, tenant, exam, question string, lock bool) (SchoolTemplateTarget, error) {
	var target SchoolTemplateTarget
	suffix := ""
	if lock {
		suffix = " FOR SHARE OF e,q"
	}
	// 冻结后的作答类型优先于可变配置；学校和学科只来自当前题目所属考试，绝不接受请求中的school_id。
	err := query.QueryRowContext(ctx, `SELECT e.school_id::text,e.subject,COALESCE(NULLIF(snap.archetype_snapshot_json->>'code',''),NULLIF(config.archetype_code,''),NULLIF(q.assessment_archetype,''),''),q.score::float8
FROM exam e JOIN question q ON q.tenant_id=e.tenant_id AND q.exam_id=e.id AND q.id=$3::uuid AND q.deleted_at IS NULL AND q.status<>'deleted'
LEFT JOIN question_assessment_config config ON config.tenant_id=q.tenant_id AND config.exam_id=q.exam_id AND config.question_id=q.id
LEFT JOIN LATERAL(SELECT archetype_snapshot_json FROM exam_question_snapshot WHERE tenant_id=q.tenant_id AND exam_id=q.exam_id AND question_id=q.id ORDER BY snapshot_version DESC LIMIT 1) snap ON true
WHERE e.tenant_id=$1::uuid AND e.id=$2::uuid AND e.deleted_at IS NULL`+suffix, tenant, exam, question).Scan(&target.SchoolID, &target.Subject, &target.ArchetypeCode, &target.MaxScore)
	if errors.Is(err, sql.ErrNoRows) {
		return target, ErrNotFound
	}
	if err == nil && !validTarget(target) {
		err = ErrTemplateContextMissing
	}
	return target, err
}
func (s *PostgresStore) SchoolTemplateTarget(ctx context.Context, tenant, exam, question string) (SchoolTemplateTarget, error) {
	return schoolTemplateTargetQuery(ctx, s.db, tenant, exam, question, false)
}

const templateColumns = `id::text,school_id::text,subject,name,source_exam_id::text,source_question_id::text,source_policy_revision,policy_json,status,revision,approved_by::text,approved_at,COALESCE(withdrawn_by::text,''),withdrawn_at,withdraw_reason`

func scanSchoolTemplate(row rowScanner) (SchoolTemplate, error) {
	var item SchoolTemplate
	var raw []byte
	var withdrawn sql.NullTime
	err := row.Scan(&item.ID, &item.SchoolID, &item.Subject, &item.Name, &item.SourceExamID, &item.SourceQuestionID, &item.SourcePolicyRevision, &raw, &item.Status, &item.Revision, &item.ApprovedBy, &item.ApprovedAt, &item.WithdrawnBy, &withdrawn, &item.WithdrawReason)
	if errors.Is(err, sql.ErrNoRows) {
		return item, ErrNotFound
	}
	if err != nil {
		return item, err
	}
	err = json.Unmarshal(raw, &item.Policy)
	item.ApprovedAt = item.ApprovedAt.UTC()
	if withdrawn.Valid {
		t := withdrawn.Time.UTC()
		item.WithdrawnAt = &t
	}
	return item, err
}
func (s *PostgresStore) ListSchoolTemplates(ctx context.Context, tenant string, target SchoolTemplateTarget) ([]SchoolTemplate, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+templateColumns+` FROM school_calibration_template WHERE tenant_id=$1::uuid AND school_id=$2::uuid AND subject=$3 AND archetype_code=$4 AND max_score=$5 AND status='approved' ORDER BY approved_at DESC,id`, tenant, target.SchoolID, target.Subject, target.ArchetypeCode, target.MaxScore)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SchoolTemplate{}
	for rows.Next() {
		item, err := scanSchoolTemplate(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}
func (s *PostgresStore) ApproveSchoolTemplate(ctx context.Context, tenant, actor, exam, question string, input ApproveSchoolTemplateInput) (SchoolTemplate, error) {
	if !validApproval(input) {
		return SchoolTemplate{}, ErrInvalidInput
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return SchoolTemplate{}, err
	}
	defer tx.Rollback()
	target, err := schoolTemplateTargetQuery(ctx, tx, tenant, exam, question, true)
	if err != nil {
		return SchoolTemplate{}, err
	}
	if err = checkExpectedTemplateTarget(ctx, exam, question, target, true); err != nil {
		return SchoolTemplate{}, err
	}
	var raw []byte
	err = tx.QueryRowContext(ctx, `SELECT to_jsonb(p) FROM grader_calibration_policy p WHERE tenant_id=$1::uuid AND exam_id=$2::uuid AND question_id=$3::uuid FOR SHARE`, tenant, exam, question).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return SchoolTemplate{}, ErrPolicyMissing
	}
	if err != nil {
		return SchoolTemplate{}, err
	}
	var p Policy
	if err = json.Unmarshal(raw, &p); err != nil {
		return SchoolTemplate{}, err
	}
	if p.Revision != input.SourcePolicyRevision {
		return SchoolTemplate{}, ErrConflict
	}
	if p.ArchetypeCode != target.ArchetypeCode || p.MaxScore != target.MaxScore {
		return SchoolTemplate{}, ErrTemplateUnavailable
	}
	raw, err = json.Marshal(p)
	if err != nil {
		return SchoolTemplate{}, err
	}
	// 唯一来源修订和名称使未知网络结果可回认；不能借换一个请求键重复批准同一份标准。
	_, err = tx.ExecContext(ctx, `INSERT INTO school_calibration_template(id,tenant_id,school_id,subject,name,source_exam_id,source_question_id,source_policy_id,source_policy_revision,archetype_code,max_score,policy_json,approved_by)
VALUES($1::uuid,$2::uuid,$3::uuid,$4,$5,$6::uuid,$7::uuid,$8::uuid,$9,$10,$11,$12::jsonb,$13::uuid) ON CONFLICT(tenant_id,source_policy_id,source_policy_revision,name) DO NOTHING`, uuid.NewString(), tenant, target.SchoolID, target.Subject, strings.TrimSpace(input.Name), exam, question, p.ID, p.Revision, p.ArchetypeCode, p.MaxScore, raw, actor)
	if err != nil {
		return SchoolTemplate{}, err
	}
	item, err := scanSchoolTemplate(tx.QueryRowContext(ctx, `SELECT `+templateColumns+` FROM school_calibration_template WHERE tenant_id=$1::uuid AND source_policy_id=$2::uuid AND source_policy_revision=$3 AND name=$4`, tenant, p.ID, p.Revision, strings.TrimSpace(input.Name)))
	if err != nil {
		return item, err
	}
	return item, tx.Commit()
}
func (s *PostgresStore) WithdrawSchoolTemplate(ctx context.Context, tenant, actor string, target SchoolTemplateTarget, id string, input WithdrawSchoolTemplateInput) (SchoolTemplate, error) {
	if !validWithdrawal(input) {
		return SchoolTemplate{}, ErrInvalidInput
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return SchoolTemplate{}, err
	}
	defer tx.Rollback()
	if expected, ok := ctx.Value(templateTargetContextKey{}).(templateTargetContext); ok {
		target, err = schoolTemplateTargetQuery(ctx, tx, tenant, expected.exam, expected.question, true)
		if err != nil {
			return SchoolTemplate{}, err
		}
		if err = checkExpectedTemplateTarget(ctx, expected.exam, expected.question, target, true); err != nil {
			return SchoolTemplate{}, err
		}
	} else if _, authenticated := auth.UserFromContext(ctx); authenticated {
		return SchoolTemplate{}, ErrConflict
	}
	item, err := scanSchoolTemplate(tx.QueryRowContext(ctx, `UPDATE school_calibration_template SET status='withdrawn',revision=revision+1,withdrawn_by=$3::uuid,withdrawn_at=now(),withdraw_reason=$4 WHERE tenant_id=$1::uuid AND id=$2::uuid AND school_id=$5::uuid AND subject=$6 AND archetype_code=$7 AND max_score=$8 AND status='approved' AND revision=$9 RETURNING `+templateColumns, tenant, id, actor, strings.TrimSpace(input.Reason), target.SchoolID, target.Subject, target.ArchetypeCode, target.MaxScore, input.ExpectedRevision))
	if errors.Is(err, ErrNotFound) {
		return item, ErrConflict
	}
	if err != nil {
		return item, err
	}
	return item, tx.Commit()
}
func (s *PostgresStore) ApplySchoolTemplate(ctx context.Context, tenant, exam, question string, input PutPolicyInput) (Policy, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Policy{}, err
	}
	defer tx.Rollback()
	target, err := schoolTemplateTargetQuery(ctx, tx, tenant, exam, question, true)
	if err != nil {
		return Policy{}, err
	}
	if err = checkExpectedTemplateTarget(ctx, exam, question, target, false); err != nil {
		return Policy{}, err
	}
	// 应用持有模板共享锁直到目标CAS提交；撤回需更新锁，不能在核对后、保存前穿过这一边界。
	item, err := scanSchoolTemplate(tx.QueryRowContext(ctx, `SELECT `+templateColumns+` FROM school_calibration_template WHERE tenant_id=$1::uuid AND id=$2::uuid FOR SHARE`, tenant, input.SchoolTemplateID))
	if errors.Is(err, ErrNotFound) {
		return Policy{}, ErrTemplateUnavailable
	}
	if err != nil {
		return Policy{}, err
	}
	if item.Status != "approved" || item.Revision != input.SchoolTemplateRevision || !matchesTarget(item, target) {
		return Policy{}, ErrTemplateUnavailable
	}
	p, err := putPolicyQuery(ctx, tx, tenant, exam, question, templatePolicyInput(item, input.ExpectedRevision))
	if err != nil {
		return p, err
	}
	return p, tx.Commit()
}
