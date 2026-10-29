import { useCallback, useEffect, useMemo, useRef, useState } from "react";
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
import { QuestionBankHandoffs } from "../features/question-bank/QuestionBankHandoffs";

interface TeacherHomeData {
  reviewTasks: ReviewTask[];
  reviewHistory: ReviewTask[];
  historyCount: number;
  historyCursor?: string;
  historyHasMore: boolean;
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
  const completedCount = warnings?.includes("历史评阅暂时不可用") ? "—" : completedReviewCount;
  return (
    <section className="teacher-summary" aria-label="工作摘要">
      {canReview ? <div><span>待阅卷</span><strong>{reviewCount}</strong></div> : null}
      {canArbitrate ? <div><span>待仲裁</span><strong>{arbitrationCount}</strong></div> : null}
      {canReview ? <div><span>已提交</span><strong>{completedCount}</strong></div> : null}
    </section>
  );
}

async function fetchTeacherHome(user: SessionUser): Promise<TeacherHomeData> {
  const canReview = hasAnyPermission(user, ["review:manage", "review:work"]);
  const canArbitrate = hasAnyPermission(user, ["arbitration:manage", "arbitration:work"]);
  // 阅卷和仲裁分别降级，某一接口失败时仍展示另一类已分派任务并提示缺失。
  const results = await Promise.allSettled([
    canReview ? listReviewTasks({ assigned_to: user.id }) : Promise.resolve({ tasks: [] }),
    canArbitrate ? listArbitrationTasks({ assigned_to: user.id }) : Promise.resolve({ arbitration_tasks: [] }),
    // 历史单独按状态分页，不能从待办首屏推导，否则完成全部任务后会无记录可看。
    canReview ? listReviewTasks({ assigned_to: user.id, status: "reviewed", limit: 10 }) : Promise.resolve({ tasks: [], has_more: false, next_cursor: undefined, aggregate: undefined })
  ]);
  const warnings: string[] = [];
  if (results[0].status === "rejected") warnings.push("阅卷任务暂时不可用");
  if (results[1].status === "rejected") warnings.push("仲裁任务暂时不可用");
  if (results[2].status === "rejected") warnings.push("历史评阅暂时不可用");
  const history = results[2].status === "fulfilled" ? results[2].value : undefined;
  const reviewHistory = (history?.tasks ?? []).filter(task => ["submitted", "completed"].includes(task.status));
  return {
    reviewTasks: results[0].status === "fulfilled" ? results[0].value.tasks : [],
    reviewHistory,
    historyCount: history?.aggregate?.total_count ?? reviewHistory.length,
    historyCursor: history?.next_cursor,
    historyHasMore: Boolean(history?.has_more),
    arbitrationTasks: results[1].status === "fulfilled" ? results[1].value.arbitration_tasks : [],
    warnings
  };
}

export function TeacherDashboardPage({ user, onNavigate }: Readonly<{ user: SessionUser; onNavigate: (path: string) => void }>) {
  // 切换账号时重建本人的列表和游标，旧账号的分页回执不能进入新页面。
  return <TeacherDashboardContent key={JSON.stringify([user.tenant, user.id])} user={user} onNavigate={onNavigate} />;
}

function TeacherDashboardContent({ user, onNavigate }: Readonly<{ user: SessionUser; onNavigate: (path: string) => void }>) {
  const [data, setData] = useState<TeacherHomeData>();
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string>();
  const [historyLoading, setHistoryLoading] = useState(false);
  const [historyError, setHistoryError] = useState<string>();
  const requestVersion = useRef(0);
  const historyPending = useRef(false);

  const load = useCallback(async () => {
    const version = ++requestVersion.current;
    setLoading(true);
    setError(undefined);
    setHistoryError(undefined);
    setHistoryLoading(false);
    historyPending.current = false;
    try {
      const result = await fetchTeacherHome(user);
      if (version === requestVersion.current) setData(result);
    } catch (loadError) {
      if (version === requestVersion.current) setError(getUserErrorMessage(loadError, "教师工作台加载失败"));
    } finally {
      if (version === requestVersion.current) setLoading(false);
    }
  }, [user]);

  useEffect(() => {
    void load();
    return () => { requestVersion.current += 1; };
  }, [load]);

  const loadMoreHistory = async () => {
    if (loading || historyPending.current || !data?.historyHasMore) return;
    if (!data.historyCursor) { setHistoryError("历史记录分页位置缺失，请刷新后重试"); return; }
    const cursor = data.historyCursor;
    const version = requestVersion.current;
    historyPending.current = true;
    setHistoryLoading(true);
    setHistoryError(undefined);
    try {
      const page = await listReviewTasks({ assigned_to: user.id, status: "reviewed", limit: 10, cursor });
      if (version !== requestVersion.current) return;
      if (page.has_more && (!page.next_cursor || page.next_cursor === cursor)) throw new Error("历史记录分页位置未推进");
      setData(current => current ? { ...current,
        reviewHistory: [...new Map([...current.reviewHistory, ...page.tasks.filter(task => ["submitted", "completed"].includes(task.status))].map(task => [task.id, task])).values()],
        historyCount: page.aggregate?.total_count ?? current.historyCount,
        historyCursor: page.next_cursor, historyHasMore: Boolean(page.has_more)
      } : current);
    } catch (loadError) {
      if (version === requestVersion.current) setHistoryError(getUserErrorMessage(loadError, "历史评阅加载失败，请重试"));
    } finally {
      if (version === requestVersion.current) { historyPending.current = false; setHistoryLoading(false); }
    }
  };

  const activeReviews = useMemo(() => (data?.reviewTasks ?? []).filter((task) => activeReviewStatuses.has(task.status)), [data?.reviewTasks]);
  const activeArbitrations = useMemo(() => (data?.arbitrationTasks ?? []).filter((task) => activeArbitrationStatuses.has(task.status)), [data?.arbitrationTasks]);
  const completedReviews = data?.historyCount ?? 0;
  const canReview = hasAnyPermission(user, ["review:manage", "review:work"]);
  const canArbitrate = hasAnyPermission(user, ["arbitration:manage", "arbitration:work"]);
  const learningLinks = [
    { title: "题库", path: "/question-bank", visible: hasEveryPermission(user, ["question_bank:read"]) },
    { title: "班级成绩", path: "/reports", visible: hasEveryPermission(user, ["report:read"]) },
    { title: "成绩复核", path: "/appeals", visible: hasEveryPermission(user, ["appeal:read"]) },
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
  // 降级空数组不能推导为没有待办；失败原因由上方警告和刷新动作承接。
  const emptyTaskState = (canReview || canArbitrate) && !data?.warnings.length && !error ? <EmptyState title="当前暂无待办" description="任务分配后会显示在这里。" /> : null;

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
      {canReview || canArbitrate ? <p className="muted">摘要仅统计当前已读取任务。完整任务请进入阅卷或仲裁列表。</p> : null}

      {tasks.length ? <section className="teacher-task-pane">
        <div className="section-head"><div><h2>当前任务</h2></div></div>
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

      {canReview ? <section className="teacher-task-pane" aria-label="历史评阅">
        <div className="section-head"><h2>历史评阅</h2><Button type="link" onClick={() => onNavigate("/grading?status=reviewed")}>查看全部历史评阅</Button></div>
        {data?.reviewHistory.length ? <div className="teacher-task-list">{data.reviewHistory.map(task => (
          <button type="button" key={task.id} onClick={() => onNavigate(`/grading?status=reviewed&task=${encodeURIComponent(task.id)}`)}>
            <span className="teacher-task-icon"><BookOpenCheck size={17} /></span>
            <span><strong>第 {task.question_no} 题{task.source === "omr_ambiguous" ? "客观题核验记录" : "评阅记录"}</strong></span>
            <StatusTag tone="success">{taskStatusLabels[task.status]}</StatusTag><ArrowRight size={16} />
          </button>
        ))}</div> : !data?.warnings.includes("历史评阅暂时不可用") ? <p className="muted">暂无历史评阅记录</p> : null}
        {historyError ? <Alert type="error" showIcon message={historyError} /> : null}
        {data?.historyHasMore ? <div className="teacher-task-more"><Button loading={historyLoading} disabled={loading} onClick={() => void loadMoreHistory()}>加载更多历史记录</Button></div> : null}
      </section> : null}

      {learningLinks.length ? <section className="teacher-learning-links"><h2>教学工作</h2><div>{learningLinks.map((link) => <Button key={link.path} onClick={() => onNavigate(link.path)}>{link.title}<ArrowRight size={15} /></Button>)}</div></section> : null}
      <QuestionBankHandoffs key={`${user.tenant}:${user.id}`} user={user} onNavigate={onNavigate} />
    </div>
  );
}
