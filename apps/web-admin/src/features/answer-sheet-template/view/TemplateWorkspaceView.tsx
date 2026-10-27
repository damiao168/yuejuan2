// 保留原公开导出作为页面组合入口；各领域状态和请求生命周期继续由原 hooks 拥有。

import { Alert } from 'antd';
import { ObjectiveGridDrawer } from './ObjectiveGridDrawer';
import type { TemplatePageViewProps } from './templateViewTypes';
import { TemplateQuestionPane } from './TemplateQuestionPane';
import { TemplateCanvas } from './TemplateCanvasView';
import { TemplateSettingsDrawer } from './TemplateSettingsView';
export { SourceUploadStatus, EmptyAnswerSheetSources, TemplateToolbar } from './TemplateSourceToolbarView';
export { TemplateQuestionPane } from './TemplateQuestionPane';
export { TemplateCanvas } from './TemplateCanvasView';
export { RegionGeometry, OptionRegionEditor } from './TemplateRegionSettingsView';
export { TemplateBindingPanel } from './TemplateBindingView';
export { TemplateSettingsDrawer } from './TemplateSettingsView';

export function TemplateWorkspace({ view }: TemplatePageViewProps) {
	const { selectedTemplate } = view;
	const { preview } = view.previewController;
	if (!selectedTemplate)
		return (
			<Alert
				type="info"
				showIcon
				message="先创建模板草稿"
				description={
					preview.loading
						? "正在读取答题卡，请稍候。"
						: preview.error ||
              `系统已读取答题卡，共 ${preview.pageCount} 页。新建区域模板后将自动识别整份答题卡并对应题目，发现错误可直接修改。`
				}
			/>
		);
	return (
		<><section className="template-workbench">
			<TemplateQuestionPane view={view} />
			<TemplateCanvas view={view} />
		</section>
    <TemplateSettingsDrawer view={view} />
    {view.editor.objectiveGrid ? <ObjectiveGridDrawer grid={view.editor.objectiveGrid} imageURL={preview.imageUrl}
      pageNo={view.previewController.pageNo} readonly={view.editor.readonly} /> : null}
    </>
	);
}
