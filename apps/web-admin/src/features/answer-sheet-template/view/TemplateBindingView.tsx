import { Button } from 'antd';
import { CheckCircle2 } from 'lucide-react';
import { StatusTag } from '../../../components/StatusTag';
import type { TemplateBindingView } from './templateViewTypes';
import { templateConfirmationState, bindingDescription } from './templateViewRules';

export function TemplateBindingPanel({ view }: Readonly<{ view: TemplateBindingView }>) {
	const { canManage, canEditSources, sourceUpload, selectedTemplate } = view;
	const { saving, confirmActivate } = view.editor;
	const { examBinding, bindingBusy, releaseExamBinding, bindTemplateForExam } = view.binding;
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
			{!confirmation.usageConfirmed ? <>
				{confirmation.disabledReason ? <p className="template-confirmation-reason">{confirmation.disabledReason}</p> : null}
				<Button type="primary" block icon={<CheckCircle2 size={16} />} disabled={confirmation.disabled} loading={saving || bindingBusy}
					onClick={() => {
            if (confirmation.disabled || !selectedTemplate) {
              return;
            }
						if (confirmation.draft) confirmActivate();
						else bindTemplateForExam(selectedTemplate);
					}}>确认答题卡设置</Button>
				{confirmation.draft ? <p>确认将保存并锁定当前版本；修改已锁定区域须克隆新版本。</p> : null}
			</> : null}
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
				<p>当前版本尚未明确用于本场考试。区域检查与本场模板使用设置分别确认。</p>
			)}
		</section>
	);
}
