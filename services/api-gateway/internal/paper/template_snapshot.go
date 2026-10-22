package paper

import "slices"

// cloneTemplateSnapshot 隔离内存仓储与调用方的布局切片、审核原因和草稿候选。
// 否则返回值上的改框可以绕过仓储写锁、版本递增和锁定门禁。
func cloneTemplateSnapshot(item AnswerSheetTemplate) AnswerSheetTemplate {
	item.Layout = cloneTemplateLayout(item.Layout)
	if item.LockedAt != nil {
		value := *item.LockedAt
		item.LockedAt = &value
	}
	return item
}

func cloneTemplateLayout(layout TemplateLayout) TemplateLayout {
	layout.Pages = slices.Clone(layout.Pages)
	for index := range layout.Pages {
		page := &layout.Pages[index]
		page.RegistrationMarks = cloneTemplateRegions(page.RegistrationMarks)
		page.IdentityRegions = cloneTemplateRegions(page.IdentityRegions)
		page.QuestionRegions = cloneTemplateRegions(page.QuestionRegions)
		page.GradingGroups = cloneTemplateGradingGroups(page.GradingGroups)
	}
	if layout.OMRProfile.Reference != nil {
		value := *layout.OMRProfile.Reference
		layout.OMRProfile.Reference = &value
	}
	if layout.EditorState != nil {
		value := *layout.EditorState
		value.Candidates = slices.Clone(value.Candidates)
		for index := range value.Candidates {
			value.Candidates[index].Region = cloneTemplateRegion(value.Candidates[index].Region)
		}
		layout.EditorState = &value
	}
	return layout
}

func cloneTemplateGradingGroups(groups []TemplateGradingGroup) []TemplateGradingGroup {
	groups = slices.Clone(groups)
	for index := range groups {
		groups[index].QuestionIDs = slices.Clone(groups[index].QuestionIDs)
	}
	return groups
}

func cloneTemplateRegions(regions []LayoutRegion) []LayoutRegion {
	regions = slices.Clone(regions)
	for index := range regions {
		regions[index] = cloneTemplateRegion(regions[index])
	}
	return regions
}

func cloneTemplateRegion(region LayoutRegion) LayoutRegion {
	region.OptionRegions = slices.Clone(region.OptionRegions)
	region.ReasonCodes = slices.Clone(region.ReasonCodes)
	if region.GradingBBox != nil {
		box := *region.GradingBBox
		region.GradingBBox = &box
	}
	return region
}
