import {
	Alert,
	Button,
	Input,
	InputNumber,
	List,
	Progress,
	Select,
	Space,
	Spin,
	Upload
} from "antd";
import {
	ChevronLeft,
	ChevronRight,
	Copy,
	LockKeyhole,
	MousePointer2,
	Plus,
	RefreshCw,
	Save,
	Trash2,
	Upload as UploadIcon,
	WandSparkles,
	ZoomIn,
	ZoomOut
} from "lucide-react";
import { EmptyState } from "../../../components/PageState";
import { StatusTag } from "../../../components/StatusTag";
import { CalibrationPanel, OMRProfilePanel } from "./OMRCalibrationView";
import { StudentPrintPanel } from "./StudentPrintingView";
import type {
	TemplatePageViewProps,
	TemplatePageViewState
} from "./templateViewTypes";

const coordLabels = { x: "左", y: "上", width: "宽", height: "高" } as const;
function clamp(value: number, min = 0, max = 1) {
	return Math.min(max, Math.max(min, value));
}

export function SourceUploadStatus({ view }: TemplatePageViewProps) {
	const { sourceUpload, uploadProps, confirmDiscardTemplate } = view;
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
		return <Progress percent={sourceUpload.progress} status="active" />;
	return null;
}

export function EmptyAnswerSheetSources({ view }: TemplatePageViewProps) {
	const { canEditSources, canManage, uploadProps, sourceUpload } = view;
	return (
		<div className="page-stack">
			<section className="page-heading">
				<div>
					<h2>答题卡设置</h2>
					<p>上传空白答题卡，再设置各题的答题区域。</p>
				</div>
			</section>
			{canEditSources ? (
				<Upload.Dragger {...uploadProps}>
					<UploadIcon size={32} />
					<h3>上传空白答题卡</h3>
					<p>选择文件或拖到这里，支持 PDF、PNG、JPG。</p>
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
							: "请联系考试负责人上传空白答题卡。"
					}
				/>
			)}
			<SourceUploadStatus view={view} />
		</div>
	);
}

export function TemplateToolbar({ view }: TemplatePageViewProps) {
	const {
		papers,
		templates,
		selectedPaperId,
		selectedTemplateId,
		selectedTemplate,
		setSelectedPaperId,
		setSelectedTemplateId,
		canManage,
		canEditSources,
		sourceUpload,
		uploadProps,
		confirmDiscardTemplate,
		loadData,
		questions
	} = view;
	const {
		saving,
		suggestingRegions,
		setLayout,
		readonly,
		createDraft,
		cloneTemplate,
		suggestQuestionRegions,
		saveDraft,
		confirmActivate
	} = view.editor;
	const { examBinding, bindingBusy, bindTemplateForExam } = view.binding;
	const { preview } = view.previewController;
	return (
		<section className="template-toolbar">
			<div>
				<h2>答题卡模板</h2>
				<p>选择题目后在空白答题卡上拖拽框选答题区域</p>
			</div>
			<Space wrap>
				<Select
					aria-label="答题卡源文件"
					disabled={saving || suggestingRegions || sourceUpload.uploading}
					value={selectedPaperId}
					options={papers.map((paper) => ({
						value: paper.id,
						label: `v${paper.version_no} ${paper.file.original_name}`
					}))}
					onChange={(value) =>
						confirmDiscardTemplate(() => {
							setSelectedPaperId(value);
							setSelectedTemplateId("");
							setLayout({ pages: [] });
						})
					}
				/>
				{canManage ? (
					<Upload {...uploadProps}>
						<Button
							icon={<UploadIcon size={16} />}
							disabled={uploadProps.disabled}
							loading={sourceUpload.uploading}
						>
							上传答题卡
						</Button>
					</Upload>
				) : null}
				{templates.length ? (
					<Select
						disabled={saving || suggestingRegions || sourceUpload.uploading}
						value={selectedTemplateId || undefined}
						placeholder="选择模板版本"
						options={templates.map((item) => ({
							value: item.id,
							label: `v${item.version_no} ${item.name} · ${item.status === "locked" ? "已锁定" : "草稿"}`
						}))}
						onChange={(value) =>
							confirmDiscardTemplate(() => setSelectedTemplateId(value))
						}
					/>
				) : null}
				<Button
					disabled={saving || suggestingRegions || sourceUpload.uploading}
					icon={<RefreshCw size={16} />}
					onClick={() => confirmDiscardTemplate(() => void loadData())}
				>
					刷新
				</Button>
				{!selectedTemplate ? (
					<Button
						type="primary"
						icon={<Plus size={16} />}
						disabled={
							!canEditSources ||
							sourceUpload.uploading ||
							preview.loading ||
							Boolean(preview.error)
						}
						loading={saving}
						onClick={() => void createDraft()}
					>
						新建模板
					</Button>
				) : null}
				{selectedTemplate?.status === "locked" ? (
					<Button
						icon={<Copy size={16} />}
						disabled={sourceUpload.uploading}
						loading={saving}
						onClick={() => void cloneTemplate()}
					>
						克隆新版本
					</Button>
				) : null}
				{canManage &&
				selectedTemplate?.status === "locked" &&
				(examBinding?.template_id !== selectedTemplate.id ||
					examBinding.mode === "bound_auto") ? (
					<Button
						disabled={sourceUpload.uploading}
						loading={bindingBusy}
						onClick={() => bindTemplateForExam(selectedTemplate)}
					>
						{examBinding?.template_id === selectedTemplate.id
							? "确认本场模板"
							: "用于本场考试"}
					</Button>
				) : null}
				{selectedTemplate?.status === "draft" ? (
					<>
						{preview.textLayerAvailable ? <Button
							icon={<WandSparkles size={16} />}
							disabled={readonly || preview.textLayerChecking}
							loading={suggestingRegions}
							onClick={() => void suggestQuestionRegions()}
						>
							按题号生成候选区域（待核对）
						</Button> : <span className="muted">{preview.textLayerChecking ? "正在检查文件文字层…" : "请选题后拖拽框选；图片及扫描件需手动设置区域"}</span>}
						<Button
							icon={<Save size={16} />}
							disabled={readonly || suggestingRegions}
							loading={saving}
							onClick={() => void saveDraft()}
						>
							保存草稿
						</Button>
						<Button
							type="primary"
							icon={<LockKeyhole size={16} />}
							disabled={
								readonly ||
								!questions.length ||
								saving ||
								suggestingRegions
							}
							onClick={confirmActivate}
						>
							确认区域并用于本场考试
						</Button>
					</>
				) : null}
			</Space>
		</section>
	);
}

export function TemplateQuestionPane({ view }: TemplatePageViewProps) {
	const { questions, questionPaneOpen, setQuestionPaneOpen } = view;
	const {
		selectedQuestionId,
		setSelectedQuestionId,
		coveredQuestions,
		readonly,
		createRegionWithoutDragging
	} = view.editor;
	return (
		<aside
			id="template-question-pane"
			className={
				questionPaneOpen
					? "template-question-pane is-open"
					: "template-question-pane"
			}
		>
			<div className="template-pane-head">
				<strong>题目</strong>
				<span>
					{coveredQuestions.size}/{questions.length}
				</span>
			</div>
			<Select
				aria-label="选择要配置的题目"
				value={selectedQuestionId || undefined}
				placeholder="选择题目"
				options={questions.map((question) => ({
					value: question.id,
					label: `第 ${question.question_no} 题 · ${question.score} 分`
				}))}
				onChange={(id) => {
					setSelectedQuestionId(id);
					setQuestionPaneOpen(false);
				}}
			/>
			<Button
				block
				disabled={readonly || !selectedQuestionId}
				onClick={createRegionWithoutDragging}
			>
				创建或定位答题区域
			</Button>
			<List
				dataSource={questions}
				locale={{ emptyText: "尚未配置题目" }}
				renderItem={(question) => (
					<List.Item
						className={
							selectedQuestionId === question.id
								? "template-question active"
								: "template-question"
						}
						onClick={() => {
							setSelectedQuestionId(question.id);
							setQuestionPaneOpen(false);
						}}
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
	const { questionPaneOpen, setQuestionPaneOpen, questions } = view;
	const { preview, pageNo, setPageNo } = view.previewController;
	const {
		coveredQuestions,
		layout,
		zoom,
		setZoom,
		canvasRef,
		readonly,
		currentPage,
		selectedRegionId,
		draftRect,
		onCanvasPointerDown,
		onCanvasPointerMove,
		onCanvasPointerUp,
		beginRegionInteraction
	} = view.editor;
	return (
		<main className="template-canvas-column">
			<div className="template-canvas-toolbar">
				<Button
					className="template-question-toggle"
					aria-expanded={questionPaneOpen}
					aria-controls="template-question-pane"
					onClick={() => setQuestionPaneOpen((open) => !open)}
				>
					题目 {coveredQuestions.size}/{questions.length}
				</Button>
				<Space>
					<Button
						icon={<ChevronLeft size={15} />}
						disabled={pageNo <= 1}
						onClick={() => setPageNo((value) => value - 1)}
						aria-label="上一页"
					/>
					<span>
						第 {pageNo} / {layout.pages.length} 页
					</span>
					<Button
						icon={<ChevronRight size={15} />}
						disabled={pageNo >= layout.pages.length}
						onClick={() => setPageNo((value) => value + 1)}
						aria-label="下一页"
					/>
				</Space>
				<Space>
					<Button
						icon={<ZoomOut size={15} />}
						disabled={zoom <= 0.7}
						onClick={() => setZoom((value) => Math.max(0.7, value - 0.1))}
						aria-label="缩小"
					/>
					<span>{Math.round(zoom * 100)}%</span>
					<Button
						icon={<ZoomIn size={15} />}
						disabled={zoom >= 1.5}
						onClick={() => setZoom((value) => Math.min(1.5, value + 0.1))}
						aria-label="放大"
					/>
				</Space>
			</div>
			<div className="template-canvas-scroll">
				<div
					ref={canvasRef}
					className={readonly ? "template-canvas readonly" : "template-canvas"}
					style={{
						width: `${zoom * 760}px`,
						aspectRatio: `${currentPage?.width || preview.width} / ${currentPage?.height || preview.height}`
					}}
					onPointerDown={onCanvasPointerDown}
					onPointerMove={onCanvasPointerMove}
					onPointerUp={onCanvasPointerUp}
				>
					<CanvasPreview view={view} />
					{currentPage?.question_regions.map((region) => (
						<div
							key={region.id}
							className={
								selectedRegionId === region.id
									? "template-region selected"
									: "template-region"
							}
							style={{
								left: `${region.x * 100}%`,
								top: `${region.y * 100}%`,
								width: `${region.width * 100}%`,
								height: `${region.height * 100}%`
							}}
							onPointerDown={(event) =>
								beginRegionInteraction(event, region, "move")
							}
						>
							<span>
								{region.label}
								{region.suggestion_confidence
									? ` · 建议 ${Math.round(region.suggestion_confidence * 100)}%`
									: ""}
							</span>
							{(region.option_regions ?? []).map((option) => (
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
							{!readonly ? (
								<button
									type="button"
									className="template-resize-handle"
									onPointerDown={(event) =>
										beginRegionInteraction(event, region, "resize")
									}
									aria-label="调整区域大小"
								/>
							) : null}
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
				<section className="template-option-editor">
					<strong>第 {selectedRegion.label} 题 · 区域位置与大小</strong>
					<p>优先在画布上框选。精确输入以整页为参照：左、上表示距页边的位置；宽、高表示占整页的百分比。</p>
					<div className="template-coordinate-fields">
						{(["x", "y", "width", "height"] as const).map((key) => (
							<label key={key}>
								<span>{coordLabels[key]}</span>
								<InputNumber
									aria-label={`答题区域${coordLabels[key]}`}
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
				</section>
			) : null}
		</>
	);
}

function bindingDescription(view: TemplatePageViewState) {
	const { examBinding } = view.binding;
	const { selectedTemplate } = view;
	if (!selectedTemplate || !examBinding) return "";
	if (examBinding.template_id !== selectedTemplate.id)
		return "本场考试已绑定其他模板版本，请在上方版本列表中切换查看。";
	if (examBinding.mode === "bound_auto")
		return `首张答卷自动识别为 v${selectedTemplate.version_no}，请核对后点击“确认本场模板”。`;
	return `当前版本 v${selectedTemplate.version_no} 已用于本场考试；后续页面会先做一致性检查。`;
}

export function TemplateBindingPanel({ view }: TemplatePageViewProps) {
	const { canManage, sourceUpload } = view;
	const { examBinding, bindingBusy, releaseExamBinding } = view.binding;
	return (
		<section className="template-option-editor">
			<div className="template-pane-head">
				<strong>本场考试模板</strong>
				{examBinding ? (
					<StatusTag
						tone={examBinding.mode === "bound_auto" ? "warning" : "success"}
					>
						{examBinding.mode === "bound_auto" ? "自动识别待确认" : "已绑定"}
					</StatusTag>
				) : (
					<StatusTag tone="neutral">未绑定</StatusTag>
				)}
			</div>
			{examBinding ? (
				<>
					<p>{bindingDescription(view)}</p>
					<span title={examBinding.template_content_hash}>
						模板校验码：{examBinding.template_content_hash.slice(0, 16)}
					</span>
					{canManage ? (
						<Button
							danger
							block
							disabled={sourceUpload.uploading}
							loading={bindingBusy}
							onClick={releaseExamBinding}
						>
							解除本场绑定
						</Button>
					) : null}
				</>
			) : (
				<p>
					锁定一个模板版本后，可将其用于本场考试。模板绑定与模板版本锁定相互独立。
				</p>
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
			{selectedRegion &&
			["single_choice", "multiple_choice", "true_false"].includes(
				selectedQuestion?.question_type ?? ""
			) ? (
				<section className="template-option-editor">
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
				</section>
			) : null}
		</>
	);
}

export function TemplateInspector({ view }: TemplatePageViewProps) {
	const { selectedTemplate, questions } = view;
	const {
		name,
		setName,
		readonly,
		selectedRegionId,
		removeRegion,
		layout,
		coveredQuestions
	} = view.editor;
	if (!selectedTemplate) return null;
	return (
		<aside className="template-inspector">
			<div className="template-pane-head">
				<strong>模板属性</strong>
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
			<RegionGeometry view={view} />
			<TemplateBindingPanel view={view} />
			<div className="template-help">
				<MousePointer2 size={18} />
				<p>
					选择左侧题目，在页面空白处拖拽创建区域。拖动区域可移动，右下角控制点可调整大小。
				</p>
			</div>
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
			<OptionRegionEditor view={view} />
			<OMRProfilePanel view={view} />
			<CalibrationPanel view={view} />
			<StudentPrintPanel view={view} />
			<div className="template-summary">
				<span>页面</span>
				<strong>{layout.pages.length}</strong>
				<span>题目区域</span>
				<strong>{coveredQuestions.size}</strong>
				<span>未配置</span>
				<strong>{Math.max(questions.length - coveredQuestions.size, 0)}</strong>
			</div>
		</aside>
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
							`系统已读取答题卡，共 ${preview.pageCount} 页。创建后即可按题框选区域。`
				}
			/>
		);
	return (
		<section className="template-workbench">
			<TemplateQuestionPane view={view} />
			<TemplateCanvas view={view} />
			<TemplateInspector view={view} />
		</section>
	);
}
