import type { TemplateLayout } from "../../../api/configuration";

// 独立整区的成员已有答题区域，不要求再画单题 ROI；草稿、锁定绑定与学校库收录共用这一覆盖口径。
export function templateCoveredQuestions(layout: TemplateLayout): Set<string> {
  return new Set(layout.pages.flatMap(page => [
    ...page.question_regions.flatMap(region => region.question_id ? [region.question_id] : []),
    ...(page.grading_groups ?? []).flatMap(group => group.question_ids)
  ]));
}
