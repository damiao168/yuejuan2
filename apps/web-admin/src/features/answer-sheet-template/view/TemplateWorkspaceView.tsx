import { useCallback, useEffect, useLayoutEffect, useRef, useState } from "react";
import {
	Alert,
	Button,
	Checkbox,
	Drawer,
	Input,
	InputNumber,
	List,
	Progress,
	Popover,
	Select,
	Space,
	Spin,
	Upload
} from "antd";
import {
	CheckCircle2,
	ChevronLeft,
	ChevronRight,
	Copy,
	Hand,
	SquareDashed,
	MousePointer2,
	MoreHorizontal,
	Plus,
	Redo2,
	Save,
	Settings2,
	Trash2,
	Undo2,
	Upload as UploadIcon,
	WandSparkles,
	ZoomIn,
	ZoomOut
} from "lucide-react";
import { EmptyState } from "../../../components/PageState";
import { StatusTag } from "../../../components/StatusTag";
import { CalibrationPanel, OMRProfilePanel } from "./OMRCalibrationView";
import { StudentPrintPanel } from "./StudentPrintingView";
import { ClearTemplateQuestionRegionsButton } from "./ClearTemplateQuestionRegionsButton";
import { IdentityRegionPanel } from "./IdentityRegionPanel";
import { FillGradingControl } from "./FillGradingControl";
import { IDENTITY_REGION_LABELS, normalizeIdentityRegionLabel } from "../editor/identityRegionFields";
import { regionIssueMessage, regionRequiresReview } from "../editor/regionLifecycle";
import { ObjectiveGridDrawer } from "./ObjectiveGridDrawer";
import { MAX_TEMPLATE_ZOOM, MIN_TEMPLATE_ZOOM, normalizeTemplateZoom, useTemplateCanvasZoom } from "../editor/useTemplateCanvasZoom";
import { useTemplateCanvasPan } from "../editor/useTemplateCanvasPan";
import type { RegionResizeHandle } from "../editor/useRegionEditor";
import type {
	TemplatePageViewProps,
	TemplatePageViewState
} from "./templateViewTypes";

const coordLabels = { x: "左", y: "上", width: "宽", height: "高" } as const;
function clamp(value: number, min = 0, max = 1) {
	return Math.min(max, Math.max(min, value));
}

function isInputTarget(target: EventTarget | null) {
  return target instanceof Element && Boolean(target.closest("input, textarea, select, [contenteditable='true'], [role='textbox'], [role='combobox']"));
}
function isDialogTarget(target: EventTarget | null) {
  return target instanceof Element && Boolean(target.closest("[role='dialog'], [role='alertdialog']"));
}
const resizeHandles: Array<{ handle: RegionResizeHandle; label: string }> = [
  { handle: "nw", label: "左上角" }, { handle: "n", label: "上边" }, { handle: "ne", label: "右上角" },
  { handle: "e", label: "右边" }, { handle: "se", label: "右下角" }, { handle: "s", label: "下边" },
  { handle: "sw", label: "左下角" }, { handle: "w", label: "左边" }
];

export function SourceUploadStatus({ view }: TemplatePageViewProps) {
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
      {sourceUpload.phase === "ordering" ? <span role="status">多模态识别页序…</span> : null}
      <Progress percent={sourceUpload.progress} status="active" />
    </div>;
	return null;
}

function selectionUsageConfirmed(view: TemplatePageViewState) {
  const { selectedTemplate } = view;
  const { examBinding } = view.binding;
  return selectedTemplate?.status === "locked" && Boolean(examBinding?.exam_id === selectedTemplate.exam_id
    && examBinding.template_id === selectedTemplate.id && examBinding.mode === "locked_with_guard"
    && selectedTemplate.content_hash && examBinding.template_content_hash === selectedTemplate.content_hash);
}

function templateDisabledReason(view: TemplatePageViewState, missingCount: number) {
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

function templateConfirmationState(view: TemplatePageViewState) {
  const { selectedTemplate, questions } = view;
  const { coveredQuestions } = view.editor;
  const draft = selectedTemplate?.status === "draft";
  // 当前框选只指导确认按钮；锁定并绑定内容版本和区域准备检查是两个独立事实。
	const savedCoverage = new Set(selectedTemplate?.layout.pages.flatMap(page => page.question_regions.map(region => region.question_id)) ?? []);
	const missing = questions.filter(question => !(draft ? coveredQuestions : savedCoverage).has(question.id));
  const disabledReason = templateDisabledReason(view, missing.length);
  return { draft, usageConfirmed: selectionUsageConfirmed(view), disabled: Boolean(disabledReason), disabledReason };
}

export function EmptyAnswerSheetSources({ view }: TemplatePageViewProps) {
	const { canEditSources, canManage, uploadProps, sourceUpload } = view;
	return (
		<div className="page-stack">
			<section className="page-heading">
				<div>
					<h2>答题卡设置</h2>
					<p>上传后自动匹配模板、识别并对应题目，发现错误可直接修改。空白卡和已填写答题卡均可辅助识别，收录学校库需核对空白来源。</p>
				</div>
			</section>
			{canEditSources ? (
				<Upload.Dragger {...uploadProps}>
					<UploadIcon size={32} />
					<h3>上传答题卡</h3>
					<p>支持 PDF，或一次选择多张 PNG、JPG 图片；系统自动识别页序与答题区域。</p>
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
		</div>
	);
}

export function TemplateToolbar({ view }: TemplatePageViewProps) {
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
		</section>
	);
}

export function TemplateQuestionPane({ view }: TemplatePageViewProps) {
	const { questions } = view;
	const [onlyMissing, setOnlyMissing] = useState(false);
	const {
		selectedQuestionId,
		setSelectedQuestionId,
		coveredQuestions,
		readonly,
		createRegionWithoutDragging
	} = view.editor;
	const shownQuestions = onlyMissing ? questions.filter(question => !coveredQuestions.has(question.id)) : questions;
  const locateQuestion = (id: string) => {
    setSelectedQuestionId(id);
    const page = view.editor.layout.pages.find(item => item.question_regions.some(region => region.question_id === id));
    const region = page?.question_regions.find(item => item.question_id === id);
    if (page && region) {
      view.previewController.setPageNo(page.page_no); view.editor.setSelectedRegionId(region.id);
      // 再次点击同一道题也要重新定位；框本身的 pointerdown 不触发滚动，避免拖动起点跳走。
      requestAnimationFrame(() => [...document.querySelectorAll<HTMLElement>("[data-region-id]")].find(element => element.dataset.regionId === region.id)?.scrollIntoView?.({ block: "center", inline: "center" }));
    } else view.editor.setSelectedRegionId("");
  };
	return (
		<aside
			id="template-question-pane"
			className="template-question-pane"
		>
			<div className="template-pane-head">
				<strong>题目</strong>
				<span>
					{coveredQuestions.size}/{questions.length}
				</span>
			</div>
			<div className="template-question-filter">
				<Checkbox checked={onlyMissing} onChange={event => setOnlyMissing(event.target.checked)}>只看未配置</Checkbox>
				<span>未配置 {questions.filter(question => !coveredQuestions.has(question.id)).length} 题</span>
			</div>
			<Select
				aria-label="选择要配置的题目"
				value={selectedQuestionId || undefined}
				placeholder="选择题目"
				options={questions.map((question) => ({
					value: question.id,
					label: `第 ${question.question_no} 题 · ${question.score} 分`
				}))}
				onChange={locateQuestion}
			/>
			<Button
				block
				disabled={readonly || !selectedQuestionId}
				onClick={createRegionWithoutDragging}
			>
				创建或定位答题区域
			</Button>
			<List
				dataSource={shownQuestions}
				locale={{ emptyText: onlyMissing && questions.length ? "所有题目均已配置区域" : "尚未配置题目" }}
				renderItem={(question) => (
					<List.Item
						className={
							selectedQuestionId === question.id
								? "template-question active"
								: "template-question"
						}
						onClick={() => locateQuestion(question.id)}
					>
						<div>
							<strong>{question.question_no}</strong>
							<span>{question.score} 分</span>
						</div>
						<StatusTag
							tone={coveredQuestions.has(question.id) ? "success" : "warning"}
						>
							{coveredQuestions.has(question.id) ? "已框选" : "待框选"}
						</StatusTag>
					</List.Item>
				)}
			/>
		</aside>
	);
}

function CanvasPreview({ view }: TemplatePageViewProps) {
	const { preview, pageNo } = view.previewController;
	if (preview.loading)
		return (
			<div className="template-preview-state">
				<Spin />
				<span>正在渲染页面</span>
			</div>
		);
	if (preview.error)
		return <div className="template-preview-state error">{preview.error}</div>;
	if (preview.imageUrl)
		return (
			<img
				src={preview.imageUrl}
				alt={`答题卡第 ${pageNo} 页`}
				draggable={false}
			/>
		);
	return null;
}

export function TemplateCanvas({ view }: TemplatePageViewProps) {
	const { questions } = view;
	const { preview, pageNo, setPageNo } = view.previewController;
	const {
		coveredQuestions,
		layout,
		zoom,
		setZoom,
		canvasRef,
		readonly,
		currentPage,
		selectedQuestionId,
		selectedRegionId,
		draftRect,
		onCanvasPointerDown,
		onCanvasPointerMove,
		onCanvasPointerUp,
		beginRegionInteraction
	} = view.editor;
	// 来源页数独立于区域配置；旧模板尚未配置的页面仍须能够翻到。
	const pageCount = Math.max(1, preview.sourceId === view.selectedPaperId ? preview.pageCount : layout.pages.length);
	const candidates = view.editor.unassignedRegions?.get(pageNo) ?? [];
	const objectiveGroups = view.editor.objectiveGroups?.get(pageNo) ?? [];
	const candidateScope = `${view.selectedPaperId}:${view.selectedTemplate?.id}:${pageNo}`;
	const scrollRef = useTemplateCanvasZoom({ zoom, setZoom, canvasRef, scopeKey: candidateScope });
	const [canvasTool, setCanvasTool] = useState<"select" | "draw" | "pan">("select");
  const [spaceHeld, setSpaceHeld] = useState(false);
  const [annotationVisibility, setAnnotationVisibility] = useState("all");
	const panEnabled = canvasTool === "pan" || spaceHeld;
	const pan = useTemplateCanvasPan({ scrollRef, enabled: panEnabled, scopeKey: candidateScope });
	const [candidateSelection, setCandidateSelection] = useState<{ scope: string; id: string }>();
	const selectedCandidate = candidateSelection?.scope === candidateScope ? candidates.find(candidate => candidate.id === candidateSelection.id) : undefined;
	const selectedQuestion = questions.find(question => question.id === selectedQuestionId);
  const currentPageRef = useRef(currentPage);
  currentPageRef.current = currentPage;
  const centerRegion = useCallback((id: string) => {
    const scroll = scrollRef.current;
    const canvas = canvasRef.current;
    const page = currentPageRef.current;
    const region = [...(page?.question_regions ?? []), ...(page?.identity_regions ?? [])].find(item => item.id === id);
    if (!scroll || !canvas || !region) return;
    const bounds = canvas.getBoundingClientRect(), viewport = scroll.getBoundingClientRect();
    // 题区和身份框保存的都是页面比例；定位时才换成缩放像素，并限制在滚动边界内。
    scroll.scrollLeft = clamp(scroll.scrollLeft + bounds.left + (region.x + region.width / 2) * bounds.width - viewport.left - scroll.clientWidth / 2, 0, Math.max(0, scroll.scrollWidth - scroll.clientWidth));
    scroll.scrollTop = clamp(scroll.scrollTop + bounds.top + (region.y + region.height / 2) * bounds.height - viewport.top - scroll.clientHeight / 2, 0, Math.max(0, scroll.scrollHeight - scroll.clientHeight));
  }, [canvasRef, scrollRef]);
  const pendingFocusRef = useRef<{ scope: string; regionId: string; zoom: number } | undefined>(undefined);
  const pointerSelectingRef = useRef(false);
  useLayoutEffect(() => {
    if (selectedRegionId && !preview.loading && !pointerSelectingRef.current) centerRegion(selectedRegionId);
    pointerSelectingRef.current = false;
  }, [selectedRegionId, preview.loading, centerRegion]);
  useLayoutEffect(() => {
    const pending = pendingFocusRef.current;
    if (pending?.scope === candidateScope && pending.zoom === zoom) centerRegion(pending.regionId);
    pendingFocusRef.current = undefined;
  }, [zoom, candidateScope, centerRegion]);
  useEffect(() => {
    const activeHere = (target: EventTarget | null) => target instanceof Node && Boolean(scrollRef.current?.contains(target));
    const keyDown = (event: KeyboardEvent) => {
      if (!activeHere(event.target) || isInputTarget(event.target) || isDialogTarget(event.target)) return;
      if (event.code === "Space" && !event.ctrlKey && !event.metaKey && !event.altKey) {
        event.preventDefault();
        if (!event.repeat) { view.editor.cancelInteraction?.(); setSpaceHeld(true); }
      } else if (event.key === "Escape") {
        event.preventDefault(); setSpaceHeld(false); setCanvasTool("select"); setCandidateSelection(undefined);
        view.editor.cancelInteraction?.();
      }
    };
    const keyUp = (event: KeyboardEvent) => { if (event.code === "Space") setSpaceHeld(false); };
    const blur = () => setSpaceHeld(false);
    window.addEventListener("keydown", keyDown); window.addEventListener("keyup", keyUp); window.addEventListener("blur", blur);
    return () => { window.removeEventListener("keydown", keyDown); window.removeEventListener("keyup", keyUp); window.removeEventListener("blur", blur); };
  }, [view.editor.cancelInteraction, scrollRef]);
  const visibleRegions = annotationVisibility === "hidden" || annotationVisibility === "current" && view.editor.selectedRegionKind === "identity" ? [] : currentPage?.question_regions.filter(region => annotationVisibility !== "current" || region.question_id === selectedQuestionId) ?? [];
  const visibleIdentityRegions = annotationVisibility === "hidden" ? [] : currentPage?.identity_regions.filter(region => annotationVisibility !== "current" || region.id === selectedRegionId) ?? [];
  const displayedFillGroups = new Set<string>();
  const manualGroups = annotationVisibility === "hidden" ? [] : (currentPage?.grading_groups ?? [])
    .filter(group => annotationVisibility !== "current" || group.question_ids.includes(selectedQuestionId));
  const sharedQuestionIds = new Set(manualGroups.flatMap(group => group.question_ids));
  const visibleAnnotations = [
    ...visibleRegions.filter(region => !region.question_id || !sharedQuestionIds.has(region.question_id)).flatMap(region => {
      if (!region.grading_group_id || !region.grading_bbox) return [{ region, kind: "question" as const, shared: false }];
      if (displayedFillGroups.has(region.grading_group_id)) return [];
      displayedFillGroups.add(region.grading_group_id);
      const members = currentPage?.question_regions.filter(other => other.grading_group_id === region.grading_group_id) ?? [];
      const selected = members.find(member => member.id === selectedRegionId) ?? region;
      // 画布共框只改变批阅展示；选中仍指向原单题，不能把大框拖动写回某一道题的 ROI。
      return [{ region: { ...selected, ...region.grading_bbox, label: members.map(member => member.label).join("、") },
        kind: "question" as const, shared: true }];
    }),
    ...visibleIdentityRegions.map(region => ({ region, kind: "identity" as const, shared: false }))
  ];
	return (
		<main className="template-canvas-column">
			<div className="template-canvas-toolbar">
				<Space wrap>
					<Button
						icon={<ChevronLeft size={15} />}
						disabled={pageNo <= 1}
						onClick={() => setPageNo((value) => value - 1)}
						aria-label="上一页"
					/>
					{pageCount > 1 ? <Select
						aria-label="跳转答题卡页码"
						value={pageNo}
						options={Array.from({ length: pageCount }, (_, index) => ({ value: index + 1, label: `第 ${index + 1} 页` }))}
						onChange={value => setPageNo(Number(value))}
					/> : null}
					<span>第 {pageNo} / {pageCount} 页</span>
					<Button
						icon={<ChevronRight size={15} />}
						disabled={pageNo >= pageCount}
						onClick={() => setPageNo((value) => value + 1)}
						aria-label="下一页"
					/>
				</Space>
				<Space>
					<Button icon={<MousePointer2 size={15} />} title="选择和移动区域" aria-label="选择区域" aria-pressed={canvasTool === "select" && !panEnabled} type={canvasTool === "select" && !panEnabled ? "primary" : "default"} onClick={() => setCanvasTool("select")} />
					<Button icon={<Hand size={15} />} title="拖动画布（空格 + 左键）" aria-label="拖动画布" aria-pressed={panEnabled} type={panEnabled ? "primary" : "default"} onClick={() => setCanvasTool("pan")} />
					<Button icon={<SquareDashed size={15} />} title="框选答题区域" aria-label="框选区域" aria-pressed={canvasTool === "draw" && !panEnabled} disabled={readonly} type={canvasTool === "draw" && !panEnabled ? "primary" : "default"} onClick={() => setCanvasTool("draw")} />
					<Button
						icon={<ZoomOut size={15} />}
						disabled={zoom <= MIN_TEMPLATE_ZOOM}
						onClick={() => setZoom((value) => normalizeTemplateZoom(value - 0.1))}
						aria-label="缩小"
					/>
					<Button type="text" aria-label="恢复答题卡原始缩放" title="恢复 100%" onClick={() => setZoom(1)}>{Math.round(zoom * 100)}%</Button>
					<Button
						icon={<ZoomIn size={15} />}
						disabled={zoom >= MAX_TEMPLATE_ZOOM}
						onClick={() => setZoom((value) => normalizeTemplateZoom(value + 0.1))}
						aria-label="放大"
					/>
				</Space>
        <Select aria-label="显示题区标注" value={annotationVisibility} onChange={setAnnotationVisibility} options={[
          { value: "all", label: "全部标注" }, { value: "current", label: "仅当前区域" }, { value: "hidden", label: "隐藏标注" }
        ]} />
        <Popover trigger="click" placement="bottomRight" title="个人信息" destroyOnHidden
          content={<div className="template-identity-popover-content"><IdentityRegionPanel view={view} /></div>}>
          <Button aria-label="个人信息">个人信息</Button>
        </Popover>
        <Button aria-label="识别选择题" disabled={readonly || !preview.imageUrl || !view.editor.objectiveGrid}
          onClick={() => view.editor.objectiveGrid?.openGrid()}>识别选择题</Button>
        {currentPage && [...currentPage.registration_marks, ...currentPage.question_regions, ...currentPage.identity_regions].some(regionRequiresReview) ?
          <Button disabled={readonly} onClick={() => view.editor.confirmPageRegions?.()}>确认本页异常区域已处理</Button> : null}
			</div>
			<div className="template-canvas-zoom-hint">Ctrl + 滚轮缩放 · 空格 + 左键拖动 · 双击题区放大 · Esc 取消 · Ctrl + S 保存</div>
      {view.editor.fillGradingGroup ? <FillGradingControl key={`${candidateScope}:${selectedRegionId}:${selectedQuestionId}`} view={view} selectedCandidate={selectedCandidate} /> : null}
      {currentPage?.registration_marks.length ? <div className="template-canvas-zoom-hint">
        本页 {currentPage.registration_marks.length} 个定位标记，位置不正确可直接调整。
      </div> : null}
			{objectiveGroups.length ? <div className="template-candidate-actions template-objective-group-actions" role="group" aria-label="选择题区域核对">
				<span>此选择题区域尚未自动对应完整题号和选项，可重新识别或修改。</span>
				{objectiveGroups.map((group, index) => <Space key={group.id}>
					<Button type="primary" size="small" disabled={readonly || !view.editor.objectiveGrid}
						onClick={() => view.editor.objectiveGrid?.openGrid(group)}>
						{objectiveGroups.length > 1 ? `核对选择题区域 ${index + 1}` : "核对选择题"}
					</Button>
					<Button size="small" danger disabled={readonly}
						onClick={() => view.editor.discardObjectiveGroup(group.id)}>
						{objectiveGroups.length > 1 ? `丢弃区域 ${index + 1}` : "丢弃此区域"}
					</Button>
				</Space>)}
			</div> : null}
			{candidates.length ? <div className="template-candidate-actions" role="group" aria-label="未关联区域操作">
				<span>本页 {candidates.length} 个区域待核对，可先保存草稿。</span>
				{selectedCandidate ? <>
          <span>{regionIssueMessage(selectedCandidate)}</span>
					<Button type="primary" size="small" disabled={readonly || !selectedQuestion || coveredQuestions.has(selectedQuestion.id)}
						onClick={() => view.editor.assignSuggestedRegion(selectedCandidate.id, selectedQuestionId)}>
						{selectedQuestion ? `关联第 ${selectedQuestion.question_no} 题` : "请先选择左侧题目"}
					</Button>
          <Select<string> aria-label="将候选设为个人信息" placeholder="设为个人信息" value={undefined} disabled={readonly}
            options={IDENTITY_REGION_LABELS.map(label => ({ value: label, label }))}
            onChange={label => view.editor.assignSuggestedIdentity?.(selectedCandidate.id, label)} />
          <Button size="small" disabled={readonly || !view.editor.objectiveGrid}
            onClick={() => view.editor.objectiveGrid?.openGrid(selectedCandidate)}>按选择题核对</Button>
					<Button size="small" danger disabled={readonly} onClick={() => view.editor.discardSuggestedRegion(selectedCandidate.id)}>丢弃此区域</Button>
				</> : <span>点选虚线区域后关联题目</span>}
			</div> : null}
			<div ref={scrollRef} className={`template-canvas-scroll${panEnabled ? " is-pan" : ""}${pan.panning ? " is-panning" : ""}`} role="region" aria-label="答题卡缩放区域" tabIndex={0}
				onPointerDownCapture={event => {
          // 点击底图后让快捷键跟随画布焦点；其它页面区域的空格仍保留原行为。
          if (!isInputTarget(event.target)) event.currentTarget.focus({ preventScroll: true });
          pan.onPointerDownCapture(event);
        }} onPointerMoveCapture={pan.onPointerMoveCapture}
				onPointerUpCapture={pan.onPointerUpCapture} onPointerCancelCapture={pan.onPointerCancelCapture} onLostPointerCapture={pan.onLostPointerCapture}
				onClickCapture={event => { if (panEnabled) { event.preventDefault(); event.stopPropagation(); } }}>
				<div
					ref={canvasRef}
					className={`${readonly ? "template-canvas readonly" : "template-canvas"}${canvasTool === "select" ? " is-select" : ""}`}
					style={{
						width: `${zoom * 760}px`,
						aspectRatio: `${currentPage?.width || preview.width} / ${currentPage?.height || preview.height}`
					}}
					onPointerDown={event => { if (canvasTool === "draw" && !panEnabled) onCanvasPointerDown(event); else view.editor.setSelectedRegionId(""); }}
					onPointerMove={event => { if (!panEnabled) onCanvasPointerMove(event); }}
					onPointerUp={() => { if (!panEnabled) onCanvasPointerUp(); }}
          onPointerCancel={() => view.editor.cancelInteraction?.()}
				>
					<CanvasPreview view={view} />
					{annotationVisibility !== "hidden" ? objectiveGroups.map((group, index) => <button
						key={group.id}
						type="button"
						aria-label={`选择题区域 ${index + 1}，点击核对题号和选项`}
						className="template-region template-objective-group"
						disabled={readonly || !view.editor.objectiveGrid}
						style={{ left: `${group.x * 100}%`, top: `${group.y * 100}%`, width: `${group.width * 100}%`, height: `${group.height * 100}%` }}
						onPointerDown={event => event.stopPropagation()}
						onClick={event => { event.stopPropagation(); view.editor.objectiveGrid?.openGrid(group); }}
					><span>{objectiveGroups.length > 1 ? `选择题区域 ${index + 1}` : "选择题区域"}</span></button>) : null}
					{annotationVisibility === "all" ? candidates.map((candidate, index) => <div
						key={candidate.id}
						role="button"
						tabIndex={0}
						aria-label={`待核对区域 ${index + 1}`}
						title={`${candidate.label || `待核对区域 ${index + 1}`}：点击选择用途或关联题目`}
						aria-pressed={selectedCandidate?.id === candidate.id}
						className={`template-region template-unassigned-region${selectedCandidate?.id === candidate.id ? " selected" : ""}`}
						style={{ left: `${candidate.x * 100}%`, top: `${candidate.y * 100}%`, width: `${candidate.width * 100}%`, height: `${candidate.height * 100}%` }}
						onPointerDown={event => event.stopPropagation()}
						onClick={event => { event.stopPropagation(); setCandidateSelection({ scope: candidateScope, id: candidate.id }); }}
						onKeyDown={event => {
							if (event.key === "Enter" || event.key === " ") {
								event.preventDefault();
								setCandidateSelection({ scope: candidateScope, id: candidate.id });
							}
						}}
					><span>{candidate.label && candidate.label !== "待核对区域"
              ? candidate.label.replace(/^填空第 (\d{1,3}) 题 · 待核对$/, "$1题·待核对") : `待核对 ${index + 1}`}</span></div>) : null}
          {annotationVisibility === "all" ? currentPage?.registration_marks.map((mark, index) => <div key={`registration-${mark.id}`}
            className="template-region template-registration-mark" role="img" aria-label={`定位标记 ${index + 1}`}
            style={{ left: `${mark.x * 100}%`, top: `${mark.y * 100}%`, width: `${mark.width * 100}%`, height: `${mark.height * 100}%`, pointerEvents: "none" }}>
            <span>定位标记 {index + 1}</span>
          </div>) : null}
          {manualGroups.map(group => {
            const labels = group.question_ids.flatMap(id => questions.find(question => question.id === id)?.question_no ?? []);
            return <div key={`fill-area-${group.group_id}`} data-grading-group={group.group_id} data-group-only="true"
              className={`template-region${group.question_ids.includes(selectedQuestionId) ? " selected" : ""}`}
              role="button" tabIndex={0} aria-label={`填空第 ${labels.join("、")} 题整区`}
              style={{ left: `${group.bbox.x * 100}%`, top: `${group.bbox.y * 100}%`, width: `${group.bbox.width * 100}%`, height: `${group.bbox.height * 100}%` }}
              onPointerDown={event => { event.stopPropagation(); view.editor.setSelectedRegionId(""); view.editor.setSelectedQuestionId(group.question_ids[0]); }}
              onKeyDown={event => { if (event.key === "Enter") { view.editor.setSelectedRegionId(""); view.editor.setSelectedQuestionId(group.question_ids[0]); } }}>
              <span>{labels.join("、")}</span>
            </div>;
          })}
					{visibleAnnotations.map(({ region, kind, shared }) => (
						<div
							key={region.id}
              data-region-id={region.id}
              data-region-kind={kind}
              data-grading-group={shared ? region.grading_group_id : undefined}
              role="button" tabIndex={0} aria-label={kind === "identity" ? `${normalizeIdentityRegionLabel(region.label)}区域` : `第 ${region.label || region.question_id} 题区域`}
              title={kind === "question" && region.option_regions?.length ? `第 ${region.label || region.question_id} 题区域` : undefined}
							className={
								(selectedRegionId === region.id
									? "template-region selected"
									: "template-region") + (kind === "identity" ? " template-identity-region" : "")
							}
							style={{
								left: `${region.x * 100}%`,
								top: `${region.y * 100}%`,
								width: `${region.width * 100}%`,
								height: `${region.height * 100}%`
							}}
							onPointerDown={event => {
                pointerSelectingRef.current = true;
                if (readonly || shared) { event.stopPropagation(); view.editor.setSelectedRegionId(region.id); if (region.question_id) view.editor.setSelectedQuestionId(region.question_id); }
                else beginRegionInteraction(event, region, "move");
              }}
              onDoubleClick={event => {
                event.stopPropagation(); view.editor.setSelectedRegionId(region.id);
                const scroll = scrollRef.current;
                if (!scroll) return;
                const pageHeight = 760 * (currentPage?.height || preview.height) / (currentPage?.width || preview.width);
                const next = normalizeTemplateZoom(Math.min((scroll.clientWidth - 64) / (760 * region.width), (scroll.clientHeight - 64) / (pageHeight * region.height)));
                if (next === zoom) centerRegion(region.id);
                else { pendingFocusRef.current = { scope: candidateScope, regionId: region.id, zoom: next }; setZoom(next); }
              }}
              onKeyDown={event => { if (event.key === "Enter") { view.editor.setSelectedRegionId(region.id); centerRegion(region.id); } }}
						>
							{/* 选择题沿用原件题号；小框可能从题号或首个选项开始，重复蓝标签会遮住印刷内容。 */}
							{kind !== "question" || !region.option_regions?.length ? <span>
								{kind === "identity" ? normalizeIdentityRegionLabel(region.label) : region.label}
							</span> : null}
							{(kind === "question" ? region.option_regions ?? [] : []).map((option) => (
								<i
									key={option.id}
									className="template-option-region"
									title={`选项 ${option.label}`}
									style={{
										left: `${option.x * 100}%`,
										top: `${option.y * 100}%`,
										width: `${option.width * 100}%`,
										height: `${option.height * 100}%`
									}}
								>
									<span>{option.label}</span>
								</i>
							))}
							{!readonly && !shared && selectedRegionId === region.id ? resizeHandles.map(({ handle, label }) => (
								<button key={handle}
									type="button"
									className={`template-resize-handle handle-${handle}`}
									onPointerDown={(event) =>
										beginRegionInteraction(event, region, "resize", handle)
									}
									aria-label={kind === "identity" ? `调整${normalizeIdentityRegionLabel(region.label)}区域${label}` : `调整区域${label}`}
								/>
							)) : null}
						</div>
					))}
					{draftRect ? (
						<div
							className="template-region drawing"
							style={{
								left: `${draftRect.x * 100}%`,
								top: `${draftRect.y * 100}%`,
								width: `${draftRect.width * 100}%`,
								height: `${draftRect.height * 100}%`
							}}
						/>
					) : null}
				</div>
			</div>
		</main>
	);
}

export function RegionGeometry({ view }: TemplatePageViewProps) {
	const { selectedRegion, readonly, updateRegion } = view.editor;
	return (
		<>
			{selectedRegion ? (
				<details className="template-option-editor template-advanced-geometry">
					<summary>高级设置 · 精确坐标</summary>
					<p>优先在画布上框选。精确输入以整页为参照：左、上表示距页边的位置；宽、高表示占整页的百分比。</p>
					<div className="template-coordinate-fields">
						{(["x", "y", "width", "height"] as const).map((key) => (
							<label key={key}>
								<span>{coordLabels[key]}</span>
								<InputNumber
									aria-label={`${view.editor.selectedRegionKind === "identity" ? "个人信息区域" : "答题区域"}${coordLabels[key]}`}
									min={key === "x" || key === "y" ? 0 : 1}
									max={100}
									step={1}
									precision={1}
									suffix="%"
									disabled={readonly}
									value={selectedRegion[key] * 100}
									onChange={(value) => {
										const next = {
											...selectedRegion,
											[key]: Number(value ?? 0) / 100
										};
										next.x = clamp(next.x, 0, 0.99);
										next.y = clamp(next.y, 0, 0.99);
										next.width = clamp(next.width, 0.01, 1 - next.x);
										next.height = clamp(next.height, 0.01, 1 - next.y);
										updateRegion(selectedRegion.id, next);
									}}
								/>
							</label>
						))}
					</div>
				</details>
			) : null}
		</>
	);
}

function bindingDescription(view: TemplatePageViewState) {
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

export function TemplateBindingPanel({ view }: TemplatePageViewProps) {
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

export function OptionRegionEditor({ view }: TemplatePageViewProps) {
	const {
		selectedRegion,
		selectedQuestion,
		readonly,
		addOptionRegion,
		updateOption,
		removeOptionRegion
	} = view.editor;
	return (
		<>
			{selectedRegion && view.editor.selectedRegionKind !== "identity" &&
			["single_choice", "multiple_choice", "true_false"].includes(
				selectedQuestion?.question_type ?? ""
			) ? (
				<details className="template-option-editor template-advanced-options">
					<summary>高级设置 · 单个选项</summary>
					<div className="template-pane-head">
						<strong>选项标记区域</strong>
						<Button
							size="small"
							icon={<Plus size={14} />}
							disabled={readonly}
							onClick={addOptionRegion}
						>
							添加
						</Button>
					</div>
					<p>选项框以本题区域为参照，数值为占本题区域的百分比；每个框只覆盖一个填涂点。</p>
					{(selectedRegion.option_regions ?? []).map((option) => (
						<div className="template-option-row" key={option.id}>
							<Input
								aria-label="选项标签"
								value={option.label}
								disabled={readonly}
								maxLength={16}
								onChange={(event) =>
									updateOption(option.id, {
										label: event.target.value.toUpperCase()
									})
								}
							/>
							{(["x", "y", "width", "height"] as const).map((key) => (
								<label key={key}>
									<span>{coordLabels[key]}</span>
									<InputNumber
										aria-label={`选项 ${option.label} ${coordLabels[key]}`}
										min={0}
										max={100}
										step={1}
										precision={1}
										suffix="%"
										disabled={readonly}
										value={option[key] * 100}
										onChange={(value) =>
											updateOption(option.id, { [key]: Number(value ?? 0) / 100 })
										}
									/>
								</label>
							))}
							<Button
								danger
								type="text"
								icon={<Trash2 size={14} />}
								aria-label={`删除选项 ${option.label}`}
								disabled={readonly}
								onClick={() => removeOptionRegion(option.id)}
							/>
						</div>
					))}
					{(selectedRegion.option_regions ?? []).length === 0 ? (
						<Alert
							type="warning"
							showIcon
							message="尚未配置选项框"
							description="此题无法自动识别填涂，将转人工处理。"
						/>
					) : null}
				</details>
			) : null}
		</>
	);
}

export function TemplateSettingsDrawer({ view }: TemplatePageViewProps) {
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
