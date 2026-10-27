import { Button, Drawer, Input } from 'antd';
import { Trash2 } from 'lucide-react';
import { StatusTag } from '../../../components/StatusTag';
import { CalibrationPanel, OMRProfilePanel } from './OMRCalibrationView';
import { StudentPrintPanel } from './StudentPrintingView';
import { normalizeIdentityRegionLabel } from '../editor/identityRegionFields';
import { regionRequiresReview } from '../editor/regionLifecycle';
import type { TemplateSettingsView } from './templateViewTypes';
import { RegionGeometry, OptionRegionEditor } from './TemplateRegionSettingsView';
import { TemplateBindingPanel } from './TemplateBindingView';

export function TemplateSettingsDrawer({ view }: Readonly<{ view: TemplateSettingsView }>) {
	const { selectedTemplate, questions } = view;
  const selectedIdentity = view.editor.selectedRegionKind === "identity";
	const {
		name,
		setName,
		readonly,
		selectedRegionId,
		removeRegion,
		layout,
		coveredQuestions
	} = view.editor;
	if (!selectedTemplate || !view.settingsOpen) return null;
	return (
		<Drawer title="模板设置" open={view.settingsOpen} onClose={() => view.setSettingsOpen(false)} width={420}
      rootClassName="template-settings-drawer" destroyOnHidden>
      <div className="template-settings-content">
			<div className="template-pane-head">
				<strong>当前模板 v{selectedTemplate.version_no}</strong>
				{selectedTemplate.status === "locked" ? (
					<StatusTag tone="success">已锁定</StatusTag>
				) : (
					<StatusTag tone="processing">草稿</StatusTag>
				)}
			</div>
			<label>
				<span>模板名称</span>
				<Input
					value={name}
					disabled={readonly}
					onChange={(event) => setName(event.target.value)}
				/>
			</label>
			<TemplateBindingPanel view={view} />
      <details className="template-settings-omr"><summary>选择题识别方式</summary><OMRProfilePanel view={view} /></details>
      {view.editor.selectedRegion ? <div className="template-current-region-settings">
        <strong>当前选区：{selectedIdentity ? normalizeIdentityRegionLabel(view.editor.selectedRegion.label) : `第 ${view.editor.selectedRegion.label || view.editor.selectedRegion.question_id} 题`}</strong>
        <RegionGeometry view={view} />
        <OptionRegionEditor view={view} />
        {regionRequiresReview(view.editor.selectedRegion) ? <Button disabled={readonly}
          onClick={() => view.editor.confirmRegion?.(view.editor.selectedRegion!.id)}>确认此异常区域可用</Button> : null}
			{selectedRegionId ? (
				<Button
					danger
					icon={<Trash2 size={16} />}
					disabled={readonly}
					onClick={() => removeRegion(selectedRegionId)}
				>
					删除所选区域
				</Button>
			) : null}
      </div> : null}
      <details className="template-settings-printing"><summary>校准与打印</summary>
			<CalibrationPanel view={view} />
			<StudentPrintPanel view={view} />
      </details>
			<div className="template-summary">
				<span>页面</span>
				<strong>{layout.pages.length}</strong>
				<span>题目区域</span>
				<strong>{coveredQuestions.size}</strong>
				<span>未配置</span>
				<strong>{Math.max(questions.length - coveredQuestions.size, 0)}</strong>
			</div>
      </div>
		</Drawer>
	);
}
