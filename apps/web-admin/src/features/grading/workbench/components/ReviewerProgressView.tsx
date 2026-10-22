import { Button, Progress } from "antd";
import { RefreshCw } from "lucide-react";
import { ErrorState } from "../../../../components/PageState";
import type { ReviewerProgress } from "../gradingWorkbench.types";

interface ReviewerProgressViewProps {
  reviewers: ReviewerProgress[];
  progressComplete: boolean;
  loading: boolean;
  error: string | null;
  gradersError: string | null;
  onRefresh: () => Promise<void>;
}

export function ReviewerProgressView({ reviewers, progressComplete, loading, error, gradersError, onRefresh }: Readonly<ReviewerProgressViewProps>) {
  return <section className="grading-reviewer-workspace" aria-label="教师进度">
    <header className="grading-focus-heading"><div><h1>教师进度</h1><p>查看当前任务范围内，每位教师已完成和待完成的阅卷数量。</p></div>
      <Button icon={<RefreshCw size={15} />} loading={loading} onClick={() => void onRefresh()}>刷新</Button>
    </header>
    {gradersError ? <ErrorState message={gradersError} /> : null}
    {error ? <ErrorState message={error} onRetry={() => void onRefresh()} /> : null}
    {/* 汇总尚未成功返回时，零值不能表示教师已无待办。 */}
    {!progressComplete ? <p className="muted" role="status">{loading ? "正在读取教师进度…" : "教师进度尚未完整加载，请刷新后核对。"}{reviewers.length ? " 以下仅显示当前已读取的数据。" : ""}</p> : null}
    <div className="grading-reviewer-list">
      {reviewers.length ? reviewers.map(reviewer => <div className="grading-reviewer-row" key={reviewer.id}>
        <div><strong>{reviewer.name}</strong><span>{reviewer.total ? `已完成 ${reviewer.completed} / ${reviewer.total} 项` : progressComplete ? "尚未安排任务" : "暂无已读取的任务"}</span></div>
        <Progress percent={reviewer.percent} showInfo={false} status={reviewer.percent === 100 ? "success" : "normal"} />
        <span>待完成 {Math.max(0, reviewer.total - reviewer.completed)} 项</span><strong>{reviewer.percent}%</strong>
      </div>) : progressComplete ? <p className="muted">暂无阅卷教师，请先在“安排教师”中分配任务。</p> : null}
    </div>
  </section>;
}
