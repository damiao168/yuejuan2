import { Alert, Button, Drawer, Input, Modal, Popconfirm, Progress, Segmented, Select, Space, Tooltip } from "antd";
import { Award, BadgeCheck, CircleStop, Eye, Play, RefreshCw, RotateCcw, Search, Sparkles } from "lucide-react";
import { getSafeUserText } from "../../../../api/client";
import type { ReviewTask, ScoringRunItem } from "../../../../api/review";
import { OcrWorkerAlert } from "../../../../components/OcrWorkerAlert";
import { ResponsiveTable } from "../../../../components/ResponsiveTable";
import { ScoringPaperMonitor } from "../../../../components/ScoringPaperMonitor";
import { StatusTag } from "../../../../components/StatusTag";
import {
  confidenceTone,
  formatAnswer,
  gradingConclusion,
  questionTypeLabels,
  reasonLabels,
  recognitionDecisionLabels,
  recognitionSourceLabels,
  resultState,
  scoringItemStateLabels,
  scoringRunStatusLabels,
  taskStatusLabels
} from "../gradingWorkbench.model";
import type { ScoringResultState, ScoringResultType } from "../gradingWorkbench.types";
import type { ExamScoringController } from "../hooks/useExamScoring";
import { scoringModeLabels, scoringRiskLabels } from "../scoringPlan";
import { FastConfirmDrawer } from "./FastConfirmDrawer";

export interface ExamScoringPanelProps {
  initialExamId: string;
  canGrade: boolean;
  canWork: boolean;
  canManageTasks: boolean;
  currentQuestionId: string;
  scoring: ExamScoringController;
  onOpenGoldPapers: () => void;
  onOpenCalibration: (questionId: string) => void;
  tasks: ReviewTask[];
  currentUserId: string;
  onTasksChanged: () => Promise<void>;
}

export function ExamScoringPanel({
  initialExamId,
  canGrade,
  canWork,
  canManageTasks,
  currentQuestionId,
  scoring,
  onOpenGoldPapers,
  onOpenCalibration,
  tasks,
  currentUserId,
  onTasksChanged
}: ExamScoringPanelProps) {
  const fallbackQuestionId = scoring.summary?.questions[0]?.question_id ?? "";
  const fastConfirmExamId = initialExamId || tasks.find((task) => task.assigned_to === currentUserId)?.exam_id || "";
  return (
    <>
      {fastConfirmExamId && canWork ? <div className="grading-fast-confirm-entry"><FastConfirmDrawer key={fastConfirmExamId} tasks={tasks} examId={fastConfirmExamId} currentUserId={currentUserId} onTasksChanged={onTasksChanged} onScoringChanged={async () => { if (canGrade) await scoring.loadSummary(); }} /></div> : null}
      {initialExamId && canGrade ? (
        <section className="grading-overview">
          <div className="grading-overview-head">
            <div><h2>评分与 AI 辅助</h2><p>规则题自动判分，主观题按冻结策略生成 AI 建议或进入人工；最终成绩由教师确认。</p></div>
            <Space wrap>
              <Button icon={<RefreshCw size={15} />} loading={scoring.loading} onClick={() => void scoring.loadSummary()}>刷新</Button>
              <Button type={scoring.summary?.run ? "primary" : "default"} icon={<Eye size={15} />} loading={scoring.actioning === "scoring-detail"} onClick={() => void scoring.showDetail()}>逐卷查看</Button>
              {canManageTasks ? <Button icon={<Award size={15} />} onClick={onOpenGoldPapers}>标准卷</Button> : null}
              {canManageTasks && (currentQuestionId || fallbackQuestionId) ? <Button icon={<BadgeCheck size={15} />} onClick={() => onOpenCalibration(currentQuestionId || fallbackQuestionId)}>阅卷校准</Button> : null}
              {scoring.summary?.run && scoring.summary.run.failed_count > 0 ? <Button icon={<RotateCcw size={15} />} loading={scoring.actioning === "retry-scoring"} onClick={() => void scoring.retry()}>重新处理失败项</Button> : null}
              {scoring.summary?.run && ["queued", "processing", "needs_review", "failed", "cancelling"].includes(scoring.summary.run.status) ? (
                <Popconfirm title={scoring.summary.run.status === "cancelling" ? "继续完成取消？" : "取消本次评分？"} description="未完成的规则、AI 建议和人工任务将停止，已保留的历史结果不会删除。" okText={scoring.summary.run.status === "cancelling" ? "继续取消" : "取消评分"} cancelText="保留" okButtonProps={{ danger: true }} onConfirm={() => void scoring.cancel()}>
                  <Button danger icon={<CircleStop size={15} />} loading={scoring.actioning === "cancel-scoring"}>{scoring.summary.run.status === "cancelling" ? "继续取消" : "取消评分"}</Button>
                </Popconfirm>
              ) : null}
              {scoring.pendingCommand ? <Button loading={scoring.actioning === "start-scoring"} onClick={() => void scoring.start()}>继续确认评分</Button> : !scoring.hasUnresolvedRun && scoring.summary?.run ? (
                <Button icon={<Play size={15} />} disabled={!scoring.readiness?.ready} loading={scoring.actioning === "prepare-scoring"} onClick={() => void scoring.prepareStart()}>重新评分</Button>
              ) : !scoring.hasUnresolvedRun ? <Button type="primary" icon={<Play size={15} />} disabled={!scoring.readiness?.ready} loading={scoring.actioning === "prepare-scoring"} onClick={() => void scoring.prepareStart()}>开始评分</Button> : null}
            </Space>
          </div>
          {scoring.readiness ? (
            <Alert
              className="grading-readiness-alert"
              type={scoring.hasUnresolvedRun ? "info" : scoring.readiness.ready ? "success" : "error"}
              showIcon
              message={scoring.hasUnresolvedRun ? "已有评分任务，不能重复启动" : scoring.readiness.ready ? "评分启动条件已满足" : `${scoring.blockingChecks.length} 项条件阻止启动评分`}
              description={(
                <div className="grading-readiness-content">
                  <div className="grading-readiness-counts">
                    <span>题目 <strong>{scoring.readiness.total_questions}</strong></span>
                    <span>题块 <strong>{scoring.readiness.ready_segments}/{scoring.readiness.total_segments}</strong></span>
                    <span>规则自动 <strong>{scoring.readiness.automatic_candidates}</strong></span>
                    <span>人工任务 <strong>{scoring.readiness.manual_review_candidates}</strong></span>
                  </div>
                  {(scoring.hasUnresolvedRun ? scoring.blockingChecks.filter((check) => check.code === "active_run_clear") : [...scoring.blockingChecks, ...scoring.warningChecks]).length ? (
                    <ul className="grading-readiness-issues">
                      {(scoring.hasUnresolvedRun ? scoring.blockingChecks.filter((check) => check.code === "active_run_clear") : [...scoring.blockingChecks, ...scoring.warningChecks]).map((check) => (
                        <li key={check.code} className={check.severity}><strong>{getSafeUserText(check.label, "评分准备检查")}</strong><span>{getSafeUserText(check.message, "检查未通过，请完成相关设置")}</span></li>
                      ))}
                    </ul>
                  ) : <span className="grading-readiness-ok">全部题块均可进入规则处理或人工复核；AI 建议需另通过准入检查。</span>}
                </div>
              )}
            />
          ) : null}
          {scoring.summary?.run ? (
            <div className="grading-run-strip">
              <StatusTag tone={scoring.summary.run.status === "completed" ? "success" : scoring.summary.run.status === "failed" ? "danger" : scoring.summary.run.status === "needs_review" ? "warning" : "processing"}>{scoringRunStatusLabels[scoring.summary.run.status] ?? "处理中"}</StatusTag>
              <span>总计 <strong>{scoring.summary.run.total_count}</strong></span>
              <span>处理中 <strong>{scoring.summary.run.queued_count}</strong></span>
              <span>自动确认 <strong>{scoring.summary.run.auto_confirmed_count}</strong></span>
              <span>人工完成 <strong>{scoring.summary.run.human_confirmed_count}</strong></span>
              <span>待人工 <strong>{scoring.summary.run.review_count}</strong></span>
              <span>失败 <strong>{scoring.summary.run.failed_count}</strong></span>
            </div>
          ) : <Alert type="info" showIcon message="尚未开始评分" description="请先完成答题卡上传与题目切分；启动前会展示每题的规则、AI 建议和人工路线。" />}
          {scoring.aiProgress ? <div className="grading-ai-progress"><div><Sparkles size={17} /><strong>AI 辅助建议</strong><span>{scoring.aiProgress.succeeded}/{scoring.aiProgress.total} 已完成</span><span>{scoring.aiProgress.queued + scoring.aiProgress.processing} 处理中</span><span>{scoring.aiProgress.failed} 累计失败（含重试历史）</span>{scoring.aiProgress.cancelled ? <span>{scoring.aiProgress.cancelled} 已取消</span> : null}</div><Progress percent={scoring.aiProgress.total ? Math.round((scoring.aiProgress.succeeded + scoring.aiProgress.failed + scoring.aiProgress.cancelled) / scoring.aiProgress.total * 100) : 0} size="small" status={scoring.aiProgress.failed ? "exception" : undefined} /></div> : null}
          {scoring.retryableAIBatches.length ? <Alert type="warning" showIcon message={`${scoring.retryableAIBatches.length} 个 AI 批次包含失败建议；人工阅卷可继续`} action={scoring.summary?.run && ["queued", "processing", "needs_review", "failed"].includes(scoring.summary.run.status) ? <Button size="small" loading={scoring.actioning === "retry-failed-ai"} onClick={() => void scoring.retryFailedAI()}>重试失败的 AI 建议</Button> : undefined} /> : null}
          {scoring.aiIssue ? <Alert type="warning" showIcon message={scoring.aiIssue} action={scoring.summary?.run && ["queued", "processing", "needs_review", "failed"].includes(scoring.summary.run.status) ? <Button size="small" loading={scoring.actioning === "enqueue-ai"} onClick={() => void scoring.retryAI()}>重试 AI 入队</Button> : undefined} /> : null}
          <ResponsiveTable className="dense-data-table" size="small" pagination={false} loading={scoring.loading} rowKey="question_id" dataSource={scoring.summary?.questions ?? []} columns={[
            { title: "题号", dataIndex: "question_no", width: 90 },
            { title: "题型", dataIndex: "question_type", width: 140, render: (value: string) => <span title={value}>{questionTypeLabels[value] ?? "其他题型"}</span> },
            { title: "答卷", dataIndex: "total", width: 80 },
            { title: "处理中", dataIndex: "queued", width: 90 },
            { title: "已确认", dataIndex: "confirmed", width: 90 },
            { title: "待人工", dataIndex: "review", width: 90 },
            { title: "失败", dataIndex: "failed", width: 80 }
          ]} />
        </section>
      ) : null}
      <OcrWorkerAlert enabled={canWork} />
      <Modal title="确认本次评分路线" open={scoring.planOpen} onCancel={() => scoring.setPlanOpen(false)} onOk={() => void scoring.start()} okText="按此路线启动" okButtonProps={{ loading: scoring.actioning === "start-scoring" }} cancelText="返回核对" width={760}>
        {scoring.plan ? <div className="grading-plan">
          <p>按冻结的题目策略分流。AI 只写建议，不自动发布分数；准入不通过或模型失败时保留人工任务。</p>
          <div className="grading-plan-counts"><span>预计规则自动 <strong>{scoring.plan.counts.rule}</strong></span><span>预计 AI 建议 <strong>{scoring.plan.counts.ai}</strong></span><span>预计人工 <strong>{scoring.plan.counts.human}</strong></span></div>
          {scoring.plan.snapshotFailures ? <Alert type="warning" showIcon message={`${scoring.plan.snapshotFailures} 道题的冻结策略暂不可读，按人工路线处理`} /> : null}
          <div className="grading-plan-list">{scoring.plan.questions.map((question) => <div key={question.questionId}><strong>{question.questionNo} · {questionTypeLabels[question.questionType] ?? question.questionType}</strong><span>{question.count} 份</span><span>{scoringModeLabels[question.mode] ?? "人工复核"}</span><span>{scoringRiskLabels[question.riskTier] ?? question.riskTier}</span><small>{question.note}</small></div>)}</div>
        </div> : null}
      </Modal>
      <Drawer title="自动阅卷 · 逐卷查看" width="min(1680px, 98vw)" open={scoring.detailOpen} onClose={() => scoring.setDetailOpen(false)} destroyOnClose={false}>
        <div className="automation-results">
          <ScoringPaperMonitor run={scoring.summary?.run} items={scoring.runDetail?.items ?? []} loading={scoring.actioning === "scoring-detail"} onRefresh={() => void scoring.showDetail()} />
          <div className="automation-results-head">
            <div><strong>全部判分结果</strong><span>逐条核对每道题的识别结果与自动判分，可按题型和状态筛选。</span></div>
            <div className="automation-result-metrics">
              <span>总计 <strong>{scoring.resultMetrics.total}</strong></span>
              <span>自动确认 <strong>{scoring.resultMetrics.auto}</strong></span>
              <span>待人工 <strong>{scoring.resultMetrics.review}</strong></span>
              <span className={scoring.resultMetrics.failed ? "danger" : ""}>失败 <strong>{scoring.resultMetrics.failed}</strong></span>
            </div>
          </div>
          <div className="automation-result-filters">
            <Segmented<ScoringResultType> value={scoring.resultType} options={[{ label: "全部题型", value: "all" }, { label: "选择题", value: "choice" }, { label: "填空与数值", value: "fill" }]} onChange={scoring.setResultType} />
            <Select<ScoringResultState> value={scoring.resultState} options={[{ label: "全部状态", value: "all" }, { label: "自动/人工已确认", value: "confirmed" }, { label: "待人工复核", value: "review" }, { label: "处理失败", value: "failed" }, { label: "处理中", value: "processing" }]} onChange={scoring.setResultState} />
            <Input allowClear prefix={<Search size={15} />} value={scoring.resultKeyword} placeholder="搜索匿名码、题号或答案" onChange={(event) => scoring.setResultKeyword(event.target.value)} />
            <span className="muted">{scoring.filteredItems.length} 条</span>
          </div>
          <ResponsiveTable<ScoringRunItem>
            className="automation-result-table dense-data-table"
            size="small"
            rowKey="answer_segment_id"
            dataSource={scoring.filteredItems}
            pagination={{ pageSize: 15, showSizeChanger: true, showTotal: (total) => `共 ${total} 条` }}
            mobilePrimaryCount={4}
            columns={[
              { title: "匿名码", dataIndex: "anonymous_code", width: 138, ellipsis: true },
              { title: "题目", width: 100, render: (_, item) => <div className="automation-result-cell"><strong>{item.question_no}</strong><span title={item.question_type}>{questionTypeLabels[item.question_type] ?? "其他题型"}</span></div> },
              { title: "识别答案", width: 150, render: (_, item) => <div className="automation-result-cell"><strong>{item.recognized_answer || recognitionDecisionLabels[item.recognition_decision ?? ""] || "-"}</strong><span title={[item.recognition_source, item.recognition_decision].filter(Boolean).join(" / ") || undefined}>{recognitionSourceLabels[item.recognition_source ?? ""] ?? "未识别"} · {recognitionDecisionLabels[item.recognition_decision ?? ""] ?? "待处理"}</span></div> },
              { title: "标准答案", width: 135, render: (_, item) => <span className="automation-standard-answer">{formatAnswer(item.standard_answer)}</span> },
              { title: "置信度", width: 92, render: (_, item) => typeof item.recognition_confidence === "number" ? <StatusTag tone={confidenceTone(item.recognition_confidence)}>{`${Math.round(item.recognition_confidence * 100)}%`}</StatusTag> : "-" },
              { title: "判分", width: 120, render: (_, item) => <div className="automation-result-cell"><strong>{typeof item.score === "number" ? `${item.score} / ${item.max_score ?? "-"}` : "-"}</strong><span>{gradingConclusion(item)}</span></div> },
              { title: "处理结果", width: 118, render: (_, item) => { const state = resultState(item); return <StatusTag tone={state.tone}>{state.label}</StatusTag>; } },
              { title: "说明", width: 180, ellipsis: true, render: (_, item) => {
                const rawCode = item.error_code || item.reason_code;
                if (rawCode) return <Tooltip title={rawCode}><span>{reasonLabels[rawCode] ?? "处理异常"}</span></Tooltip>;
                if (item.grade_source === "rule_confirmed") return "规则与证据通过";
                if (item.state === "confirmed") return "人工复核完成";
                const status = item.runtime_status || item.review_status;
                if (!status) return "-";
                return <span title={status}>{scoringItemStateLabels[status] ?? taskStatusLabels[status] ?? reasonLabels[status] ?? "处理中"}</span>;
              } },
              { title: "操作", width: 88, fixed: "right", render: (_, item) => <Button type="link" size="small" icon={<Eye size={14} />} loading={scoring.imageLoading === item.answer_segment_id} onClick={() => void scoring.showImage(item)}>查看答题图</Button> }
            ]}
          />
        </div>
      </Drawer>
      <Modal title={scoring.image?.title ?? "答题图"} open={Boolean(scoring.image)} footer={null} width={920} onCancel={scoring.closeImage} destroyOnClose>
        {scoring.image?.contentType.startsWith("image/") ? <div className="automation-image-preview"><img src={scoring.image.url} alt={scoring.image.title} /></div> : <Alert type="warning" showIcon message="该答题片段不是可直接预览的图片格式" />}
      </Modal>
    </>
  );
}
