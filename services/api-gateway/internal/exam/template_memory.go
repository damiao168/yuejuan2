package exam

import (
	"context"

	"edugrade-enterprise/services/api-gateway/internal/auth"
)

func (s *MemoryStore) ListExamTemplates(_ context.Context, _ auth.AccessScope, filter ExamTemplateFilter) ([]ExamTemplate, error) {
	out := []ExamTemplate{}
	for _, template := range defaultExamTemplates() {
		if filter.EducationStage != "" && template.EducationStage != filter.EducationStage {
			continue
		}
		if filter.ExamType != "" && template.ExamType != "" && template.ExamType != filter.ExamType {
			continue
		}
		out = append(out, template)
	}
	return out, nil
}

func (s *MemoryStore) GetExamTemplate(ctx context.Context, scope auth.AccessScope, id string) (ExamTemplate, error) {
	templates, _ := s.ListExamTemplates(ctx, scope, ExamTemplateFilter{})
	return templateByID(templates, id)
}

func defaultExamTemplates() []ExamTemplate {
	// 内存实现只有共享的系统模板，没有学校私有模板的持久化数据。
	section := func(id, title, kind string, count int, score float64, order int) ExamTemplateSection {
		return ExamTemplateSection{ID: id, Title: title, QuestionType: kind, QuestionCount: count, ScorePerQuestion: score, SortOrder: order}
	}
	subject := func(id, code string, total float64, duration, order int, sections ...ExamTemplateSection) ExamTemplateSubject {
		return ExamTemplateSubject{ID: id, Subject: code, TotalScore: total, DurationMinutes: duration, CandidateRule: "all_selected_classes", SortOrder: order, Sections: sections}
	}
	return []ExamTemplate{
		{ID: "00000000-0000-0000-0000-000000000601", TenantID: auth.PlatformTenantID, Code: "system.senior.standard", Name: "系统通用高中考试方案", Description: "适合校内期中、期末和阶段考试，可在本场考试中继续修改。", EducationStage: "senior", Version: 1, Source: "system", Recommended: true, Subjects: []ExamTemplateSubject{
			subject("senior-chinese", "chinese", 150, 150, 1, section("sc-1", "基础与阅读", "single_choice", 10, 3, 1), section("sc-2", "阅读与表达", "short_answer", 6, 10, 2), section("sc-3", "写作", "essay", 1, 60, 3)),
			subject("senior-math", "math", 150, 120, 2, section("sm-1", "单项选择", "single_choice", 8, 5, 1), section("sm-2", "多项选择", "multiple_choice", 3, 6, 2), section("sm-3", "填空", "fill_blank", 3, 5, 3), section("sm-4", "解答", "calculation", 7, 11, 4)),
			subject("senior-english", "english", 150, 120, 3, section("se-1", "客观题", "single_choice", 20, 4, 1), section("se-2", "语言运用", "short_answer", 5, 8, 2), section("se-3", "写作", "essay", 1, 30, 3)),
		}},
		{ID: "00000000-0000-0000-0000-000000000602", TenantID: auth.PlatformTenantID, Code: "system.junior.standard", Name: "系统通用初中考试方案", Description: "适合初中校内阶段考试，可按学校实际科目和题型调整。", EducationStage: "junior", Version: 1, Source: "system", Recommended: true, Subjects: []ExamTemplateSubject{
			subject("junior-chinese", "chinese", 120, 120, 1, section("jc-1", "基础与阅读", "single_choice", 8, 5, 1), section("jc-2", "阅读与表达", "short_answer", 4, 10, 2), section("jc-3", "写作", "essay", 1, 40, 3)),
			subject("junior-math", "math", 120, 120, 2, section("jm-1", "客观题", "single_choice", 12, 5, 1), section("jm-2", "解答题", "calculation", 6, 10, 2)),
			subject("junior-english", "english", 120, 120, 3, section("je-1", "客观题", "single_choice", 15, 4, 1), section("je-2", "语言运用", "short_answer", 4, 10, 2), section("je-3", "写作", "essay", 1, 20, 3)),
		}},
	}
}
