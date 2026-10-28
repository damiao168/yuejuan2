import { useEffect, useState } from 'react';
import { Alert, Button, Progress, Popover, Select, Space, Upload } from 'antd';
import { Copy, MoreHorizontal, Plus, Redo2, Save, Settings2, Trash2, Undo2, Upload as UploadIcon, WandSparkles } from 'lucide-react';
import { EmptyState } from '../../../components/PageState';
import { ClearTemplateQuestionRegionsButton } from './ClearTemplateQuestionRegionsButton';
import type { TemplatePageViewState, TemplateSourceStatusView, TemplateSourceToolbarView } from './templateViewTypes';
import { isDialogTarget } from './templateViewRules';
import { TemplateConfirmationAction } from './TemplateBindingView';

export function SourceUploadStatus({ view }: Readonly<{ view: TemplateSourceStatusView }>) {
	const { sourceUpload, sourceManagement, uploadProps, confirmDiscardTemplate } = view;
  if (sourceManagement?.deleteError) return <Alert type="error" showIcon message={sourceManagement.deleteError} />;
	if (sourceUpload.error)
		return (
			<Alert
				type="error"
				showIcon
				message={sourceUpload.error}
				action={
					sourceUpload.canRetry ? (
						<Button
							disabled={uploadProps.disabled}
							loading={sourceUpload.uploading}
							onClick={() =>
								confirmDiscardTemplate(() => void sourceUpload.retry())
							}
						>
							重试保存答题卡
						</Button>
					) : undefined
				}
			/>
		);
	if (sourceUpload.uploading)
		return <div className="template-source-upload-status">
      {sourceUpload.phase === "ordering" ? <span role="status">正在识别页序…</span> : null}
      <Progress percent={sourceUpload.progress} status="active" />
    </div>;
	return null;
}

export function EmptyAnswerSheetSources({ view }: Readonly<{ view: TemplateSourceStatusView & Pick<TemplatePageViewState, "canEditSources" | "canManage"> }>) {
	const { canEditSources, canManage, uploadProps, sourceUpload } = view;
	return (
		<div className="page-stack">
			<section className="page-heading">
				<div>
					<h2>答题卡设置</h2>
					<p>上传答题卡，核对识别出的题目与答题区域。</p>
				</div>
			</section>
			{canEditSources ? (
				<Upload.Dragger {...uploadProps}>
					<UploadIcon size={32} />
					<h3>上传答题卡</h3>
					<p>支持PDF或多张PNG、JPG图片。</p>
					<Button type="primary" loading={sourceUpload.uploading}>
						选择答题卡文件
					</Button>
				</Upload.Dragger>
			) : (
				<EmptyState
					title="尚未上传答题卡"
					description={
						canManage
							? "考试准备已确认，不能再上传新的答题卡。"
							: "请联系考试负责人上传答题卡。"
					}
				/>
			)}
			<SourceUploadStatus view={view} />
			<details><summary>答题卡来源与复用</summary><p>空白卡和已填写答题卡均可辅助识别；收录学校模板库前须使用不含个人信息的空白来源。</p></details>
		</div>
	);
}

export function TemplateToolbar({ view }: Readonly<{ view: TemplateSourceToolbarView }>) {
	const [moreOpen, setMoreOpen] = useState(false);
	const {
		papers,
		selectedPaperId,
		selectedTemplate,
		setSelectedPaperId,
		setSelectedTemplateId,
		canManage,
		canEditSources,
		sourceUpload,
		sourceManagement,
		uploadProps,
		confirmDiscardTemplate
	} = view;
	const {
		saving,
		suggestingRegions,
		setLayout,
		readonly,
		createDraft,
		cloneTemplate,
		suggestQuestionRegions,
		saveDraft
	} = view.editor;
	const { preview } = view.previewController;
	const { suggestionProgress, suggestionError, suggestionTone, suggestionModel } = view.editor;
  const pendingQuestionAreas = new Map(view.editor.unassignedRegions ?? []);
  for (const [pageNo, groups] of view.editor.objectiveGroups ?? []) {
    pendingQuestionAreas.set(pageNo, [...(pendingQuestionAreas.get(pageNo) ?? []), ...groups]);
  }
  useEffect(() => {
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key.toLowerCase() !== "s" || (!event.ctrlKey && !event.metaKey) || event.altKey || isDialogTarget(event.target)) return;
      // 普通页面仍展示考试导航；只接管编辑器内的保存快捷键，避免影响其它表单。
      if (!(event.target instanceof Element && event.target.closest(".template-editor-page"))) return;
      event.preventDefault();
      if (!event.repeat && selectedTemplate?.status === "draft" && !readonly && !saving && !suggestingRegions) void saveDraft();
    };
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, [selectedTemplate?.status, readonly, saving, suggestingRegions, saveDraft]);
  const sourceControls = <>
				<div className="template-source-file">
					<span className="muted">答题卡文件</span>
					<Select
						aria-label="答题卡文件"
						title="答题卡文件"
						disabled={saving || suggestingRegions || sourceUpload.uploading || sourceManagement?.deleting}
						value={selectedPaperId}
						options={papers.map((paper) => ({
							value: paper.id,
							label: paper.file.original_name
						}))}
						onChange={(value) =>
							confirmDiscardTemplate(() => {
								setSelectedPaperId(value);
								setSelectedTemplateId("");
								setLayout({ pages: [] });
							})
						}
					/>
          {canManage ? <Button className="template-source-delete" type="text" danger icon={<Trash2 size={15} />}
            aria-label="删除当前文件" title={sourceManagement?.deleteDisabledReason || "删除当前答题卡"}
            disabled={!sourceManagement?.canDelete} loading={sourceManagement?.deleting}
            onClick={() => sourceManagement?.removeCurrent()} /> : null}
				</div>
				{canManage ? (
          <Upload {...uploadProps}>
            <Button icon={<UploadIcon size={16} />} disabled={uploadProps.disabled} loading={sourceUpload.uploading}>上传答题卡</Button>
          </Upload>
				) : null}
				{!selectedTemplate ? (
					<Button
						type="primary"
						icon={<Plus size={16} />}
						disabled={
							!canEditSources ||
							sourceManagement?.deleting ||
							view.sourceRecognitionBlocked ||
							sourceUpload.uploading ||
							preview.loading ||
							Boolean(preview.error)
						}
						loading={saving}
						onClick={() => void createDraft()}
					>
						新建区域模板
					</Button>
				) : null}
				{selectedTemplate?.status === "locked" ? (
					<Button
						icon={<Copy size={16} />}
						disabled={!canEditSources || sourceUpload.uploading || sourceManagement?.deleting}
						loading={saving}
						onClick={() => void cloneTemplate()}
					>
						克隆新版本
					</Button>
				) : null}
  </>;
	return (
		<section className="template-toolbar template-compact-toolbar">
			<div className="template-toolbar-actions" role="group" aria-label="答题卡文件与操作">
        {sourceControls}
				{selectedTemplate?.status === "draft" ? (
					<>
						<Button
							type="primary"
							icon={<WandSparkles size={16} />}
							disabled={readonly || saving || suggestingRegions || sourceUpload.uploading || sourceManagement?.deleting || preview.loading || Boolean(preview.error)}
							loading={suggestingRegions}
							onClick={() => void suggestQuestionRegions()}
						>
							识别区域
						</Button>
						<Button
							icon={<Save size={16} />}
							disabled={readonly || suggestingRegions}
							loading={saving}
							onClick={() => void saveDraft()}
						>
							保存草稿
						</Button>
            <Button className="template-history-action" icon={<Undo2 size={16} />} aria-label="撤销" title="撤销"
              disabled={readonly || !view.editor.canUndo} onClick={() => view.editor.undo?.()} />
            <Button className="template-history-action" icon={<Redo2 size={16} />} aria-label="重做" title="重做"
              disabled={readonly || !view.editor.canRedo} onClick={() => view.editor.redo?.()} />
					</>
				) : null}
				{selectedTemplate ? <Popover trigger="click" placement="bottomRight" open={moreOpen} onOpenChange={setMoreOpen}
          content={<Space direction="vertical" className="template-more-actions">
            <Button type="text" icon={<Settings2 size={16} />} aria-label="模板设置"
              onClick={() => { setMoreOpen(false); view.setSettingsOpen(true); }}>模板设置</Button>
            {selectedTemplate.status === "draft" ? <ClearTemplateQuestionRegionsButton
              scopeKey={`${selectedTemplate.exam_id}:${selectedPaperId}:${selectedTemplate.id}:${selectedTemplate.revision}:${view.editor.revision}`}
              editable={canManage && canEditSources && !readonly}
              busy={saving || suggestingRegions || sourceUpload.uploading || Boolean(sourceManagement?.deleting) || preview.loading || view.binding.bindingBusy || view.printing.printBusy}
              layout={view.editor.layout} unassignedRegions={pendingQuestionAreas}
              onCleared={view.editor.resetFillGradingGroups}
              setLayout={setLayout} discardSuggestedRegion={id => {
                view.editor.discardSuggestedRegion(id); view.editor.discardObjectiveGroup?.(id);
              }} /> : null}
            {suggestionModel ? <span className="muted template-model-detail">已完成页使用：{suggestionModel}（多模态）</span> : null}
          </Space>}>
          <Button icon={<MoreHorizontal size={16} />} aria-label="更多操作" title="更多操作" />
        </Popover> : null}
			</div>
      {suggestingRegions || suggestionError ? <div className="template-recognition-status">
        {suggestingRegions ? <span role="status" aria-live="polite">{suggestionProgress?.total ? `正在识别：已处理 ${suggestionProgress.completed} / ${suggestionProgress.total} 页` : "正在准备答题卡图片…"}</span> : null}
        {suggestionError ? <Alert type={suggestionTone ?? "warning"} showIcon message={suggestionError} /> : null}
      </div> : null}
      <TemplateConfirmationAction view={view} />
		</section>
	);
}
