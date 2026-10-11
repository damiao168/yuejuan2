package exam

import (
	"fmt"
	"sort"
	"strings"

	"edugrade-enterprise/services/api-gateway/internal/auth"
)

var managementStatusLabels = map[string]string{
	"draft": "草稿", "configured": "准备中", "ready": "准备完成",
	"collecting": "答卷导入中", "grading": "阅卷中", "reviewing": "复核中",
	"finalized": "已定稿", "published": "已发布", "archived": "已归档",
}

func managementProgress(exams []Exam, facts map[string]managementSubjectFacts, archived bool) ManagementProgress {
	p := ManagementProgress{TotalCount: len(exams), Counts: map[string]int{}, FactsAvailable: true}
	for _, child := range exams {
		p.Counts[child.Status]++
		fact := facts[child.ID]
		p.FactsAvailable = p.FactsAvailable && fact.Available
		if fact.Ready {
			p.ReadyCount++
		}
		if fact.Published {
			p.PublishedCount++
		}
	}
	if archived {
		p.Status, p.Label = "archived", "已归档"
		return p
	}
	if p.TotalCount == 0 {
		p.Status, p.Label = "unavailable", "暂无法获取进度"
		return p
	}
	if p.PublishedCount == p.TotalCount {
		p.Status, p.Label = "published", "已发布"
		return p
	}
	if p.PublishedCount > 0 {
		p.Status, p.Label = "partially_published", fmt.Sprintf("部分已发布 · %d/%d 科", p.PublishedCount, p.TotalCount)
		return p
	}
	if p.Counts["draft"] == p.TotalCount {
		p.Status, p.Label = "draft", "草稿"
		return p
	}
	preparation := p.Counts["draft"] + p.Counts["configured"] + p.Counts["ready"]
	if preparation == p.TotalCount {
		return managementPreparationProgress(p)
	}
	return managementOngoingProgress(p)
}

func managementPreparationProgress(p ManagementProgress) ManagementProgress {
	if p.FactsAvailable && p.ReadyCount == p.TotalCount {
		p.Status, p.Label = "ready", "准备完成"
		return p
	}
	if !p.FactsAvailable && p.Counts["ready"] > 0 {
		p.Status, p.Label = "unavailable", "暂无法确认准备进度"
		return p
	}
	p.Status, p.Label = "configured", fmt.Sprintf("准备中 · %d/%d 科准备完成", p.ReadyCount, p.TotalCount)
	return p
}

func managementOngoingProgress(p ManagementProgress) ManagementProgress {
	// 单科的已发布状态不能替代正式发布记录；缺少发布事实时不能显示已发布。
	if p.Counts["published"] > 0 {
		p.Status, p.Label = "unavailable", "暂无法确认发布进度"
		return p
	}
	if len(p.Counts) == 1 {
		for status := range p.Counts {
			p.Status, p.Label = status, managementStatusLabels[status]
		}
		if p.Label != "" {
			return p
		}
	}
	parts := []string{}
	for _, status := range []string{"draft", "configured", "ready", "collecting", "grading", "reviewing", "finalized", "archived"} {
		if count := p.Counts[status]; count > 0 {
			parts = append(parts, fmt.Sprintf("%d 科%s", count, managementStatusLabels[status]))
		}
	}
	if len(parts) == 0 {
		p.Status, p.Label = "unavailable", "暂无法获取进度"
		return p
	}
	p.Status, p.Label = "ongoing", "进行中 · "+strings.Join(parts, "、")
	return p
}

func managementSchoolWide(scope auth.AccessScope, schoolID string) bool {
	return scope.TenantWide || (scope.IsSchoolWide() && scope.AllowsSchool(schoolID))
}

func managementScopeAllowsExam(scope auth.AccessScope, child Exam) bool {
	// 班级身份解析会附带学校 ID，不能因此获得同校全部学科的读取权限。
	if managementSchoolWide(scope, child.SchoolID) || scope.AllowsExam(child.ID) {
		return true
	}
	for _, classID := range child.ClassIDs {
		if scope.AllowsClass(classID) {
			return true
		}
	}
	return false
}

func managementScopeCanMutateExam(scope auth.AccessScope, child Exam) bool {
	if managementSchoolWide(scope, child.SchoolID) {
		return true
	}
	// 班级和任务身份会派生 ExamIDs 供导航；它们不能替代全部参考班级的整场写权限。
	if scope.AssignedOnly {
		return false
	}
	if len(scope.ClassIDs) == 0 {
		return scope.AllowsExam(child.ID)
	}
	if len(child.ClassIDs) == 0 {
		return false
	}
	for _, id := range child.ClassIDs {
		if !scope.AllowsClass(id) {
			return false
		}
	}
	return true
}

func managementScopeAllowsRequestedClasses(scope auth.AccessScope, schoolID string, classIDs []string) bool {
	if managementSchoolWide(scope, schoolID) {
		return true
	}
	if len(classIDs) == 0 || scope.AssignedOnly {
		return false
	}
	for _, id := range classIDs {
		if !scope.AllowsClass(id) {
			return false
		}
	}
	return true
}

func completeManagementExam(out *ManagementExam, scope auth.AccessScope, total int, rules map[string]string, facts map[string]managementSubjectFacts) {
	out.ScopeComplete = total == len(out.Exams)
	out.CanManage = out.ScopeComplete && total > 0
	legacyArchived := completeManagementSubjectScope(out, scope, rules)
	// 默认班级和继承科目先按原顺序汇总；展示排序不能改变继承科目数组的顺序。
	sort.Slice(out.Exams, func(i, j int) bool {
		if out.Exams[i].Subject == out.Exams[j].Subject {
			return out.Exams[i].ID < out.Exams[j].ID
		}
		return out.Exams[i].Subject < out.Exams[j].Subject
	})
	out.CanEdit = out.CanManage && out.ArchivedAt == nil && !legacyArchived && len(out.ArchivedSubjectIDs) == 0
	out.CanRestore = out.CanManage && out.ArchivedAt != nil && !legacyArchived && len(out.ArchivedSubjectIDs) == 0
	out.Progress = managementProgress(out.Exams, facts, out.ArchivedAt != nil || out.LegacyArchived || (legacyArchived && len(out.Exams) == 1))
}

func completeManagementSubjectScope(out *ManagementExam, scope auth.AccessScope, rules map[string]string) bool {
	// CanManage 已由整场可见范围初始化，这里逐科收紧，不能用班级并集扩大写权限。
	allClasses, defaults := []string{}, []string{}
	out.InheritedSubjectIDs = []string{}
	if out.ArchivedSubjectIDs == nil {
		out.ArchivedSubjectIDs = []string{}
	}
	legacyArchived := out.LegacyArchived
	for _, child := range out.Exams {
		out.CanManage = out.CanManage && managementScopeCanMutateExam(scope, child)
		allClasses = append(allClasses, child.ClassIDs...)
		if rules[child.ID] != "subject_selected_classes" {
			defaults = append(defaults, child.ClassIDs...)
			out.InheritedSubjectIDs = append(out.InheritedSubjectIDs, child.ID)
		}
		legacyArchived = legacyArchived || child.Status == "archived"
	}
	out.ClassIDs, out.DefaultClassIDs = normalizedClassIDs(allClasses), normalizedClassIDs(defaults)
	return legacyArchived
}

func managementMatchesFilter(item ManagementExam, filter ManagementListFilter) bool {
	archived := item.ArchivedAt != nil || item.Progress.Status == "archived"
	if filter.Archive == "active" && archived || filter.Archive == "archived" && !archived {
		return false
	}
	if filter.SchoolID != "" && item.SchoolID != filter.SchoolID || filter.GradeID != "" && item.GradeID != filter.GradeID || filter.ExamType != "" && item.ExamType != filter.ExamType {
		return false
	}
	if filter.Search != "" && !strings.Contains(strings.ToLower(item.Name), strings.ToLower(filter.Search)) {
		return false
	}
	return filter.Status == "" || item.Progress.Status == filter.Status
}

func validateManagementUpdate(input ManagementUpdateInput) error {
	if input.ExpectedRevision <= 0 || (input.Name == nil && input.ExamType == nil && input.ClassIDs == nil) {
		return ErrInvalidInput
	}
	if input.Name != nil && (strings.TrimSpace(*input.Name) == "" || len(*input.Name) > 600) {
		return ErrInvalidInput
	}
	if input.ExamType != nil && (strings.TrimSpace(*input.ExamType) == "" || len(*input.ExamType) > 200) {
		return ErrInvalidInput
	}
	return validateManagementClassSelection(input.ClassIDs)
}

func validateManagementClassSelection(classIDs *[]string) error {
	// 未提交班级字段表示保持原配置；显式空数组不能隐式清空参考班级。
	if classIDs == nil {
		return nil
	}
	if len(*classIDs) == 0 {
		return ErrInvalidInput
	}
	for _, id := range *classIDs {
		if strings.TrimSpace(id) == "" {
			return ErrInvalidInput
		}
	}
	return nil
}

func requireManagementRevisions(item ManagementExam, input ManagementUpdateInput) error {
	if item.Revision != input.ExpectedRevision {
		return ErrRevisionConflict
	}
	if item.Kind == "session" {
		// 父级版本不会随单科配置自动增长，因此整场编辑还要比对所有学科版本。
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
