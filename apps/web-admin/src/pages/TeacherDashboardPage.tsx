import { useCallback, useEffect, useMemo, useState } from "react";
import { Alert, Button } from "antd";
import { ArrowRight, BookOpenCheck, Gavel, RefreshCw } from "lucide-react";
import { getSafeUserText, getUserErrorMessage } from "../api/client";
import { listArbitrationTasks, listReviewTasks, type ArbitrationTask, type ReviewTask } from "../api/review";
import { hasAnyPermission, hasEveryPermission, type SessionUser } from "../auth/session";
import { EmptyState, ErrorState, LoadingState } from "../components/PageState";
import { StatusTag } from "../components/StatusTag";
import type { StatusTone } from "../types";
import { workspaceForExperience } from "../workspaces/registry";
import { examSubjectLabel } from "../constants/examStatus";

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

const activeReviewStatuses = new Set(["assigned", "in_progress", "returned"]);
const activeArbitrationStatuses = new Set(["assigned", "in_progress", "pending"]);

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

function reviewTaskDetail(task: ReviewTask) {
  // 摘要沿用任务响应的考试名称，缺项才用短编号兜底；不额外读取整场考试目录。
  const exam = task.exam_name || (task.exam_id ? `考试 ${task.exam_id.slice(0, 8)}` : "考试名称未提供");
  const subject = task.subject ? examSubjectLabel(task.subject) : "学科未标注";
  const source = sourceLabels[task.source];
  const sourceSuffix = source ? ` · ${source}` : "";
  return `${exam} · ${subject} · 密号 ${task.anonymous_code}${sourceSuffix}`;
}

function TeacherWorkSummary({ canReview, canArbitrate, warnings, activeReviewCount, activeArbitrationCount, completedReviewCount }: Readonly<{
  canReview: boolean;
  canArbitrate: boolean;
  warnings?: string[];
  activeReviewCount: number;
  activeArbitrationCount: number;
  completedReviewCount: number;
}>) {
  if (!canReview && !canArbitrate) return null;
  // 接口降级后的空数组不代表零任务，读取失败的类别显示缺失标记。
  const reviewUnavailable = warnings?.includes("阅卷任务暂时不可用");
  const arbitrationUnavailable = warnings?.includes("仲裁任务暂时不可用");
  const reviewCount = reviewUnavailable ? "—" : activeReviewCount;
  const arbitrationCount = arbitrationUnavailable ? "—" : activeArbitrationCount;
  const completedCount = reviewUnavailable ? "—" : completedReviewCount;
  return (
    <section className="teacher-summary" aria-label="已加载的工作摘要">
      {canReview ? <div><span>已加载待阅卷</span><strong>{reviewCount}</strong></div> : null}
      {canArbitrate ? <div><span>已加载待仲裁</span><strong>{arbitrationCount}</strong></div> : null}
      {canReview ? <div><span>已加载已提交</span><strong>{completedCount}</strong></div> : null}
    </section>
  );
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

export function TeacherDashboardPage({ user, onNavigate }: Readonly<{ user: SessionUser; onNavigate: (path: string) => void }>) {
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

  const activeReviews = useMemo(() => (data?.reviewTasks ?? []).filter((task) => activeReviewStatuses.has(task.status)), [data?.reviewTasks]);
  const activeArbitrations = useMemo(() => (data?.arbitrationTasks ?? []).filter((task) => activeArbitrationStatuses.has(task.status)), [data?.arbitrationTasks]);
  const completedReviews = (data?.reviewTasks ?? []).filter((task) => ["submitted", "completed"].includes(task.status)).length;
  const canReview = hasAnyPermission(user, ["review:manage", "review:work"]);
  const canArbitrate = hasAnyPermission(user, ["arbitration:manage", "arbitration:work"]);
  const learningLinks = [
    { title: "题库", path: "/question-bank", visible: hasEveryPermission(user, ["question_bank:read"]) },
    { title: "班级成绩", path: "/reports", visible: hasEveryPermission(user, ["report:read"]) },
    { title: "学生反馈", path: "/appeals", visible: hasEveryPermission(user, ["appeal:read"]) },
  ].filter((link) => link.visible);

  const tasks = useMemo<PersonalTask[]>(() => [
    ...activeReviews.map((task) => ({
      id: task.id,
      kind: "review" as const,
      title: `第 ${task.question_no} 题阅卷`,
      detail: reviewTaskDetail(task),
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

  const workspace = workspaceForExperience(user, "teacher");
  let pageTitle = "我的工作";
  if (workspace === "arbitrator") {
    pageTitle = "我的仲裁";
  } else if (workspace === "grader") {
    pageTitle = "我的阅卷";
  }
  if (!data && loading) return <LoadingState label="正在加载我的工作" />;
  if (!data && error) return <ErrorState message={error} onRetry={() => void load()} />;
  const emptyTaskState = canReview || canArbitrate ? <EmptyState title="当前没有已加载的待办" description="任务分配后会显示在这里；如果任务区加载失败，请重试。" /> : null;

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

      <TeacherWorkSummary canReview={canReview} canArbitrate={canArbitrate} warnings={data?.warnings} activeReviewCount={activeReviews.length} activeArbitrationCount={activeArbitrations.length} completedReviewCount={completedReviews} />

      {tasks.length ? <section className="teacher-task-pane">
        <div className="section-head"><div><h2>当前任务</h2><p>分别查看已分配的阅卷与仲裁；数量以当前已加载任务为准。</p></div></div>
        <div className="teacher-task-list">{[...tasks.filter((task) => task.kind === "arbitration").slice(0, 4), ...tasks.filter((task) => task.kind === "review").slice(0, 4)].map((task) => (
          <button type="button" key={`${task.kind}-${task.id}`} onClick={() => onNavigate(task.path)}>
            <span className="teacher-task-icon">{task.kind === "review" ? <BookOpenCheck size={17} /> : <Gavel size={17} />}</span>
            <span><strong>{task.title}</strong><small>{task.detail}</small></span>
            <StatusTag tone={taskTone(task.status)}>{taskStatusLabels[task.status] ?? "处理中"}</StatusTag>
            <ArrowRight size={16} />
          </button>
        ))}</div>
        <div className="teacher-task-more">{canReview ? <Button type="link" onClick={() => onNavigate("/grading")}>查看全部阅卷任务</Button> : null}{canArbitrate ? <Button type="link" onClick={() => onNavigate("/arbitration")}>查看全部仲裁任务</Button> : null}</div>
      </section> : emptyTaskState}

      {learningLinks.length ? <section className="teacher-learning-links"><h2>教学工作</h2><div>{learningLinks.map((link) => <Button key={link.path} onClick={() => onNavigate(link.path)}>{link.title}<ArrowRight size={15} /></Button>)}</div></section> : null}
    </div>
  );
}
