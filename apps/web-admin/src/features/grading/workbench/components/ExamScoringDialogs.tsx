import { Alert, Button, Drawer, Input, Modal, Segmented, Select, Tooltip } from "antd";
import { useState } from "react";
import { Eye, Search } from "lucide-react";
import type { ScoringRunItem } from "../../../../api/review";
import { ResponsiveTable } from "../../../../components/ResponsiveTable";
import { ScoringPaperMonitor } from "../../../../components/ScoringPaperMonitor";
import { StatusTag } from "../../../../components/StatusTag";
import { MathMarkdown } from "../../../../components/MathMarkdown";
import { imageRecognitionSummary, scoringProgressLabel } from "../scoringProgress";
import { confidenceTone, formatAnswer, gradingConclusion, questionTypeLabels, reasonLabels, recognitionDecisionLabels, recognitionSourceLabels, resultState, scoringItemStateLabels, taskStatusLabels } from "../gradingWorkbench.model";
import type { ScoringResultState, ScoringResultType } from "../gradingWorkbench.types";
import type { ExamScoringController } from "../hooks/useExamScoring";
import { scoringModeLabels, scoringRiskLabels } from "../scoringPlan";
import { scoringReviewCategory, type ScoringReviewFilter } from "../scoringReviewCategories";
import { GradingAIPlanSummary } from "./GradingAIPlanSettings";
import { ImageGradingRecordLoader } from "./ImageGradingRecord";

interface ControllerProps { scoring: ExamScoringController; }

export function ExamScoringPlanDialog({ scoring, examId, onConfigureAI }: Readonly<ControllerProps & { examId?: string; onConfigureAI?: () => void }>) {
  return <Modal title="确认本次评分路线" open={scoring.planOpen} onCancel={() => scoring.setPlanOpen(false)} onOk={() => void scoring.start()} okText="按此路线启动" okButtonProps={{ loading: scoring.actioning === "start-scoring" }} cancelText="返回核对" width={760}>
    {scoring.plan ? <div className="grading-plan">
      <p>系统将自动完成作答识别、规则判分和 AI 建议生成。AI 建议由教师确认最终分数，处理过程中可关闭页面。</p>
      {scoring.planOpen && examId ? <GradingAIPlanSummary examId={examId} onConfigure={onConfigureAI} /> : null}
      <div className="grading-plan-counts"><span>预计规则自动 <strong>{scoring.plan.counts.rule}</strong></span><span>AI 建议 <strong>{scoring.plan.counts.ai}</strong></span><span>其他人工 <strong>{scoring.plan.counts.human}</strong></span></div>
      {scoring.plan.snapshotFailures ? <Alert type="warning" showIcon message={`${scoring.plan.snapshotFailures} 道题的冻结策略暂不可读，按人工路线处理`} /> : null}
      <div className="grading-plan-list">{scoring.plan.questions.map((question) => <div key={question.questionId}><strong>{question.questionNo} · {questionTypeLabels[question.questionType] ?? question.questionType}</strong><span>{question.count} 份</span><span>{scoringModeLabels[question.mode] ?? "人工复核"}</span><span>{scoringRiskLabels[question.riskTier] ?? question.riskTier}</span><small>{question.note}</small></div>)}</div>
    </div> : null}
  </Modal>;
}

function scoringItemExplanation(item: ScoringRunItem) {
  if (item.decision_status === "auto_confirmed") return "规则评分已确认";
  if (item.decision_status === "human_confirmed") return "教师评分已确认";
  const rawCode = item.error_code || item.reason_code;
  if (rawCode) return <Tooltip title={rawCode}><span>{reasonLabels[rawCode] ?? scoringProgressLabel(item) ?? "请查看本题处理详情"}</span></Tooltip>;
  const progressLabel = scoringProgressLabel(item);
  if (progressLabel) return progressLabel;
  if (item.grade_source === "rule_confirmed") return "规则与证据通过";
  if (item.state === "confirmed") return "人工复核完成";
  const status = item.runtime_status || item.review_status;
  if (!status) return "-";
  return <span title={status}>{scoringItemStateLabels[status] ?? taskStatusLabels[status] ?? reasonLabels[status] ?? "处理中"}</span>;
}

function scoringRecognizedAnswer(item: ScoringRunItem) {
  const imageSummary = imageRecognitionSummary(item);
  const answer = item.recognized_answer?.trim();
  return <div className="automation-result-cell">
    {answer && answer !== "-" ? <MathMarkdown>{answer}</MathMarkdown> : <strong>{imageSummary?.label ?? recognitionDecisionLabels[item.recognition_decision ?? ""] ?? "—"}</strong>}
    <span title={[item.recognition_source, item.recognition_decision].filter(Boolean).join(" / ") || undefined}>{scoringReviewCategory(item) === "recognition" ? "识别结果待核对" : imageSummary?.detail ?? `${recognitionSourceLabels[item.recognition_source ?? ""] ?? "未识别"} · ${recognitionDecisionLabels[item.recognition_decision ?? ""] ?? "待处理"}`}</span>
  </div>;
}

function scoringStandardAnswer(item: ScoringRunItem) {
  const answer = formatAnswer(item.standard_answer);
  // 缺省占位不是 Markdown 内容；单个负号会被解析成空列表项。
  return answer === "-" ? <span className="automation-standard-answer">—</span> : <MathMarkdown className="automation-standard-answer">{answer}</MathMarkdown>;
}

function ExamScoringResultsContent({ scoring }: Readonly<ControllerProps>) {
  const [previewOpen, setPreviewOpen] = useState(false);
  const [recognitionDetails, setRecognitionDetails] = useState(false);
  const items = scoring.runDetail?.items ?? [];
  const papers = [...new Set(items.map(item => item.submission_id || item.anonymous_code))];
  const confirmed = items.filter(item => item.state === "confirmed").length;
  const hasFilters = scoring.resultType !== "all" || scoring.resultState !== "all" || (scoring.resultReviewFilter ?? "all") !== "all" || Boolean(scoring.resultKeyword.trim());
  const clearFilters = () => { scoring.setResultType("all"); scoring.setResultState("all"); scoring.setResultReviewFilter("all"); scoring.setResultKeyword(""); };
  return <div className="automation-results">
      <header className="grading-results-heading"><div><h1>答卷结果</h1><p>{papers.length} 份答卷 · {items.length} 道题 · 已确认 {confirmed}</p></div>
        <Button loading={scoring.actioning === "scoring-detail"} onClick={() => void scoring.showDetail()}>刷新结果</Button>
      </header>
      {scoring.detailError ? <Alert type="warning" showIcon message="答卷结果暂未更新" description={scoring.detailError} /> : null}
      <div className="automation-result-filters">
        <Segmented<ScoringResultType> aria-label="按题型筛选" value={scoring.resultType} options={[{ label: "全部题型", value: "all" }, { label: "选择题", value: "choice" }, { label: "填空题", value: "fill" }, { label: "其他主观题", value: "subjective" }]} onChange={scoring.setResultType} />
        <Select<ScoringReviewFilter> aria-label="按待办原因筛选" value={scoring.resultReviewFilter ?? "all"} options={[{ label: "全部待办原因", value: "all" }, { label: "选择题核对", value: "choice" }, { label: "建议分确认", value: "suggestion" }, { label: "识别不确定", value: "recognition" }, { label: "人工评分", value: "manual" }]} onChange={scoring.setResultReviewFilter} />
        <Select<ScoringResultState> aria-label="按处理状态筛选" value={scoring.resultState} options={[{ label: "全部状态", value: "all" }, { label: "已确认", value: "confirmed" }, { label: "待确认", value: "review" }, { label: "处理失败", value: "failed" }, { label: "处理中", value: "processing" }]} onChange={scoring.setResultState} />
        <Input aria-label="搜索答卷结果" allowClear prefix={<Search size={15} />} value={scoring.resultKeyword} placeholder="搜索题号、答案或匿名码" onChange={(event) => scoring.setResultKeyword(event.target.value)} />
        {hasFilters ? <Button type="link" onClick={clearFilters}>清除筛选</Button> : null}
      </div>
      <div className="grading-results-tools"><span>当前 {scoring.filteredItems.length} 道题</span><div>
        <Button type="text" aria-expanded={previewOpen} onClick={() => setPreviewOpen(open => !open)}>{previewOpen ? "收起整卷预览" : "查看整卷与评分标记"}</Button>
        <Button type="text" aria-pressed={recognitionDetails} onClick={() => setRecognitionDetails(open => !open)}>{recognitionDetails ? "收起识别详情" : "显示识别详情"}</Button>
      </div></div>
      {/* 整卷影像按需挂载；进入结果页先处理筛选后的题目，避免自动下载所有页面。 */}
      {previewOpen ? <ScoringPaperMonitor run={scoring.summary?.run} items={items} loading={scoring.actioning === "scoring-detail"} onRefresh={() => void scoring.showDetail()} /> : null}
      <ResponsiveTable<ScoringRunItem> className="automation-result-table dense-data-table" size="small" loading={scoring.actioning === "scoring-detail" && !items.length} rowKey="answer_segment_id" dataSource={scoring.filteredItems} pagination={{ pageSize: 15, showSizeChanger: true, showTotal: (total) => `共 ${total} 道题` }} mobilePrimaryCount={4} columns={[
        { title: "答卷", width: 96, ellipsis: true, render: (_, item) => <span title={item.anonymous_code}>{/^[-\da-f]{36}$/i.test(item.anonymous_code) ? `答卷 ${String(papers.indexOf(item.submission_id || item.anonymous_code) + 1).padStart(2, "0")}` : item.anonymous_code}</span> },
        { title: "题目", width: 100, render: (_, item) => <div className="automation-result-cell"><strong>{item.question_no}</strong><span title={item.question_type}>{questionTypeLabels[item.question_type] ?? "其他题型"}</span></div> },
        { title: "识别答案", width: 150, render: (_, item) => scoringRecognizedAnswer(item) },
        { title: "标准答案", width: 135, render: (_, item) => scoringStandardAnswer(item) },
        ...(recognitionDetails ? [{ title: "识别置信度", width: 104, render: (_: unknown, item: ScoringRunItem) => typeof item.recognition_confidence === "number" ? <StatusTag tone={confidenceTone(item.recognition_confidence)}>{`${Math.round(item.recognition_confidence * 100)}%`}</StatusTag> : "—" }] : []),
        { title: "得分", width: 110, render: (_, item) => <div className="automation-result-cell"><strong>{typeof item.score === "number" ? `${item.score} / ${item.max_score ?? "—"}` : "—"}</strong><span>{item.grade_source === "ai_suggestion" ? "AI 建议分" : gradingConclusion(item)}</span></div> },
        { title: "处理结果", width: 190, render: (_, item) => { const state = resultState(item); return <div className="automation-result-cell"><StatusTag tone={state.tone}>{state.label}</StatusTag><span>{scoringItemExplanation(item)}</span></div>; } },
        { title: "操作", width: 142, fixed: "right", render: (_, item) => <Button className="grading-view-answer" type="link" size="small" icon={<Eye size={14} />} loading={scoring.imageLoading === item.answer_segment_id} onClick={() => void scoring.showImage(item)}>查看作答与评分</Button> }
      ]} />
    </div>;
}

export function ExamScoringResultsView(props: Readonly<ControllerProps>) {
  return <section className="grading-results-workspace" aria-label="答卷结果"><ExamScoringResultsContent {...props} /></section>;
}

export function ExamScoringResultsDrawer({ scoring }: Readonly<ControllerProps>) {
  return <Drawer title="答卷结果" width="min(1680px, 98vw)" open={scoring.detailOpen} onClose={() => scoring.setDetailOpen(false)} destroyOnHidden={false}><ExamScoringResultsContent scoring={scoring} /></Drawer>;
}

export function ExamScoringImageDialog({ scoring }: Readonly<ControllerProps>) {
  const item = scoring.image?.item;
  return <Drawer title={scoring.image?.title ?? "作答详情"} open={Boolean(scoring.image)} width="min(1040px, 90vw)" onClose={scoring.closeImage} destroyOnHidden>
    {item ? <div className="grading-answer-detail"><div><strong>识别结果</strong>{scoringRecognizedAnswer(item)}</div><div><strong>标准答案</strong>{scoringStandardAnswer(item)}</div><div><strong>处理说明</strong><p>{scoringItemExplanation(item)}</p></div></div> : null}
    {scoring.image ? scoring.image.contentType.startsWith("image/") ? <div className="automation-image-preview"><img src={scoring.image.url} alt={scoring.image.title} /></div> : <Alert type="warning" showIcon message="该答题片段不是可直接预览的图片格式" /> : null}
    {item ? <ImageGradingRecordLoader key={`${item.answer_segment_id}:${item.input_version_id}`} item={item} /> : null}
  </Drawer>;
}
