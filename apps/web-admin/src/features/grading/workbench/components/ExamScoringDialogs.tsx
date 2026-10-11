import { Alert, Button, Drawer, Input, Modal, Segmented, Select, Tooltip } from "antd";
import { Eye, Search } from "lucide-react";
import type { ScoringRunItem } from "../../../../api/review";
import { ResponsiveTable } from "../../../../components/ResponsiveTable";
import { ScoringPaperMonitor } from "../../../../components/ScoringPaperMonitor";
import { StatusTag } from "../../../../components/StatusTag";
import { confidenceTone, formatAnswer, gradingConclusion, questionTypeLabels, reasonLabels, recognitionDecisionLabels, recognitionSourceLabels, resultState, scoringItemStateLabels, taskStatusLabels } from "../gradingWorkbench.model";
import type { ScoringResultState, ScoringResultType } from "../gradingWorkbench.types";
import type { ExamScoringController } from "../hooks/useExamScoring";
import { scoringModeLabels, scoringRiskLabels } from "../scoringPlan";

interface ControllerProps { scoring: ExamScoringController; }

export function ExamScoringPlanDialog({ scoring }: Readonly<ControllerProps>) {
  return <Modal title="确认本次评分路线" open={scoring.planOpen} onCancel={() => scoring.setPlanOpen(false)} onOk={() => void scoring.start()} okText="按此路线启动" okButtonProps={{ loading: scoring.actioning === "start-scoring" }} cancelText="返回核对" width={760}>
    {scoring.plan ? <div className="grading-plan">
      <p>按冻结的题目策略分流。AI 只写建议，不自动发布分数；准入不通过或模型失败时保留人工任务。</p>
      <div className="grading-plan-counts"><span>预计规则自动 <strong>{scoring.plan.counts.rule}</strong></span><span>预计 AI 建议 <strong>{scoring.plan.counts.ai}</strong></span><span>预计人工 <strong>{scoring.plan.counts.human}</strong></span></div>
      {scoring.plan.snapshotFailures ? <Alert type="warning" showIcon message={`${scoring.plan.snapshotFailures} 道题的冻结策略暂不可读，按人工路线处理`} /> : null}
      <div className="grading-plan-list">{scoring.plan.questions.map((question) => <div key={question.questionId}><strong>{question.questionNo} · {questionTypeLabels[question.questionType] ?? question.questionType}</strong><span>{question.count} 份</span><span>{scoringModeLabels[question.mode] ?? "人工复核"}</span><span>{scoringRiskLabels[question.riskTier] ?? question.riskTier}</span><small>{question.note}</small></div>)}</div>
    </div> : null}
  </Modal>;
}

function scoringItemExplanation(item: ScoringRunItem) {
  const rawCode = item.error_code || item.reason_code;
  if (rawCode) return <Tooltip title={rawCode}><span>{reasonLabels[rawCode] ?? "处理异常"}</span></Tooltip>;
  if (item.grade_source === "rule_confirmed") return "规则与证据通过";
  if (item.state === "confirmed") return "人工复核完成";
  const status = item.runtime_status || item.review_status;
  if (!status) return "-";
  return <span title={status}>{scoringItemStateLabels[status] ?? taskStatusLabels[status] ?? reasonLabels[status] ?? "处理中"}</span>;
}

export function ExamScoringResultsDrawer({ scoring }: Readonly<ControllerProps>) {
  return <Drawer title="自动阅卷 · 逐卷查看" width="min(1680px, 98vw)" open={scoring.detailOpen} onClose={() => scoring.setDetailOpen(false)} destroyOnHidden={false}>
    <div className="automation-results">
      <ScoringPaperMonitor run={scoring.summary?.run} items={scoring.runDetail?.items ?? []} loading={scoring.actioning === "scoring-detail"} onRefresh={() => void scoring.showDetail()} />
      <div className="automation-results-head">
        <div><strong>全部判分结果</strong><span>逐条核对每道题的识别结果与自动判分，可按题型和状态筛选。</span></div>
        <div className="automation-result-metrics"><span>总计 <strong>{scoring.resultMetrics.total}</strong></span><span>自动确认 <strong>{scoring.resultMetrics.auto}</strong></span><span>待人工 <strong>{scoring.resultMetrics.review}</strong></span><span className={scoring.resultMetrics.failed ? "danger" : ""}>失败 <strong>{scoring.resultMetrics.failed}</strong></span></div>
      </div>
      <div className="automation-result-filters">
        <Segmented<ScoringResultType> value={scoring.resultType} options={[{ label: "全部题型", value: "all" }, { label: "选择题", value: "choice" }, { label: "填空与数值", value: "fill" }]} onChange={scoring.setResultType} />
        <Select<ScoringResultState> value={scoring.resultState} options={[{ label: "全部状态", value: "all" }, { label: "自动/人工已确认", value: "confirmed" }, { label: "待人工复核", value: "review" }, { label: "处理失败", value: "failed" }, { label: "处理中", value: "processing" }]} onChange={scoring.setResultState} />
        <Input allowClear prefix={<Search size={15} />} value={scoring.resultKeyword} placeholder="搜索匿名码、题号或答案" onChange={(event) => scoring.setResultKeyword(event.target.value)} />
        <span className="muted">{scoring.filteredItems.length} 条</span>
      </div>
      <ResponsiveTable<ScoringRunItem> className="automation-result-table dense-data-table" size="small" rowKey="answer_segment_id" dataSource={scoring.filteredItems} pagination={{ pageSize: 15, showSizeChanger: true, showTotal: (total) => `共 ${total} 条` }} mobilePrimaryCount={4} columns={[
        { title: "匿名码", dataIndex: "anonymous_code", width: 138, ellipsis: true },
        { title: "题目", width: 100, render: (_, item) => <div className="automation-result-cell"><strong>{item.question_no}</strong><span title={item.question_type}>{questionTypeLabels[item.question_type] ?? "其他题型"}</span></div> },
        { title: "识别答案", width: 150, render: (_, item) => <div className="automation-result-cell"><strong>{item.recognized_answer || recognitionDecisionLabels[item.recognition_decision ?? ""] || "-"}</strong><span title={[item.recognition_source, item.recognition_decision].filter(Boolean).join(" / ") || undefined}>{recognitionSourceLabels[item.recognition_source ?? ""] ?? "未识别"} · {recognitionDecisionLabels[item.recognition_decision ?? ""] ?? "待处理"}</span></div> },
        { title: "标准答案", width: 135, render: (_, item) => <span className="automation-standard-answer">{formatAnswer(item.standard_answer)}</span> },
        { title: "置信度", width: 92, render: (_, item) => typeof item.recognition_confidence === "number" ? <StatusTag tone={confidenceTone(item.recognition_confidence)}>{`${Math.round(item.recognition_confidence * 100)}%`}</StatusTag> : "-" },
        { title: "判分", width: 120, render: (_, item) => <div className="automation-result-cell"><strong>{typeof item.score === "number" ? `${item.score} / ${item.max_score ?? "-"}` : "-"}</strong><span>{gradingConclusion(item)}</span></div> },
        { title: "处理结果", width: 118, render: (_, item) => { const state = resultState(item); return <StatusTag tone={state.tone}>{state.label}</StatusTag>; } },
        { title: "说明", width: 180, ellipsis: true, render: (_, item) => scoringItemExplanation(item) },
        { title: "操作", width: 88, fixed: "right", render: (_, item) => <Button type="link" size="small" icon={<Eye size={14} />} loading={scoring.imageLoading === item.answer_segment_id} onClick={() => void scoring.showImage(item)}>查看答题图</Button> }
      ]} />
    </div>
  </Drawer>;
}

export function ExamScoringImageDialog({ scoring }: Readonly<ControllerProps>) {
  return <Modal title={scoring.image?.title ?? "答题图"} open={Boolean(scoring.image)} footer={null} width={920} onCancel={scoring.closeImage} destroyOnHidden>
    {scoring.image?.contentType.startsWith("image/") ? <div className="automation-image-preview"><img src={scoring.image.url} alt={scoring.image.title} /></div> : <Alert type="warning" showIcon message="该答题片段不是可直接预览的图片格式" />}
  </Modal>;
}
