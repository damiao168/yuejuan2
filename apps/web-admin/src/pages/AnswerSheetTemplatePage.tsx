import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Alert, App, Button, Checkbox, Collapse, Drawer, Input, InputNumber, List, Modal, Progress, Select, Space, Spin } from "antd";
import { ChevronLeft, ChevronRight, Copy, Download, LockKeyhole, MousePointer2, Plus, Printer, RefreshCw, Save, Trash2, WandSparkles, ZoomIn, ZoomOut } from "lucide-react";
import { ApiClientError, getUserErrorMessage } from "../api/client";
import {
	getExamTemplateBinding,
  listAnswerSheetTemplates,
  type AnswerSheetTemplate,
	type OMRCalibrationCase,
	type OMRCalibrationStatus
} from "../api/configuration";
import { listPapers, listQuestions, type PaperVersion, type Question } from "../api/papers";
import { EmptyState, ErrorState, LoadingState } from "../components/PageState";
import { StatusTag } from "../components/StatusTag";
import { useOMRCalibration } from "../features/answer-sheet-template/calibration/useOMRCalibration";
import { useTemplateBinding } from "../features/answer-sheet-template/binding/useTemplateBinding";
import { useStudentPrinting } from "../features/answer-sheet-template/printing/useStudentPrinting";
import { useTemplatePreview } from "../features/answer-sheet-template/editor/useTemplatePreview";
import { useTemplateEditor } from "../features/answer-sheet-template/editor/useTemplateEditor";

function formatError(error: unknown) {
  if (error instanceof ApiClientError) {
    console.error("请求失败", error.status, error.code, error.message);
    return getUserErrorMessage(error, "操作失败，请稍后重试");
  }
  return getUserErrorMessage(error, "操作失败，请稍后重试");
}

function printBatchTime(value: string) {
	return new Intl.DateTimeFormat("zh-CN", {
		month: "2-digit",
		day: "2-digit",
		hour: "2-digit",
		minute: "2-digit",
		hour12: false
	}).format(new Date(value));
}

function calibrationStatusLabel(status: OMRCalibrationStatus) {
	return { draft: "待标注", approved: "已批准", revoked: "已撤销", discarded: "已弃用" }[status];
}

function calibrationStatusTone(status: OMRCalibrationStatus): "warning" | "success" | "danger" | "neutral" {
	switch (status) {
		case "approved": return "success";
		case "revoked": return "danger";
		case "discarded": return "neutral";
		default: return "warning";
	}
}

function calibrationBlockerLabel(code: string) {
	return {
		sample_count_below_minimum: "合格历史样本不足 100 份",
		calibration_labels_pending: "仍有样本尚未人工标注",
		calibration_mismatch_detected: "人工标注与识别结果出现不一致",
		eligible_calibration_mismatch_detected: "高置信候选存在识别错误，不能开放自动确认",
		option_coverage_incomplete: "各选项的人工样本覆盖不足 10 份",
		question_coverage_incomplete: "模板内仍有选择题未被样本覆盖",
		stratum_coverage_incomplete: "高置信、低置信、空白或模糊样本覆盖不足",
		calibration_not_draft: "该校准已不处于可审批状态"
	}[code] ?? "存在未满足的校准条件";
}

function calibrationStratumLabel(value: OMRCalibrationCase["sample_stratum"]) {
	return ({
		selected_high: "高置信候选",
		selected_low: "低置信候选",
		blank: "机器判空白",
		ambiguous: "模糊或多选",
		"": "待标注"
	}[value] ?? "其他");
}

const coordLabels = { x: "左", y: "上", width: "宽", height: "高" } as const;

function clamp(value: number, min = 0, max = 1) {
  return Math.min(max, Math.max(min, value));
}

export function AnswerSheetTemplatePage({ examId, canManage, canCalibrate = false, onExamChanged }: { examId: string; canManage: boolean; canCalibrate?: boolean; onExamChanged?: () => void }) {
  const { message } = App.useApp();
  const dataRequestRef = useRef(0);
  const [papers, setPapers] = useState<PaperVersion[]>([]);
  const [questions, setQuestions] = useState<Question[]>([]);
  const [templates, setTemplates] = useState<AnswerSheetTemplate[]>([]);
  const [selectedPaperId, setSelectedPaperId] = useState("");
  const [selectedTemplateId, setSelectedTemplateId] = useState("");
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string>();

  const selectedPaper = useMemo(() => papers.find((item) => item.id === selectedPaperId), [papers, selectedPaperId]);
  const selectedTemplate = useMemo(() => templates.find((item) => item.id === selectedTemplateId), [templates, selectedTemplateId]);
  const binding = useTemplateBinding(examId);
  const { examBinding, setExamBinding, bindingBusy, bindTemplateForExam, releaseExamBinding } = binding;
  const printing = useStudentPrinting(selectedTemplate, canManage);
  const {
    printContext, printContextLoading, printContextError, printModalOpen,
    setPrintModalOpen, selectedPrintClassIds, setSelectedPrintClassIds,
    printBusy, printableCandidates, printClasses, loadPrintContext, openPrintModal,
    downloadPrintBatch, issuePrintPackage
  } = printing;
  const previewController = useTemplatePreview(selectedPaper);
  const { preview, pdfRef, pageNo, setPageNo } = previewController;
  const isTemplateDifference = selectedTemplate?.status === "locked" && selectedTemplate.layout.omr_profile?.mode === "template_difference";
  // 校准针对已锁定的差分识别模板；查看/编辑模板权限本身不代表可审批自动确认。
  const calibration = useOMRCalibration({
    template: selectedTemplate,
    enabled: Boolean(selectedTemplate?.status === "locked" && selectedTemplate.layout.omr_profile?.mode === "template_difference" && canCalibrate),
    message,
    formatError
  });
  const {
    calibrations, loading: calibrationLoading, error: calibrationError,
    detail: calibrationDetail, drawerOpen: calibrationDrawerOpen, setDrawerOpen: setCalibrationDrawerOpen,
    selectedCase: selectedCalibrationCase, setCaseId: setCalibrationCaseId,
    expectedOptions: calibrationExpectedOptions, setExpectedOptions: setCalibrationExpectedOptions,
    imageUrl: calibrationImageUrl, imageLoading: calibrationImageLoading, busy: calibrationBusy,
    action: calibrationAction, setAction: setCalibrationAction,
    actionReason: calibrationActionReason, setActionReason: setCalibrationActionReason,
    reload: loadOMRCalibrationList, open: openCalibration, start: startCalibration,
    labelCase: labelCalibrationCase, submitAction: submitCalibrationAction
  } = calibration;

  const loadData = useCallback(async () => {
    const requestId = ++dataRequestRef.current;
    setLoading(true);
    setError(undefined);
    try {
      const [paperResponse, questionResponse, templateResponse, bindingResponse] = await Promise.all([listPapers(examId), listQuestions(examId), listAnswerSheetTemplates(examId), getExamTemplateBinding(examId)]);
      if (requestId !== dataRequestRef.current) return;
      setPapers(paperResponse.papers);
      setQuestions(questionResponse.questions);
      setTemplates(templateResponse.templates);
		setExamBinding(bindingResponse.binding);
      const latest = templateResponse.templates[0];
      setSelectedPaperId((current) =>
        paperResponse.papers.some((paper) => paper.id === current)
          ? current
          : latest?.exam_paper_id || paperResponse.papers[0]?.id || ""
      );
      setSelectedTemplateId((current) =>
        templateResponse.templates.some((template) => template.id === current)
          ? current
          : bindingResponse.binding?.template_id || latest?.id || ""
      );
    } catch (loadError) {
      if (requestId !== dataRequestRef.current) return;
      setError(formatError(loadError));
    } finally {
      if (requestId === dataRequestRef.current) setLoading(false);
    }
  }, [examId]);

  useEffect(() => { void loadData(); }, [loadData]);
  useEffect(() => {
    if (selectedTemplate) setSelectedPaperId(selectedTemplate.exam_paper_id);
  }, [selectedTemplate]);

  const editor = useTemplateEditor({
    examId, canManage, selectedPaper, selectedTemplate, questions, setTemplates,
    setSelectedTemplateId, loadData, onChanged: onExamChanged, pdfRef, preview,
    pageNo, setPageNo
  });
  const {
    canvasRef, selectedQuestionId, setSelectedQuestionId, name, setName, layout, setLayout,
    zoom, setZoom, selectedRegionId, saving,
    suggestingRegions, currentPage, selectedRegion, selectedQuestion, readonly,
    coveredQuestions, draftRect, onCanvasPointerDown, beginRegionInteraction,
    onCanvasPointerMove, onCanvasPointerUp, updateRegion, createRegionWithoutDragging,
    removeRegion, updateOption, addOptionRegion, removeOptionRegion,
    suggestQuestionRegions, setOMRProfile, createDraft, saveDraft, confirmLock,
    cloneTemplate
  } = editor;


  if (loading) return <LoadingState label="正在加载答题卡模板" />;
  if (error) return <ErrorState message={error} onRetry={() => void loadData()} />;
  if (!papers.length) return <EmptyState title="尚未上传试卷" description="先在“试卷”步骤上传 PDF 或图片，再建立答题卡模板。" />;

	const selectedCalibrationIndex = calibrationDetail && selectedCalibrationCase ? calibrationDetail.cases.findIndex((item) => item.id === selectedCalibrationCase.id) : -1;
	const calibrationProgress = calibrationDetail ? Math.round((calibrationDetail.session.summary.labeled_count / Math.max(calibrationDetail.session.summary.total_count, 1)) * 100) : 0;

  return (
    <div className="template-editor-page">
      <section className="template-toolbar">
        <div><h2>答题卡模板</h2><p>选择题目后在试卷上拖拽框选答题区域</p></div>
        <Space wrap>
          <Select value={selectedPaperId} options={papers.map((paper) => ({ value: paper.id, label: `v${paper.version_no} ${paper.file.original_name}` }))} onChange={(value) => { setSelectedPaperId(value); setSelectedTemplateId(""); setLayout({ pages: [] }); }} />
          {templates.length ? <Select value={selectedTemplateId || undefined} placeholder="选择模板版本" options={templates.map((item) => ({ value: item.id, label: `v${item.version_no} ${item.name} · ${item.status === "locked" ? "已锁定" : "草稿"}` }))} onChange={setSelectedTemplateId} /> : null}
          <Button icon={<RefreshCw size={16} />} onClick={() => void loadData()}>刷新</Button>
          {!selectedTemplate ? <Button type="primary" icon={<Plus size={16} />} loading={saving} onClick={() => void createDraft()}>新建模板</Button> : null}
          {selectedTemplate?.status === "locked" ? <Button icon={<Copy size={16} />} loading={saving} onClick={() => void cloneTemplate()}>克隆新版本</Button> : null}
			{canManage && selectedTemplate?.status === "locked" && (examBinding?.template_id !== selectedTemplate.id || examBinding.mode === "bound_auto") ? <Button loading={bindingBusy} onClick={() => bindTemplateForExam(selectedTemplate)}>{examBinding?.template_id === selectedTemplate.id ? "确认本场模板" : "用于本场考试"}</Button> : null}
          {selectedTemplate?.status === "draft" ? <><Button icon={<WandSparkles size={16} />} loading={suggestingRegions} onClick={() => void suggestQuestionRegions()}>自动识别区域</Button><Button icon={<Save size={16} />} loading={saving} onClick={() => void saveDraft()}>保存</Button><Button type="primary" icon={<LockKeyhole size={16} />} disabled={!questions.length} onClick={confirmLock}>锁定模板</Button></> : null}
        </Space>
      </section>

      {!selectedTemplate ? <Alert type="info" showIcon message="先创建模板草稿" description={`系统已读取试卷，共 ${preview.pageCount} 页。创建后即可按题框选区域。`} /> : (
        <section className="template-workbench">
          <aside className="template-question-pane">
            <div className="template-pane-head"><strong>题目</strong><span>{coveredQuestions.size}/{questions.length}</span></div>
            <Select aria-label="选择要配置的题目" value={selectedQuestionId || undefined} placeholder="选择题目" options={questions.map((question) => ({ value: question.id, label: `第 ${question.question_no} 题 · ${question.score} 分` }))} onChange={setSelectedQuestionId} />
            <Button block disabled={readonly || !selectedQuestionId} onClick={createRegionWithoutDragging}>创建或定位答题区域</Button>
            <List dataSource={questions} locale={{ emptyText: "尚未配置题目" }} renderItem={(question) => <List.Item className={selectedQuestionId === question.id ? "template-question active" : "template-question"} onClick={() => setSelectedQuestionId(question.id)}><div><strong>{question.question_no}</strong><span>{question.score} 分</span></div><StatusTag tone={coveredQuestions.has(question.id) ? "success" : "warning"}>{coveredQuestions.has(question.id) ? "已框选" : "待框选"}</StatusTag></List.Item>} />
          </aside>

          <main className="template-canvas-column">
            <div className="template-canvas-toolbar">
              <Space><Button icon={<ChevronLeft size={15} />} disabled={pageNo <= 1} onClick={() => setPageNo((value) => value - 1)} aria-label="上一页" /><span>第 {pageNo} / {layout.pages.length} 页</span><Button icon={<ChevronRight size={15} />} disabled={pageNo >= layout.pages.length} onClick={() => setPageNo((value) => value + 1)} aria-label="下一页" /></Space>
              <Space><Button icon={<ZoomOut size={15} />} disabled={zoom <= 0.7} onClick={() => setZoom((value) => Math.max(0.7, value - 0.1))} aria-label="缩小" /><span>{Math.round(zoom * 100)}%</span><Button icon={<ZoomIn size={15} />} disabled={zoom >= 1.5} onClick={() => setZoom((value) => Math.min(1.5, value + 0.1))} aria-label="放大" /></Space>
            </div>
            <div className="template-canvas-scroll">
              <div ref={canvasRef} className={readonly ? "template-canvas readonly" : "template-canvas"} style={{ width: `${zoom * 760}px`, aspectRatio: `${currentPage?.width || preview.width} / ${currentPage?.height || preview.height}` }} onPointerDown={onCanvasPointerDown} onPointerMove={onCanvasPointerMove} onPointerUp={onCanvasPointerUp}>
                {preview.loading ? <div className="template-preview-state"><Spin /><span>正在渲染页面</span></div> : preview.error ? <div className="template-preview-state error">{preview.error}</div> : preview.imageUrl ? <img src={preview.imageUrl} alt={`试卷第 ${pageNo} 页`} draggable={false} /> : null}
                {currentPage?.question_regions.map((region) => <div key={region.id} className={selectedRegionId === region.id ? "template-region selected" : "template-region"} style={{ left: `${region.x * 100}%`, top: `${region.y * 100}%`, width: `${region.width * 100}%`, height: `${region.height * 100}%` }} onPointerDown={(event) => beginRegionInteraction(event, region, "move")}><span>{region.label}{region.suggestion_confidence ? ` · 建议 ${Math.round(region.suggestion_confidence * 100)}%` : ""}</span>{(region.option_regions ?? []).map((option) => <i key={option.id} className="template-option-region" title={`选项 ${option.label}`} style={{ left: `${option.x * 100}%`, top: `${option.y * 100}%`, width: `${option.width * 100}%`, height: `${option.height * 100}%` }}>{option.label}</i>)}{!readonly ? <button type="button" className="template-resize-handle" onPointerDown={(event) => beginRegionInteraction(event, region, "resize")} aria-label="调整区域大小" /> : null}</div>)}
                {draftRect ? <div className="template-region drawing" style={{ left: `${draftRect.x * 100}%`, top: `${draftRect.y * 100}%`, width: `${draftRect.width * 100}%`, height: `${draftRect.height * 100}%` }} /> : null}
              </div>
            </div>
          </main>

          <aside className="template-inspector">
            <div className="template-pane-head"><strong>模板属性</strong>{selectedTemplate.status === "locked" ? <StatusTag tone="success">已锁定</StatusTag> : <StatusTag tone="processing">草稿</StatusTag>}</div>
            <label><span>模板名称</span><Input value={name} disabled={readonly} onChange={(event) => setName(event.target.value)} /></label>
            {selectedRegion ? <section className="template-option-editor"><strong>第 {selectedRegion.label} 题 · 精确区域</strong><p>无需拖动，可按页面比例调整位置与大小。</p><div className="template-coordinate-fields">{(["x", "y", "width", "height"] as const).map((key) => <label key={key}><span>{coordLabels[key]}</span><InputNumber aria-label={`答题区域${coordLabels[key]}`} min={key === "x" || key === "y" ? 0 : .01} max={1} step={.01} precision={3} disabled={readonly} value={selectedRegion[key]} onChange={(value) => { const next = { ...selectedRegion, [key]: Number(value ?? 0) }; next.x = clamp(next.x, 0, .99); next.y = clamp(next.y, 0, .99); next.width = clamp(next.width, .01, 1 - next.x); next.height = clamp(next.height, .01, 1 - next.y); updateRegion(selectedRegion.id, next); }} /></label>)}</div></section> : null}
			<section className="template-option-editor">
				<div className="template-pane-head"><strong>本场考试模板</strong>{examBinding ? <StatusTag tone={examBinding.mode === "bound_auto" ? "warning" : "success"}>{examBinding.mode === "bound_auto" ? "自动识别待确认" : "已绑定"}</StatusTag> : <StatusTag tone="neutral">未绑定</StatusTag>}</div>
				{examBinding ? <>
					<p>{examBinding.template_id === selectedTemplate.id ? examBinding.mode === "bound_auto" ? `首张答卷自动识别为 v${selectedTemplate.version_no}，请核对后点击“确认本场模板”。` : `当前版本 v${selectedTemplate.version_no} 已用于本场考试；后续页面会先做一致性检查。` : `本场考试已绑定其他模板版本，请在上方版本列表中切换查看。`}</p>
					<span title={examBinding.template_content_hash}>模板校验码：{examBinding.template_content_hash.slice(0, 16)}</span>
					{canManage ? <Button danger block loading={bindingBusy} onClick={releaseExamBinding}>解除本场绑定</Button> : null}
				</> : <p>锁定一个模板版本后，可将其用于本场考试。模板绑定与模板版本锁定相互独立。</p>}
			</section>
            <div className="template-help"><MousePointer2 size={18} /><p>选择左侧题目，在页面空白处拖拽创建区域。拖动区域可移动，右下角控制点可调整大小。</p></div>
            {selectedRegionId ? <Button danger icon={<Trash2 size={16} />} disabled={readonly} onClick={() => removeRegion(selectedRegionId)}>删除所选区域</Button> : null}
            {selectedRegion && ["single_choice", "multiple_choice", "true_false"].includes(selectedQuestion?.question_type ?? "") ? <section className="template-option-editor">
              <div className="template-pane-head"><strong>选项标记区域</strong><Button size="small" icon={<Plus size={14} />} disabled={readonly} onClick={addOptionRegion}>添加</Button></div>
              <p>选项框位置以本题区域为基准，每个框只覆盖一个填涂点。</p>
              {(selectedRegion.option_regions ?? []).map((option) => <div className="template-option-row" key={option.id}>
                <Input aria-label="选项标签" value={option.label} disabled={readonly} maxLength={16} onChange={(event) => updateOption(option.id, { label: event.target.value.toUpperCase() })} />
                {(["x", "y", "width", "height"] as const).map((key) => <label key={key}><span>{coordLabels[key]}</span><InputNumber aria-label={`选项 ${option.label} ${coordLabels[key]}`} min={0} max={1} step={0.01} precision={3} disabled={readonly} value={option[key]} onChange={(value) => updateOption(option.id, { [key]: Number(value ?? 0) })} /></label>)}
                <Button danger type="text" icon={<Trash2 size={14} />} aria-label={`删除选项 ${option.label}`} disabled={readonly} onClick={() => removeOptionRegion(option.id)} />
              </div>)}
              {(selectedRegion.option_regions ?? []).length === 0 ? <Alert type="warning" showIcon message="尚未配置选项框" description="此题无法自动识别填涂，将转人工处理。" /> : null}
            </section> : null}
			{selectedTemplate ? <section className="template-option-editor">
				<div className="template-pane-head"><strong>选择题识别方式</strong>{calibrations.some((item) => item.status === "approved") ? <StatusTag tone="success">支持自动确认</StatusTag> : <StatusTag tone="neutral">结果需人工复核</StatusTag>}</div>
				<Select
					value={layout.omr_profile?.mode ?? "manual_only"}
					disabled={readonly}
					onChange={setOMRProfile}
					options={[
						{ value: "manual_only", label: "直接识别填涂（每份需人工复核）" },
						{ value: "template_difference", label: "对照空白卷识别（更准确，校准后可自动确认）" }
					]}
				/>
				{(layout.omr_profile?.mode ?? "manual_only") === "template_difference" ? <p>该方式用同一版空白试卷作对照，排除印刷文字干扰。校准通过前，识别结果仅供参考，不会计入成绩。</p> : null}
				{layout.omr_profile?.reference ? <div className="template-help" title={`文件 ${layout.omr_profile.reference.file_asset_id} · 校验码 ${layout.omr_profile.reference.hash_sha256}`}><MousePointer2 size={18} /><p>已绑定本试卷的空白对照页</p></div> : layout.omr_profile?.mode === "template_difference" ? <Alert type="info" showIcon message="保存后将绑定当前试卷原件" description="锁定模板时会自动核对对照页与试卷原件是否一致。" /> : null}
			</section> : null}
			{isTemplateDifference ? <section className="template-option-editor calibration-panel">
				<div className="template-pane-head"><strong>填涂识别校准</strong><StatusTag tone={calibrations.some((item) => item.status === "approved") ? "success" : "warning"}>{calibrations.some((item) => item.status === "approved") ? "校准已通过" : "未校准，全部人工复核"}</StatusTag></div>
				<p>一次校准覆盖整套锁定模板。系统按题目以及高置信、低置信、空白、模糊四类结果均衡抽样；标注前隐藏机器结果。</p>
				{!canCalibrate ? <Alert type="info" showIcon message="需要“评分管理”权限才能创建、标注或审批校准" /> : <>
					<Button block type="primary" loading={calibrationBusy} onClick={() => void startCalibration()}>开始整套模板校准</Button>
					{calibrationError ? <Alert type="warning" showIcon message="校准状态暂不可读取" description={calibrationError} action={<Button size="small" onClick={() => selectedTemplate && void loadOMRCalibrationList(selectedTemplate.id)}>重试</Button>} /> : null}
					{calibrationLoading ? <Spin size="small" /> : calibrations.length ? <List className="calibration-session-list" size="small" dataSource={calibrations.slice(0, 4)} renderItem={(item) => <List.Item actions={[<Button key="open" type="link" size="small" loading={calibrationBusy} onClick={() => void openCalibration(item.id)}>查看</Button>]}>
						<div className="calibration-session-row"><div><strong>{item.scope_type === "template" ? `整套模板 · ${item.question_ids.length} 道题${item.inherited_from_session_id ? " · 继承校准" : ""}` : questions.find((question) => question.id === item.question_id)?.question_no ?? "历史题目级校准"}</strong><span>{item.summary.labeled_count}/{item.summary.total_count} 已标注 · 高置信错误 {item.summary.eligible_mismatch_count}</span></div><StatusTag tone={calibrationStatusTone(item.status)}>{calibrationStatusLabel(item.status)}</StatusTag></div>
					</List.Item>} /> : <Alert type="info" showIcon message="尚无校准记录" description="整套模板累计足够的四类识别结果后，可开始人工校准。" />}
				</>}
			</section> : null}
			{selectedTemplate.status === "locked" && canManage ? <section className="template-option-editor print-package-panel">
				<div className="template-pane-head">
					<strong>答题卡打印包</strong>
					{printContext ? <StatusTag tone={printableCandidates.length ? "processing" : "success"}>{printableCandidates.length ? `${printableCandidates.length} 人待签发` : "签发完成"}</StatusTag> : null}
				</div>
				<p>按班级生成带学生身份条码的受控 PDF。系统不会把姓名或学号印在答题卡上。</p>
				{printContextLoading ? <Spin size="small" /> : null}
				{printContextError ? <Alert type="warning" showIcon message="打印状态暂不可读取" description={printContextError} action={<Button size="small" onClick={() => void loadPrintContext(selectedTemplate.id)}>重试</Button>} /> : null}
				{printContext ? <>
					<div className="print-package-metrics">
						<div><span>应考</span><strong>{printContext.candidates.filter((item) => item.attendance_status === "expected").length}</strong></div>
						<div><span>已有有效答题卡</span><strong>{printContext.candidates.filter((item) => item.has_active_sheet).length}</strong></div>
						<div><span>缺考</span><strong>{printContext.candidates.filter((item) => item.attendance_status === "absent").length}</strong></div>
					</div>
					<Button block type="primary" icon={<Printer size={16} />} disabled={!printableCandidates.length} onClick={openPrintModal}>生成打印包</Button>
					{printContext.batches.length ? <div className="print-batch-history">
						<span>最近签发</span>
						<List size="small" dataSource={printContext.batches.slice(0, 4)} renderItem={(batch) => <List.Item actions={batch.downloadable ? [
							<Button key="download" type="link" size="small" icon={<Download size={14} />} loading={printBusy} onClick={() => void downloadPrintBatch(batch.print_batch_id)}>下载</Button>
						] : []}>
							<div className="print-batch-row">
								<strong>{batch.sheet_count} 份 · {batch.sheet_count * batch.page_count} 页</strong>
								<span>{printBatchTime(batch.issued_at)} · {batch.downloadable ? "可下载" : batch.conflict_count ? "存在冲突" : batch.revoked_count ? "已作废" : "已开始扫描"}</span>
							</div>
						</List.Item>} />
					</div> : <Alert type="info" showIcon message="尚未签发打印包" />}
				</> : null}
			</section> : null}
            <div className="template-summary"><span>页面</span><strong>{layout.pages.length}</strong><span>题目区域</span><strong>{coveredQuestions.size}</strong><span>未配置</span><strong>{Math.max(questions.length - coveredQuestions.size, 0)}</strong></div>
          </aside>
        </section>
      )}
		<Drawer title="填涂识别校准记录" width={760} open={calibrationDrawerOpen} onClose={() => setCalibrationDrawerOpen(false)} destroyOnClose={false}>
			{calibrationDetail ? <div className="calibration-drawer">
				<Alert type={calibrationDetail.session.status === "approved" ? "success" : calibrationDetail.session.status === "revoked" ? "error" : "info"} showIcon message={`状态：${calibrationStatusLabel(calibrationDetail.session.status)}`} description={calibrationDetail.session.status === "approved" ? "校准已生效：整套模板中识别把握不低于 98% 且符合自动确认条件的结果可自动确认，其余仍转人工复核。" : calibrationDetail.session.status === "revoked" ? "撤销已生效：尚未处理完的识别任务将全部转入人工复核。" : "请只依据答题图片盲标；提交后才显示机器结果，且标注不可修改。"} />
				<div className="calibration-metrics">
					<div><span>已标注</span><strong>{calibrationDetail.session.summary.labeled_count}/{calibrationDetail.session.summary.total_count}</strong></div>
					<div><span>高置信正确</span><strong>{calibrationDetail.session.summary.eligible_match_count}/{calibrationDetail.session.summary.eligible_count}</strong></div>
					<div><span>高置信错误</span><strong>{calibrationDetail.session.summary.eligible_mismatch_count}</strong></div>
					<div><span>识别把握下限</span><strong>{Math.round(calibrationDetail.session.minimum_confidence * 100)}%</strong></div>
				</div>
				<Progress percent={calibrationProgress} status={calibrationDetail.session.summary.eligible_mismatch_count > 0 ? "exception" : calibrationProgress === 100 ? "success" : "active"} />
				{calibrationDetail.session.status === "draft" && calibrationDetail.session.summary.blockers.length ? <Alert type="warning" showIcon message="尚不能批准自动确认" description={<ul className="calibration-blockers">{calibrationDetail.session.summary.blockers.map((item) => <li key={item} title={item}>{calibrationBlockerLabel(item)}</li>)}</ul>} /> : null}
				<p className="muted">分层样本覆盖（每类至少 {calibrationDetail.session.minimum_samples_per_stratum} 份）</p>
				<div className="calibration-coverage">{Object.entries(calibrationDetail.session.summary.stratum_coverage).map(([stratum, count]) => <span key={stratum}>{calibrationStratumLabel(stratum as OMRCalibrationCase["sample_stratum"])}: {count}/{calibrationDetail.session.minimum_samples_per_stratum}</span>)}</div>
				<p className="muted">各选项已标注样本数（每个选项至少需 {calibrationDetail.session.minimum_samples_per_option} 份）</p>
				<div className="calibration-coverage">{Object.entries(calibrationDetail.session.summary.option_coverage).map(([option, count]) => <span key={option}>{option}: {count}/{calibrationDetail.session.minimum_samples_per_option}</span>)}</div>
				{selectedCalibrationCase ? <section className="calibration-case">
					<div className="template-pane-head"><strong>{selectedCalibrationCase.question_no} · 样本 {selectedCalibrationIndex + 1}/{calibrationDetail.cases.length}</strong><Space><StatusTag tone="neutral">{selectedCalibrationCase.matches === undefined ? "盲标样本" : calibrationStratumLabel(selectedCalibrationCase.sample_stratum)}</StatusTag><Button size="small" disabled={selectedCalibrationIndex <= 0 || calibrationBusy} onClick={() => setCalibrationCaseId(calibrationDetail.cases[selectedCalibrationIndex - 1].id)}>上一份</Button><Button size="small" disabled={selectedCalibrationIndex < 0 || selectedCalibrationIndex >= calibrationDetail.cases.length - 1 || calibrationBusy} onClick={() => setCalibrationCaseId(calibrationDetail.cases[selectedCalibrationIndex + 1].id)}>下一份</Button></Space></div>
					<div className="calibration-crop">{calibrationImageLoading ? <Spin tip="正在加载答题图片" /> : calibrationImageUrl ? <img src={calibrationImageUrl} alt={`校准样本 ${selectedCalibrationIndex + 1}`} /> : <Alert type="error" showIcon message="图片加载失败" />}</div>
					{selectedCalibrationCase.matches !== undefined ? <Alert type={selectedCalibrationCase.matches ? "success" : "warning"} showIcon message={`人工标注：${selectedCalibrationCase.expected_options?.length ? selectedCalibrationCase.expected_options.join(", ") : "空白"}`} description={`机器识别：${selectedCalibrationCase.observed_options?.length ? selectedCalibrationCase.observed_options?.join(", ") : selectedCalibrationCase.observed_decision} · 识别把握 ${Math.round(selectedCalibrationCase.observed_confidence * 100)}% · ${selectedCalibrationCase.matches ? "一致" : selectedCalibrationCase.sample_stratum === "selected_high" ? "高置信错误，阻断审批" : "不一致，但该分支仍保持人工复核"}`} /> : <><Alert type="info" showIcon message={selectedCalibrationCase.question_type === "multiple_choice" ? "请只根据图片选择所有实际填涂项" : "请只根据图片判断实际填涂项"} description="提交前不显示机器识别选项与置信度，避免影响人工判断。" />{selectedCalibrationCase.question_type === "multiple_choice" ? <><Checkbox.Group options={selectedCalibrationCase.option_labels} value={calibrationExpectedOptions} onChange={(values) => setCalibrationExpectedOptions(values as string[])} /><Space wrap className="calibration-label-actions"><Button type="primary" disabled={!calibrationExpectedOptions.length || calibrationBusy} loading={calibrationBusy} onClick={() => void labelCalibrationCase(calibrationExpectedOptions)}>提交所选项</Button><Button disabled={calibrationBusy} onClick={() => void labelCalibrationCase([])}>标注为空白</Button></Space></> : <Space wrap className="calibration-label-actions">{selectedCalibrationCase.option_labels.map((option) => <Button key={option} type="primary" disabled={calibrationDetail.session.status !== "draft" || calibrationBusy} loading={calibrationBusy} onClick={() => void labelCalibrationCase([option])}>标注为 {option}</Button>)}<Button disabled={calibrationDetail.session.status !== "draft" || calibrationBusy} onClick={() => void labelCalibrationCase([])}>标注为空白</Button></Space>}</>}
				</section> : <EmptyState title="没有可用样本" description="校准样本尚未生成，请先开始校准。" />}
				<div className="calibration-actions">
					{calibrationDetail.session.status === "draft" ? <><Button type="primary" disabled={!calibrationDetail.session.summary.ready_to_approve || calibrationBusy} onClick={() => { setCalibrationActionReason(""); setCalibrationAction("approve"); }}>由独立审批人批准</Button><Button danger disabled={calibrationBusy} onClick={() => { setCalibrationActionReason(""); setCalibrationAction("discard"); }}>弃用草稿</Button></> : null}
					{calibrationDetail.session.status === "approved" ? <Button danger disabled={calibrationBusy} onClick={() => { setCalibrationActionReason(""); setCalibrationAction("revoke"); }}>立即撤销自动确认</Button> : null}
				</div>
				<Collapse
					items={[{
						key: "evidence",
						label: "审计校验信息",
						children: (
							<div className="calibration-evidence">
								<span title={calibrationDetail.session.template_content_hash}>模板校验码：{calibrationDetail.session.template_content_hash.slice(0, 16)}</span>
								<span title={calibrationDetail.session.profile_hash}>配置校验码：{calibrationDetail.session.profile_hash.slice(0, 16)}</span>
								<span title={calibrationDetail.session.reference_sha256}>对照页校验码：{calibrationDetail.session.reference_sha256.slice(0, 16)}</span>
								{calibrationDetail.session.evidence_hash ? <span title={calibrationDetail.session.evidence_hash}>证据校验码：{calibrationDetail.session.evidence_hash.slice(0, 24)}</span> : null}
							</div>
						)
					}]}
				/>
			</div> : <Spin />}
		</Drawer>
		<Modal
			open={printModalOpen}
			title="生成受控答题卡打印包"
			okText={`签发并下载${selectedPrintClassIds.length ? `（${printableCandidates.filter((item) => selectedPrintClassIds.includes(item.class_id)).length} 人）` : ""}`}
			cancelText="取消"
			confirmLoading={printBusy}
			okButtonProps={{ disabled: !selectedPrintClassIds.length }}
			onOk={() => void issuePrintPackage()}
			onCancel={() => !printBusy && setPrintModalOpen(false)}
		>
			<div className="print-package-modal">
				<Alert type="warning" showIcon message="签发后请只打印所下载的这一份文件" description="每名学生、每一页都有唯一条码。重复复印或打乱页面会进入人工核验；补打必须使用作废与重印流程。" />
				<div className="print-class-heading">
					<strong>选择班级</strong>
					<Button type="link" size="small" onClick={() => setSelectedPrintClassIds(selectedPrintClassIds.length === printClasses.length ? [] : printClasses.map((item) => item.id))}>{selectedPrintClassIds.length === printClasses.length ? "取消全选" : "全选"}</Button>
				</div>
				<div className="print-class-list">
					{printClasses.map((item) => <Checkbox key={item.id} checked={selectedPrintClassIds.includes(item.id)} onChange={(event) => setSelectedPrintClassIds((current) => event.target.checked ? [...current, item.id] : current.filter((id) => id !== item.id))}>
						<span>{item.name}</span><small>{item.count} 名待签发</small>
					</Checkbox>)}
				</div>
				<p className="muted">已排除缺考学生和已有有效答题卡的学生，避免重复签发。</p>
			</div>
		</Modal>
		<Modal open={Boolean(calibrationAction)} title={calibrationAction === "approve" ? "批准自动确认校准" : calibrationAction === "revoke" ? "撤销自动确认校准" : "弃用校准草稿"} okText={calibrationAction === "approve" ? "确认批准" : calibrationAction === "revoke" ? "立即撤销" : "弃用草稿"} okButtonProps={{ danger: calibrationAction !== "approve" }} confirmLoading={calibrationBusy} onOk={() => void submitCalibrationAction()} onCancel={() => !calibrationBusy && setCalibrationAction(undefined)}>
			<Alert type={calibrationAction === "approve" ? "warning" : "info"} showIcon message={calibrationAction === "approve" ? "审批人不能是参与标注的人，系统会自动核验。" : calibrationAction === "revoke" ? "撤销后，识别结果不再自动写入成绩，未完成的任务将转入人工复核。" : "弃用不会删除已写入的标注记录，只会终止这份草稿。"} />
			<Input.TextArea autoFocus rows={4} value={calibrationActionReason} onChange={(event) => setCalibrationActionReason(event.target.value)} placeholder="请填写操作原因（至少 10 个字），将记入操作记录" />
		</Modal>
    </div>
  );
}
