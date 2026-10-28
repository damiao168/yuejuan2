import { Button } from 'antd';
import { CheckCircle2 } from 'lucide-react';
import { StatusTag } from '../../../components/StatusTag';
import type { TemplateBindingView } from './templateViewTypes';
import { templateConfirmationState, bindingDescription } from './templateViewRules';

export function TemplateBindingPanel({ view, showAction = true }: Readonly<{ view: TemplateBindingView; showAction?: boolean }>) {
	const { canManage, canEditSources, sourceUpload, selectedTemplate } = view;
	const { examBinding, bindingBusy, releaseExamBinding } = view.binding;
	const confirmation = templateConfirmationState(view);
	return (
		<section id="template-exam-binding" tabIndex={-1} className="template-option-editor template-binding-confirmation" aria-label="本场考试模板设置">
			<div className="template-pane-head">
				<strong>本场考试模板</strong>
				<StatusTag tone={confirmation.usageConfirmed ? "success" : "warning"}>
					{confirmation.usageConfirmed ? "使用设置已确认" : "使用设置待确认"}
				</StatusTag>
			</div>
			<p className="template-binding-version">选中版本 v{selectedTemplate?.version_no} · {selectedTemplate?.status === "locked" ? "已锁定" : "草稿"}</p>
      {showAction ? <TemplateConfirmationAction view={view} /> : null}
			{examBinding ? (
				<>
					<p>{bindingDescription(view)}</p>
					<details className="template-binding-details">
						<summary>查看绑定校验码</summary>
						<span title={examBinding.template_content_hash}>模板校验码：{examBinding.template_content_hash.slice(0, 16)}</span>
					</details>
					{canManage ? (
						<Button
							danger
							block
							disabled={!canEditSources || sourceUpload.uploading}
							loading={bindingBusy}
							onClick={releaseExamBinding}
						>
							解除本场绑定
						</Button>
					) : null}
				</>
			) : (
				<p>此版本尚未用于本场考试，请确认答题卡设置。</p>
			)}
		</section>
	);
}

// 主完成入口使用与设置面板相同的完整门禁；移动位置不能绕过区域覆盖、版本与本场绑定校验。
export function TemplateConfirmationAction({ view }: Readonly<{ view: TemplateBindingView }>) {
  const confirmation = templateConfirmationState(view);
  if (!view.selectedTemplate) return null;
  if (confirmation.usageConfirmed) return <StatusTag tone="success">答题卡设置已确认</StatusTag>;
  return <div className="template-primary-confirmation">
    <Button type="primary" icon={<CheckCircle2 size={16} />} disabled={confirmation.disabled}
      loading={view.editor.saving || view.binding.bindingBusy} onClick={() => {
        if (confirmation.disabled || !view.selectedTemplate) return;
        if (confirmation.draft) view.editor.confirmActivate();
        else void view.binding.bindTemplateForExam(view.selectedTemplate);
      }}>确认答题卡设置</Button>
    <p className="template-confirmation-reason">{confirmation.disabledReason || (confirmation.draft
      ? "确认后保存并锁定当前版本；修改区域需另建版本。" : "核对后将此版本用于本场考试。")}</p>
  </div>;
}
