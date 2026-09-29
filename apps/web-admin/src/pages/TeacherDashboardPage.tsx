import { useCallback, useEffect, useMemo, useState } from "react";
import { Alert, Button, Progress } from "antd";
import { ArrowRight, BookOpenCheck, Gavel, RefreshCw } from "lucide-react";
import { getSafeUserText, getUserErrorMessage } from "../api/client";
import { listArbitrationTasks, listReviewTasks, type ArbitrationTask, type ReviewTask } from "../api/review";
import { hasAnyPermission, type SessionUser } from "../auth/session";
import { EmptyState, ErrorState, LoadingState } from "../components/PageState";
import { StatusTag } from "../components/StatusTag";
import type { StatusTone } from "../types";
import { workspaceForExperience } from "../workspaces/registry";

interface TeacherHomeData {
  reviewTasks: ReviewTask[];
  arbitrationTasks: ArbitrationTask[];
  warnings: string[];
}

interface PersonalTask {
  id: string;
  kind: "review" | "arbitration";
  title: string;
  detail: string;
  status: string;
  path: string;
}

const activeReviewStatuses = ["assigned", "in_progress", "returned"];
const activeArbitrationStatuses = ["assigned", "in_progress", "pending"];

const taskStatusLabels: Record<string, string> = {
  assigned: "待阅卷",
  in_progress: "处理中",
  returned: "已退回",
  pending: "待领取",
  submitted: "已提交",
  completed: "已完成"
};

const sourceLabels: Record<string, string> = {
  ai_low_confidence: "智能评分待人工确认",
  double_mark_required: "双评任务",
  evidence_verification_failed: "证据核验未通过",
  manual_sample: "人工抽检",
  returned: "退回重评"
};

function taskTone(status: string): StatusTone {
  if (["submitted", "completed"].includes(status)) return "success";
  if (["returned", "pending"].includes(status)) return "warning";
  if (["assigned", "in_progress"].includes(status)) return "processing";
  return "neutral";
}

async function fetchTeacherHome(user: SessionUser): Promise<TeacherHomeData> {
  const canReview = hasAnyPermission(user, ["review:manage", "review:work"]);
  const canArbitrate = hasAnyPermission(user, ["arbitration:manage", "arbitration:work"]);
  // 阅卷和仲裁分别降级，某一接口失败时仍展示另一类已分派任务并提示缺失。
  const results = await Promise.allSettled([
    canReview ? listReviewTasks({ assigned_to: user.id }) : Promise.resolve({ tasks: [] }),
    canArbitrate ? listArbitrationTasks({ assigned_to: user.id }) : Promise.resolve({ arbitration_tasks: [] })
  ]);
  const warnings: string[] = [];
  if (results[0].status === "rejected") warnings.push("阅卷任务暂时不可用");
  if (results[1].status === "rejected") warnings.push("仲裁任务暂时不可用");
  return {
    reviewTasks: results[0].status === "fulfilled" ? results[0].value.tasks : [],
    arbitrationTasks: results[1].status === "fulfilled" ? results[1].value.arbitration_tasks : [],
    warnings
  };
}

export function TeacherDashboardPage({ user, onNavigate }: { user: SessionUser; onNavigate: (path: string) => void }) {
  const [data, setData] = useState<TeacherHomeData>();
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string>();

  const load = useCallback(async () => {
    setLoading(true);
    setError(undefined);
    try {
      setData(await fetchTeacherHome(user));
    } catch (loadError) {
      setError(getUserErrorMessage(loadError, "教师工作台加载失败"));
    } finally {
      setLoading(false);
    }
  }, [user]);

  useEffect(() => {
    void load();
  }, [load]);

  const activeReviews = useMemo(() => (data?.reviewTasks ?? []).filter((task) => activeReviewStatuses.includes(task.status)), [data?.reviewTasks]);
  const activeArbitrations = useMemo(() => (data?.arbitrationTasks ?? []).filter((task) => activeArbitrationStatuses.includes(task.status)), [data?.arbitrationTasks]);
  const completedReviews = (data?.reviewTasks ?? []).filter((task) => ["submitted", "completed"].includes(task.status)).length;
  const reviewTotal = data?.reviewTasks.length ?? 0;
  const reviewProgress = reviewTotal > 0 ? Math.round((completedReviews / reviewTotal) * 100) : 0;

  const tasks = useMemo<PersonalTask[]>(() => [
    ...activeReviews.map((task) => ({
      id: task.id,
      kind: "review" as const,
      title: `第 ${task.question_no} 题阅卷`,
      detail: sourceLabels[task.source] ? `密号 ${task.anonymous_code} · ${sourceLabels[task.source]}` : `密号 ${task.anonymous_code}`,
      status: task.status,
      path: `/grading?task=${encodeURIComponent(task.id)}`
    })),
    ...activeArbitrations.map((task) => ({
      id: task.id,
      kind: "arbitration" as const,
      title: `第 ${task.question_no} 题仲裁`,
      detail: `密号 ${task.anonymous_code} · 分差 ${task.score_difference}`,
      status: task.status,
      path: `/arbitration?task=${encodeURIComponent(task.id)}`
    }))
  ], [activeArbitrations, activeReviews]);

  const primary = tasks[0];
  const workspace = workspaceForExperience(user, "teacher");
  const pageTitle = workspace === "arbitrator" ? "我的仲裁" : workspace === "grader" ? "我的阅卷" : "我的工作";
  if (!data && loading) return <LoadingState label="正在加载我的工作" />;
  if (!data && error) return <ErrorState message={error} onRetry={() => void load()} />;

  return (
    <div className="page-stack teacher-dashboard">
      <section className="teacher-heading">
        <div>
          <h1>{pageTitle}</h1>
        </div>
        <Button icon={<RefreshCw size={16} />} loading={loading} onClick={() => void load()}>刷新</Button>
      </section>

      {error ? <Alert type="error" showIcon message="刷新失败" description={error} /> : null}
      {data?.warnings.map((warning) => <Alert type="warning" showIcon key={warning} message={getSafeUserText(warning, "部分工作台数据暂时不可用")} />)}

      <section className="teacher-summary" aria-label="我的工作摘要">
        <div><span>待阅卷</span><strong>{activeReviews.length}</strong></div>
        <div><span>待仲裁</span><strong>{activeArbitrations.length}</strong></div>
        <div><span>已完成</span><strong>{completedReviews}</strong></div>
      </section>

      <section className="reviewer-progress-panel" aria-label="我的阅卷进度">
        <div className="reviewer-progress-head">
          <div>
            <h2>我的阅卷进度</h2>
          </div>
          <strong>已提交 {completedReviews} / 共 {reviewTotal} 题</strong>
        </div>
        <Progress percent={reviewProgress} status={reviewProgress === 100 ? "success" : "active"} />
      </section>

      {primary ? (
        <section className="teacher-next-action">
          <div>
            <span>下一项工作</span>
            <h2>{primary.title}</h2>
            <p>{primary.detail}</p>
          </div>
          <Button type="primary" size="large" onClick={() => onNavigate(primary.path)}>开始处理<ArrowRight size={17} /></Button>
        </section>
      ) : null}

      <section className="teacher-task-pane">
        <div className="section-head"><div><h2>任务</h2></div><strong>{tasks.length}</strong></div>
        {tasks.length ? <div className="teacher-task-list">{tasks.slice(0, 8).map((task) => (
          <button type="button" key={`${task.kind}-${task.id}`} onClick={() => onNavigate(task.path)}>
            <span className="teacher-task-icon">{task.kind === "review" ? <BookOpenCheck size={17} /> : <Gavel size={17} />}</span>
            <span><strong>{task.title}</strong><small>{task.detail}</small></span>
            <StatusTag tone={taskTone(task.status)}>{taskStatusLabels[task.status] ?? "处理中"}</StatusTag>
            <ArrowRight size={16} />
          </button>
        ))}</div> : <EmptyState title="暂无任务" description="任务分配后会显示在这里。" />}
      </section>
    </div>
  );
}
