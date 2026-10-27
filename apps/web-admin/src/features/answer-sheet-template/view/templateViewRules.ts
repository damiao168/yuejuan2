// 确认规则只读取当前模板、覆盖和操作状态；锁定与本场绑定仍由既有控制器执行。

import { templateCoveredQuestions } from '../editor/templateCoverage';
import type { TemplateConfirmationView } from './templateViewTypes';

export const coordLabels = { x: "左", y: "上", width: "宽", height: "高" } as const;

export function clamp(value: number, min = 0, max = 1) {
	return Math.min(max, Math.max(min, value));
}

export function isInputTarget(target: EventTarget | null) {
  return target instanceof Element && Boolean(target.closest("input, textarea, select, [contenteditable='true'], [role='textbox'], [role='combobox']"));
}

export function isDialogTarget(target: EventTarget | null) {
  return target instanceof Element && Boolean(target.closest("[role='dialog'], [role='alertdialog']"));
}

export function selectionUsageConfirmed(view: TemplateConfirmationView) {
  const { selectedTemplate } = view;
  const { examBinding } = view.binding;
  return selectedTemplate?.status === "locked" && Boolean(examBinding?.exam_id === selectedTemplate.exam_id
    && examBinding.template_id === selectedTemplate.id && examBinding.mode === "locked_with_guard"
    && selectedTemplate.content_hash && examBinding.template_content_hash === selectedTemplate.content_hash);
}

export function templateDisabledReason(view: TemplateConfirmationView, missingCount: number) {
  const { selectedTemplate, questions, canManage, canEditSources, sourceUpload, printing } = view;
  const { saving, suggestingRegions, readonly } = view.editor;
  const { bindingBusy } = view.binding;
  const draft = selectedTemplate?.status === "draft";
  // 区域识别允许部分成功；只有最终确认设置仍要求实际覆盖全部题目，原因留在确认区。
  const reasons: [boolean, string][] = [
    [!canManage, "请联系考试负责人核对并确认。"],
    [!canEditSources, "考试准备已冻结，不能再修改或确认答题卡设置。"],
    [!selectedTemplate, "请先新建或选择答题卡模板。"],
    [sourceUpload.uploading, "正在上传答题卡，完成后可继续确认。"],
    [saving, "正在保存模板，完成后可继续确认。"],
    [suggestingRegions, "正在识别并自动对应题目，请稍候。"],
    [printing.printBusy, "请等待当前打印操作完成后再确认。"],
    [bindingBusy, "正在更新本场绑定，请稍候。"],
    [!questions.length, "请先完成小题配置。"],
    [Boolean(view.editor.objectiveGroups?.size), "选择题区域尚未核对，请点击大框核对题号和选项。"],
    [Boolean(view.editor.reviewMessage), view.editor.reviewMessage ?? ""],
    [missingCount > 0, `还有 ${missingCount} 道题未配置答题区域。`],
    [draft && readonly, "当前模板只读，暂不能确认。"],
    [Boolean(selectedTemplate && !["draft", "locked"].includes(selectedTemplate.status)), "请先选择草稿或已锁定的模板。"]
  ];
  return reasons.find(([blocked]) => blocked)?.[1] ?? "";
}

export function templateConfirmationState(view: TemplateConfirmationView) {
  const { selectedTemplate, questions } = view;
  const { coveredQuestions } = view.editor;
  const draft = selectedTemplate?.status === "draft";
  // 当前框选只指导确认按钮；锁定并绑定内容版本和区域准备检查是两个独立事实。
	const savedCoverage = selectedTemplate ? templateCoveredQuestions(selectedTemplate.layout) : new Set<string>();
	const missing = questions.filter(question => !(draft ? coveredQuestions : savedCoverage).has(question.id));
  const disabledReason = templateDisabledReason(view, missing.length);
  return { draft, usageConfirmed: selectionUsageConfirmed(view), disabled: Boolean(disabledReason), disabledReason };
}

export function bindingDescription(view: TemplateConfirmationView) {
	const { examBinding } = view.binding;
	const { selectedTemplate } = view;
	if (!selectedTemplate || !examBinding) return "";
	if (examBinding.template_id !== selectedTemplate.id)
		return "本场考试已绑定其他模板版本，请先核对当前答题卡设置。";
	if (examBinding.mode === "bound_auto")
		return `首张答卷自动识别为 v${selectedTemplate.version_no}，仍需人工核对并确认。`;
	if (!templateConfirmationState(view).usageConfirmed)
		return "绑定所属考试或内容版本待核对，请核对后重新确认。";
	return `当前版本 v${selectedTemplate.version_no} 已用于本场考试；后续页面会先做一致性检查。`;
}
