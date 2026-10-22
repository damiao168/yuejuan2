import { useState, type ReactNode } from "react";
import { Alert, Button, Popconfirm, Space } from "antd";
import { CircleStop, Eye, Play, RefreshCw, RotateCcw } from "lucide-react";
import { getSafeUserText } from "../../../../api/client";
import { ResponsiveTable } from "../../../../components/ResponsiveTable";
import { questionTypeLabels } from "../gradingWorkbench.model";
import type { ExamScoringController } from "../hooks/useExamScoring";
import { scoringReviewCounts, type ScoringReviewFilter } from "../scoringReviewCategories";

interface ScoringActionsProps {
  scoring: ExamScoringController;
  canManageTasks: boolean;
  onOpenTrial?: () => void;
  fastConfirm?: ReactNode;
  onOpenAssignments?: () => void;
  onOpenResults?: (filter?: ScoringReviewFilter) => void;
}

const reviewActions = [
  { filter: "choice", label: "选择题核对", action: "核对选项" },
  { filter: "suggestion", label: "建议分确认", action: "确认建议分" },
  { filter: "recognition", label: "识别不确定", action: "查看原图" },
  { filter: "manual", label: "人工评分", action: "人工评分" }
] as const;

function StartAction({ scoring }: { scoring: ExamScoringController }) {
  // 未知结果的启动命令必须继续确认同一个命令，不能因界面精简而重复开新运行。
  if (scoring.pendingCommand) return <Button type="primary" loading={scoring.actioning === "start-scoring"} onClick={() => void scoring.start()}>继续确认评分</Button>;
  if (scoring.hasUnresolvedRun) return null;
  return <Button type="primary" icon={<Play size={15} />} disabled={!scoring.readiness?.ready} loading={scoring.actioning === "prepare-scoring"} onClick={() => void scoring.prepareStart()}>{scoring.summary?.run ? "重新评分" : "开始评分"}</Button>;
}

export function ExamScoringOverview({ scoring, canManageTasks, fastConfirm, onOpenAssignments, onOpenResults }: Readonly<ScoringActionsProps>) {
  const run = scoring.summary?.run;
  const [showQuestions, setShowQuestions] = useState(false);
  const questions = scoring.summary?.questions ?? [];
  const reviewCounts = scoringReviewCounts(scoring.runDetail?.items ?? []);
  const classifiedReview = Object.values(reviewCounts).reduce((total, count) => total + count, 0);
  // 汇总没有区分建议分与识别异常；明细未完整或刷新尚未对齐时只显示总数，不能用差额猜分类。
  const hasCompleteDetail = Boolean(run && scoring.runDetail && scoring.runDetail.items.length === run.total_count && classifiedReview === run.review_count);
  const confirmed = (run?.auto_confirmed_count ?? 0) + (run?.human_confirmed_count ?? 0);
  const checks = [...scoring.blockingChecks, ...scoring.warningChecks];
  const canCancel = canManageTasks && run && ["queued", "processing", "needs_review", "failed", "cancelling"].includes(run.status);
  const pending = (run?.review_count ?? 0) + (run?.queued_count ?? 0) + (run?.failed_count ?? 0);
  const statusText = !run ? "" : run.status === "cancelled" ? "本次评分已取消" : run.status === "cancelling" ? "正在取消本次评分" : [run.failed_count ? `${run.failed_count} 道题处理失败` : "", run.queued_count ? `${run.queued_count} 道题正在处理` : ""].filter(Boolean).join(" · ");
  const openResults = (filter: ScoringReviewFilter = "all") => { if (onOpenResults) onOpenResults(filter); else void scoring.showDetail(); };
  return <section className="grading-overview grading-focus-overview">
    <header className="grading-focus-heading"><div><h1>阅卷概览</h1><p>{run ? `共 ${run.total_count} 道题 · 已确认 ${confirmed} · 待处理 ${pending}` : "系统按题目配置识别作答并生成评分结果。"}</p></div>
      <Button type="text" icon={<RefreshCw size={15} />} loading={scoring.loading} onClick={() => void scoring.loadSummary()}>刷新</Button>
    </header>
    {run ? <>
      {statusText ? <p role="status">{statusText}</p> : null}
      {run.review_count > 0 ? <div className="grading-next-actions" aria-label="待确认事项">
        {hasCompleteDetail ? reviewActions.filter(item => reviewCounts[item.filter] > 0).map(item => <div key={item.filter}>
          <strong>{item.label}</strong><div className="grading-next-action-end"><span>{reviewCounts[item.filter]} 道题</span><Button loading={scoring.actioning === "scoring-detail"} onClick={() => openResults(item.filter)}>{item.action}</Button></div>
        </div>) : <div><strong>待确认</strong><div className="grading-next-action-end"><span>{run.review_count} 道题</span><Button icon={<Eye size={15} />} loading={scoring.actioning === "scoring-detail"} onClick={() => openResults()}>查看答卷结果</Button></div></div>}
      </div> : null}
      <div className="grading-focus-actions">
        {onOpenAssignments && run.review_count > 0 ? <Button type="primary" onClick={onOpenAssignments}>安排阅卷教师</Button> : null}
        {run.review_count === 0 ? <Button icon={<Eye size={15} />} loading={scoring.actioning === "scoring-detail"} onClick={() => openResults()}>查看答卷结果</Button> : null}
        {run.failed_count > 0 ? <Button icon={<RotateCcw size={15} />} loading={scoring.actioning === "retry-scoring"} onClick={() => void scoring.retry()}>重新处理失败项</Button> : null}
        <StartAction scoring={scoring} />
      </div>
    </> : <div className="grading-focus-actions"><StartAction scoring={scoring} /></div>}
    {!run && checks.length ? <Alert type={scoring.blockingChecks.length ? "warning" : "info"} showIcon message="开始前请完成以下事项" description={<ul>{checks.map(check => <li key={check.code}>{getSafeUserText(check.message, "请完成相关准备")}</li>)}</ul>} /> : null}
    {fastConfirm}
    <div className="grading-overview-secondary">
      <Button type="text" aria-expanded={showQuestions} onClick={() => setShowQuestions(value => !value)}>各题进度</Button>
      {canCancel ? <details className="grading-options"><summary>更多操作</summary><Space wrap>
        <Popconfirm title={run.status === "cancelling" ? "继续完成取消？" : "取消本次评分？"} description="未完成任务将停止，已有历史结果保留。" okText="取消评分" cancelText="保留" onConfirm={() => void scoring.cancel()}><Button danger icon={<CircleStop size={15} />} loading={scoring.actioning === "cancel-scoring"}>{run.status === "cancelling" ? "继续取消" : "取消评分"}</Button></Popconfirm>
      </Space></details> : null}
    </div>
    {showQuestions ? <ResponsiveTable size="small" pagination={false} rowKey="question_id" dataSource={questions} columns={[
      { title: "题号", dataIndex: "question_no", width: 100 },
      { title: "题型", dataIndex: "question_type", render: (type: string) => questionTypeLabels[type] ?? "其他题型" },
      { title: "待确认", dataIndex: "review" }, { title: "已确认", dataIndex: "confirmed" },
      { title: "处理中", dataIndex: "queued" }, { title: "失败", dataIndex: "failed" }
    ]} /> : null}
  </section>;
}
