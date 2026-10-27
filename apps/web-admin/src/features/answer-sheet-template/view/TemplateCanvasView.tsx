// 画布状态和监听器仍在同一组件中按来源、模板和页码隔离，继续复用原缩放与平移 hooks。

import { useCallback, useEffect, useLayoutEffect, useRef, useState } from 'react';
import { Button, Popover, Select, Space, Spin } from 'antd';
import { ChevronLeft, ChevronRight, Hand, SquareDashed, MousePointer2, ZoomIn, ZoomOut } from 'lucide-react';
import { IdentityRegionPanel } from './IdentityRegionPanel';
import { FillGradingControl } from './FillGradingControl';
import { IDENTITY_REGION_LABELS, normalizeIdentityRegionLabel } from '../editor/identityRegionFields';
import { regionIssueMessage, regionRequiresReview } from '../editor/regionLifecycle';
import { MAX_TEMPLATE_ZOOM, MIN_TEMPLATE_ZOOM, normalizeTemplateZoom, useTemplateCanvasZoom } from '../editor/useTemplateCanvasZoom';
import { useTemplateCanvasPan } from '../editor/useTemplateCanvasPan';
import type { RegionResizeHandle } from '../editor/useRegionEditor';
import type { TemplateCanvasView } from './templateViewTypes';
import { clamp, isInputTarget, isDialogTarget } from './templateViewRules';

export const resizeHandles: Array<{ handle: RegionResizeHandle; label: string }> = [
  { handle: "nw", label: "左上角" }, { handle: "n", label: "上边" }, { handle: "ne", label: "右上角" },
  { handle: "e", label: "右边" }, { handle: "se", label: "右下角" }, { handle: "s", label: "下边" },
  { handle: "sw", label: "左下角" }, { handle: "w", label: "左边" }
];

export function CanvasPreview({ view }: Readonly<{ view: TemplateCanvasView }>) {
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

export function TemplateCanvas({ view }: Readonly<{ view: TemplateCanvasView }>) {
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
