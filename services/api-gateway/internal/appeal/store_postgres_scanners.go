package appeal

import (
	"database/sql"
	"encoding/json"
	"errors"
)

func appealColumns(aliases ...string) string {
	prefix := ""
	if len(aliases) > 0 && aliases[0] != "" {
		prefix = aliases[0] + "."
	}
	return prefix + `id::text, ` + prefix + `tenant_id::text, ` + prefix + `exam_id::text, ` + prefix + `submission_id::text, ` + prefix + `submission_grade_id::text,
  ` + prefix + `student_id::text, ` + prefix + `target_type, COALESCE(` + prefix + `final_grade_id::text, ''), COALESCE(` + prefix + `question_id::text, ''),
  ` + prefix + `question_no, ` + prefix + `deduction_point_id, ` + prefix + `reason, ` + prefix + `attachment, ` + prefix + `status, ` + prefix + `result_reason,
  COALESCE(` + prefix + `assigned_to::text, ''), ` + prefix + `teacher_recommendation, ` + prefix + `teacher_recommendation_reason,
  ` + prefix + `teacher_recommended_score::float8, COALESCE(` + prefix + `teacher_recommendation_by::text, ''), ` + prefix + `teacher_recommendation_at,
  COALESCE(` + prefix + `reviewed_by::text, ''), ` + prefix + `reviewed_at,
  COALESCE(` + prefix + `closed_by::text, ''), ` + prefix + `closed_at, ` + prefix + `revision, ` + prefix + `created_by::text, ` + prefix + `created_at, ` + prefix + `updated_at`
}

type scanner interface {
	Scan(dest ...any) error
}

func scanAppeal(row scanner) (Appeal, error) {
	var out Appeal
	var attachmentRaw []byte
	var recommendedScore sql.NullFloat64
	var recommendationAt sql.NullTime
	var reviewedAt sql.NullTime
	var closedAt sql.NullTime
	if err := row.Scan(
		&out.ID,
		&out.TenantID,
		&out.ExamID,
		&out.SubmissionID,
		&out.SubmissionGradeID,
		&out.StudentID,
		&out.TargetType,
		&out.FinalGradeID,
		&out.QuestionID,
		&out.QuestionNo,
		&out.DeductionPointID,
		&out.Reason,
		&attachmentRaw,
		&out.Status,
		&out.ResultReason,
		&out.AssignedTo,
		&out.Recommendation,
		&out.RecommendationReason,
		&recommendedScore,
		&out.RecommendationBy,
		&recommendationAt,
		&out.ReviewedBy,
		&reviewedAt,
		&out.ClosedBy,
		&closedAt,
		&out.Revision,
		&out.CreatedBy,
		&out.CreatedAt,
		&out.UpdatedAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Appeal{}, ErrNotFound
		}
		return Appeal{}, err
	}
	_ = json.Unmarshal(attachmentRaw, &out.Attachment)
	if recommendedScore.Valid {
		out.RecommendedScore = &recommendedScore.Float64
	}
	if recommendationAt.Valid {
		out.RecommendationAt = &recommendationAt.Time
	}
	if reviewedAt.Valid {
		out.ReviewedAt = &reviewedAt.Time
	}
	if closedAt.Valid {
		out.ClosedAt = &closedAt.Time
	}
	out.CreatedAt = out.CreatedAt.UTC()
	out.UpdatedAt = out.UpdatedAt.UTC()
	return out, nil
}

func scanAppealSummary(row scanner) (Appeal, error) {
	var out Appeal
	var attachmentRaw []byte
	var recommendedScore sql.NullFloat64
	var recommendationAt sql.NullTime
	var reviewedAt sql.NullTime
	var closedAt sql.NullTime
	if err := row.Scan(
		&out.ID,
		&out.TenantID,
		&out.ExamID,
		&out.SubmissionID,
		&out.SubmissionGradeID,
		&out.StudentID,
		&out.TargetType,
		&out.FinalGradeID,
		&out.QuestionID,
		&out.QuestionNo,
		&out.DeductionPointID,
		&out.Reason,
		&attachmentRaw,
		&out.Status,
		&out.ResultReason,
		&out.AssignedTo,
		&out.Recommendation,
		&out.RecommendationReason,
		&recommendedScore,
		&out.RecommendationBy,
		&recommendationAt,
		&out.ReviewedBy,
		&reviewedAt,
		&out.ClosedBy,
		&closedAt,
		&out.Revision,
		&out.CreatedBy,
		&out.CreatedAt,
		&out.UpdatedAt,
		&out.ExamName,
		&out.Subject,
		&out.AnonymousCode,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Appeal{}, ErrNotFound
		}
		return Appeal{}, err
	}
	_ = json.Unmarshal(attachmentRaw, &out.Attachment)
	if recommendedScore.Valid {
		out.RecommendedScore = &recommendedScore.Float64
	}
	if recommendationAt.Valid {
		out.RecommendationAt = &recommendationAt.Time
	}
	if reviewedAt.Valid {
		out.ReviewedAt = &reviewedAt.Time
	}
	if closedAt.Valid {
		out.ClosedAt = &closedAt.Time
	}
	out.CreatedAt = out.CreatedAt.UTC()
	out.UpdatedAt = out.UpdatedAt.UTC()
	return out, nil
}
