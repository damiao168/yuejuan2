import { Alert, Button, Popconfirm, Progress, Space } from "antd";
import { Award, BadgeCheck, CircleStop, Eye, Play, RefreshCw, RotateCcw, Sparkles } from "lucide-react";
import { getSafeUserText } from "../../../../api/client";
import { ResponsiveTable } from "../../../../components/ResponsiveTable";
import { StatusTag } from "../../../../components/StatusTag";
import type { StatusTone } from "../../../../types";
import { questionTypeLabels, scoringRunStatusLabels } from "../gradingWorkbench.model";
import type { ExamScoringController } from "../hooks/useExamScoring";

interface ControllerProps { scoring: ExamScoringController; }

function ScoringStartAction({ scoring }: Readonly<ControllerProps>) {
  // 未确认命令先续认同一操作，不能变成另一次新启动；活动 run 没有重复启动入口。
  if (scoring.pendingCommand) return <Button loading={scoring.actioning === "start-scoring"} onClick={() => void scoring.start()}>继续确认评分</Button>;
  if (scoring.hasUnresolvedRun) return null;
  if (scoring.summary?.run) return <Button icon={<Play size={15} />} disabled={!scoring.readiness?.ready} loading={scoring.actioning === "prepare-scoring"} onClick={() => void scoring.prepareStart()}>重新评分</Button>;
  return <Button type="primary" icon={<Play size={15} />} disabled={!scoring.readiness?.ready} loading={scoring.actioning === "prepare-scoring"} onClick={() => void scoring.prepareStart()}>开始评分</Button>;
}

interface ScoringActionsProps extends ControllerProps {
  canManageTasks: boolean;
  calibrationQuestionId: string;
  onOpenGoldPapers: () => void;
  onOpenCalibration: (questionId: string) => void;
}

function ScoringOverviewActions({ scoring, canManageTasks, calibrationQuestionId, onOpenGoldPapers, onOpenCalibration }: Readonly<ScoringActionsProps>) {
  const run = scoring.summary?.run;
  const canCancel = run && ["queued", "processing", "needs_review", "failed", "cancelling"].includes(run.status);
  const cancellationTitle = run?.status === "cancelling" ? "继续完成取消？" : "取消本次评分？";
  const cancellationLabel = run?.status === "cancelling" ? "继续取消" : "取消评分";
  return <Space wrap>
    <Button icon={<RefreshCw size={15} />} loading={scoring.loading} onClick={() => void scoring.loadSummary()}>刷新</Button>
    <Button type={run ? "primary" : "default"} icon={<Eye size={15} />} loading={scoring.actioning === "scoring-detail"} onClick={() => void scoring.showDetail()}>逐卷查看</Button>
    {canManageTasks ? <Button icon={<Award size={15} />} onClick={onOpenGoldPapers}>标准卷</Button> : null}
    {canManageTasks && calibrationQuestionId ? <Button icon={<BadgeCheck size={15} />} onClick={() => onOpenCalibration(calibrationQuestionId)}>阅卷校准</Button> : null}
    {run && run.failed_count > 0 ? <Button icon={<RotateCcw size={15} />} loading={scoring.actioning === "retry-scoring"} onClick={() => void scoring.retry()}>重新处理失败项</Button> : null}
    {canCancel ? <Popconfirm title={cancellationTitle} description="未完成的规则、AI 建议和人工任务将停止，已保留的历史结果不会删除。" okText={cancellationLabel} cancelText="保留" okButtonProps={{ danger: true }} onConfirm={() => void scoring.cancel()}><Button danger icon={<CircleStop size={15} />} loading={scoring.actioning === "cancel-scoring"}>{cancellationLabel}</Button></Popconfirm> : null}
    <ScoringStartAction scoring={scoring} />
  </Space>;
}

function readinessPresentation(scoring: ExamScoringController) {
  if (scoring.hasUnresolvedRun) return {
    type: "info" as const,
    message: "已有评分任务，不能重复启动",
    checks: scoring.blockingChecks.filter((check) => check.code === "active_run_clear")
  };
  const type = scoring.readiness?.ready ? "success" as const : "error" as const;
  const message = scoring.readiness?.ready ? "评分启动条件已满足" : `${scoring.blockingChecks.length} 项条件阻止启动评分`;
  return { type, message, checks: [...scoring.blockingChecks, ...scoring.warningChecks] };
}

function ScoringReadiness({ scoring }: Readonly<ControllerProps>) {
  const readiness = scoring.readiness;
  if (!readiness) return null;
  const presentation = readinessPresentation(scoring);
  return <Alert className="grading-readiness-alert" type={presentation.type} showIcon message={presentation.message} description={
    <div className="grading-readiness-content">
      <div className="grading-readiness-counts">
        <span>题目 <strong>{readiness.total_questions}</strong></span>
        <span>题块 <strong>{readiness.ready_segments}/{readiness.total_segments}</strong></span>
        <span>规则自动 <strong>{readiness.automatic_candidates}</strong></span>
        <span>人工任务 <strong>{readiness.manual_review_candidates}</strong></span>
      </div>
      {presentation.checks.length ? <ul className="grading-readiness-issues">{presentation.checks.map((check) => <li key={check.code} className={check.severity}><strong>{getSafeUserText(check.label, "评分准备检查")}</strong><span>{getSafeUserText(check.message, "检查未通过，请完成相关设置")}</span></li>)}</ul> : <span className="grading-readiness-ok">全部题块均可进入规则处理或人工复核；AI 建议需另通过准入检查。</span>}
    </div>
  } />;
}

function scoringRunTone(status: string): StatusTone {
  if (status === "completed") return "success";
  if (status === "failed") return "danger";
  if (status === "needs_review") return "warning";
  return "processing";
}

function ScoringRunProgress({ scoring }: Readonly<ControllerProps>) {
  const run = scoring.summary?.run;
  if (!run) return <Alert type="info" showIcon message="尚未开始评分" description="请先完成答题卡上传与题目切分；启动前会展示每题的规则、AI 建议和人工路线。" />;
  return <div className="grading-run-strip">
    <StatusTag tone={scoringRunTone(run.status)}>{scoringRunStatusLabels[run.status] ?? "处理中"}</StatusTag>
    <span>总计 <strong>{run.total_count}</strong></span><span>处理中 <strong>{run.queued_count}</strong></span>
    <span>自动确认 <strong>{run.auto_confirmed_count}</strong></span><span>人工完成 <strong>{run.human_confirmed_count}</strong></span>
    <span>待人工 <strong>{run.review_count}</strong></span><span>失败 <strong>{run.failed_count}</strong></span>
  </div>;
}

function ScoringAIProgress({ scoring }: Readonly<ControllerProps>) {
  const progress = scoring.aiProgress;
  if (!progress) return null;
  const percent = progress.total ? Math.round((progress.succeeded + progress.failed + progress.cancelled) / progress.total * 100) : 0;
  return <div className="grading-ai-progress"><div><Sparkles size={17} /><strong>AI 辅助建议</strong><span>{progress.succeeded}/{progress.total} 已完成</span><span>{progress.queued + progress.processing} 处理中</span><span>{progress.failed} 累计失败（含重试历史）</span>{progress.cancelled ? <span>{progress.cancelled} 已取消</span> : null}</div><Progress percent={percent} size="small" status={progress.failed ? "exception" : undefined} /></div>;
}

function ScoringAIIssues({ scoring }: Readonly<ControllerProps>) {
  // 取消过程仍可续取消，但不能重开 AI 入队；两种按钮的允许状态有意不同。
  const canRetry = scoring.summary?.run && ["queued", "processing", "needs_review", "failed"].includes(scoring.summary.run.status);
  return <>
    {scoring.retryableAIBatches.length ? <Alert type="warning" showIcon message={`${scoring.retryableAIBatches.length} 个 AI 批次包含失败建议；人工阅卷可继续`} action={canRetry ? <Button size="small" loading={scoring.actioning === "retry-failed-ai"} onClick={() => void scoring.retryFailedAI()}>重试失败的 AI 建议</Button> : undefined} /> : null}
    {scoring.aiIssue ? <Alert type="warning" showIcon message={scoring.aiIssue} action={canRetry ? <Button size="small" loading={scoring.actioning === "enqueue-ai"} onClick={() => void scoring.retryAI()}>重试 AI 入队</Button> : undefined} /> : null}
  </>;
}

export function ExamScoringOverview(props: Readonly<ScoringActionsProps>) {
  const { scoring } = props;
  return <section className="grading-overview">
    <div className="grading-overview-head">
      <div><h2>评分与 AI 辅助</h2><p>规则题自动判分，主观题按冻结策略生成 AI 建议或进入人工；最终成绩由教师确认。</p></div>
      <ScoringOverviewActions {...props} />
    </div>
    <ScoringReadiness scoring={scoring} />
    <ScoringRunProgress scoring={scoring} />
    <ScoringAIProgress scoring={scoring} />
    <ScoringAIIssues scoring={scoring} />
    <ResponsiveTable className="dense-data-table" size="small" pagination={false} loading={scoring.loading} rowKey="question_id" dataSource={scoring.summary?.questions ?? []} columns={[
      { title: "题号", dataIndex: "question_no", width: 90 },
      { title: "题型", dataIndex: "question_type", width: 140, render: (value: string) => <span title={value}>{questionTypeLabels[value] ?? "其他题型"}</span> },
      { title: "答卷", dataIndex: "total", width: 80 }, { title: "处理中", dataIndex: "queued", width: 90 },
      { title: "已确认", dataIndex: "confirmed", width: 90 }, { title: "待人工", dataIndex: "review", width: 90 }, { title: "失败", dataIndex: "failed", width: 80 }
    ]} />
  </section>;
}
