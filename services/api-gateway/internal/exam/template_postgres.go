package exam

import (
	"context"
	"database/sql"
	"errors"

	"edugrade-enterprise/services/api-gateway/internal/auth"
)

func (s *PostgresStore) ListExamTemplates(ctx context.Context, scope auth.AccessScope, filter ExamTemplateFilter) ([]ExamTemplate, error) {
	// 可见模板来自本租户或平台；学校专用模板还须满足当前学校范围。
	rows, err := s.db.QueryContext(ctx, `
SELECT id::text,tenant_id::text,COALESCE(school_id::text,''),code,name,description,
       education_stage,COALESCE(exam_type,''),COALESCE(region,''),COALESCE(curriculum,''),
       version,source,is_recommended
FROM exam_template
WHERE deleted_at IS NULL AND status='active'
  AND (tenant_id=$1::uuid OR tenant_id=$2::uuid)
  AND ($3='' OR education_stage=$3)
  AND ($4='' OR exam_type='' OR exam_type=$4)
  AND (valid_from IS NULL OR valid_from <= CURRENT_DATE)
  AND (valid_to IS NULL OR valid_to >= CURRENT_DATE)
  AND (school_id IS NULL OR $5::boolean OR school_id::text IN (SELECT jsonb_array_elements_text($6::jsonb)))
ORDER BY is_recommended DESC, source DESC, version DESC, name
`, scope.TenantID, auth.PlatformTenantID, filter.EducationStage, filter.ExamType, scope.TenantWide || scope.IsPlatform, scopeIDsJSON(scope.SchoolIDs))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ExamTemplate{}
	for rows.Next() {
		var item ExamTemplate
		if err := rows.Scan(&item.ID, &item.TenantID, &item.SchoolID, &item.Code, &item.Name, &item.Description, &item.EducationStage, &item.ExamType, &item.Region, &item.Curriculum, &item.Version, &item.Source, &item.Recommended); err != nil {
			return nil, err
		}
		if err := s.loadTemplateSubjects(ctx, &item); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *PostgresStore) GetExamTemplate(ctx context.Context, scope auth.AccessScope, id string) (ExamTemplate, error) {
	templates, err := s.ListExamTemplates(ctx, scope, ExamTemplateFilter{})
	if err != nil {
		return ExamTemplate{}, err
	}
	return templateByID(templates, id)
}

func (s *PostgresStore) loadTemplateSubjects(ctx context.Context, template *ExamTemplate) error {
	rows, err := s.db.QueryContext(ctx, `
SELECT id::text,subject_code,total_score::float8,duration_minutes,candidate_rule,sort_order
FROM exam_template_subject
WHERE tenant_id=$1::uuid AND exam_template_id=$2::uuid AND deleted_at IS NULL
ORDER BY sort_order,id
`, template.TenantID, template.ID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var subject ExamTemplateSubject
		if err := rows.Scan(&subject.ID, &subject.Subject, &subject.TotalScore, &subject.DurationMinutes, &subject.CandidateRule, &subject.SortOrder); err != nil {
			return err
		}
		sectionRows, err := s.db.QueryContext(ctx, `
SELECT id::text,title,question_type,question_count,score_per_question::float8,sort_order
FROM exam_template_section
WHERE tenant_id=$1::uuid AND exam_template_subject_id=$2::uuid AND deleted_at IS NULL
ORDER BY sort_order,id
`, template.TenantID, subject.ID)
		if err != nil {
			return err
		}
		for sectionRows.Next() {
			var section ExamTemplateSection
			if err := sectionRows.Scan(&section.ID, &section.Title, &section.QuestionType, &section.QuestionCount, &section.ScorePerQuestion, &section.SortOrder); err != nil {
				sectionRows.Close()
				return err
			}
			subject.Sections = append(subject.Sections, section)
		}
		if err := sectionRows.Err(); err != nil {
			sectionRows.Close()
			return err
		}
		sectionRows.Close()
		template.Subjects = append(template.Subjects, subject)
	}
	return rows.Err()
}

func requireExamTemplate(ctx context.Context, tx *sql.Tx, scope auth.AccessScope, templateID string) (int, error) {
	if templateID == "" {
		return 0, nil
	}
	var version int
	err := tx.QueryRowContext(ctx, `
SELECT version FROM exam_template
WHERE id=$1::uuid AND deleted_at IS NULL AND status='active'
  AND (tenant_id=$2::uuid OR tenant_id=$3::uuid)
  AND (school_id IS NULL OR $4::boolean OR school_id::text IN (SELECT jsonb_array_elements_text($5::jsonb)))
`, templateID, scope.TenantID, auth.PlatformTenantID, scope.TenantWide || scope.IsPlatform, scopeIDsJSON(scope.SchoolIDs)).Scan(&version)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrInvalidInput
	}
	return version, err
}
