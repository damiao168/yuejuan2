package aidisagreement

import (
	"context"

	"edugrade-enterprise/services/api-gateway/internal/auth"
)

func allowsCollectionDisagreement(ctx context.Context, tenantID string, access auth.ExamCollectionAccess, item Disagreement) (bool, error) {
	if err := access.Validate(tenantID); err != nil {
		return false, err
	}
	if access.Scope == nil {
		return true, nil
	}
	if access.Resolver != nil {
		return access.Allows(ctx, tenantID, "ai_human_disagreement", item.ID, item.ExamID)
	}
	return auth.AllowsResourceBoundary(*access.Scope, auth.ResourceBoundary{
		TenantID: tenantID, ResourceType: "ai_human_disagreement", ResourceID: item.ID,
		ExamID: item.ExamID, SubmissionID: item.SubmissionID,
	}), nil
}

func disagreementCollectionArgs(tenantID string, access auth.ExamCollectionAccess) ([]any, error) {
	args, err := access.SQLArgs(tenantID)
	if err != nil {
		return nil, err
	}
	if access.Scope == nil {
		return append(args, []string{}, "", false), nil
	}
	scope := *access.Scope
	return append(args, append([]string{}, scope.SubmissionIDs...), scope.StudentID, scope.AssignedOnly), nil
}

// 与单项答卷边界一致：外校分派只授权具体答卷，不能通过考试导航或兼任本校管理员扩大到同场答卷。
// 参数顺序固定为租户、全租户、管理学校、班级、考试、分派答卷、本人学生、仅分派。
const disagreementCollectionPredicate = `($2 OR EXISTS(
 SELECT 1 FROM submission collection_submission
 JOIN exam collection_exam ON collection_exam.tenant_id=collection_submission.tenant_id AND collection_exam.id=collection_submission.exam_id
 WHERE collection_submission.tenant_id=$1::uuid AND collection_submission.id=ai_human_disagreement.submission_id AND collection_submission.deleted_at IS NULL
 AND (collection_submission.id::text=ANY($6::text[]) OR ($7<>'' AND collection_submission.student_id::text=$7)
 OR ((NOT $8 OR collection_exam.school_id::text=ANY($3::text[]))
 AND (collection_exam.school_id::text=ANY($3::text[]) OR collection_exam.id::text=ANY($5::text[])
 OR EXISTS(SELECT 1 FROM exam_class collection_class WHERE collection_class.tenant_id=collection_exam.tenant_id AND collection_class.exam_id=collection_exam.id AND collection_class.deleted_at IS NULL AND collection_class.class_id::text=ANY($4::text[])))))))`
