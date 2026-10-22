import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { Alert, App, Button, Drawer, Empty, Space, Tag } from "antd";
import { Check, Clock3, LoaderCircle, TriangleAlert } from "lucide-react";
import { cancelQuestionRubricDraftJob, createQuestionRubricDraftJob, getQuestionRubricDraftJob, listQuestionRubricDraftJobs, type Question, type QuestionRubricSegment, type RubricDraftJob } from "../../api/papers";
import { getUserErrorMessage } from "../../api/client";
import type { PaperRubricViewContext } from "./paperRubricViewTypes";
import "./paper-rubric-jobs.css";

export const rubricJobActive = (job?: RubricDraftJob) => Boolean(job && ["queued", "running", "cancelling"].includes(job.status));
export const rubricJobLabel: Record<RubricDraftJob["status"], string> = { queued: "排队中", running: "生成中", cancelling: "取消中", cancelled: "已取消", succeeded: "建议已就绪", failed: "生成失败", stale: "建议已失效" };

function canonical(value: unknown): unknown {
  if (Array.isArray(value)) return value.map(canonical);
  if (value && typeof value === "object") return Object.fromEntries(Object.entries(value).filter(([, item]) => item !== undefined).sort(([a], [b]) => a.localeCompare(b)).map(([key, item]) => [key, canonical(item)]));
  return value;
}
// 与后台来源快照比较同一组业务字段，忽略展示顺序及 JSON 对象属性顺序。
export function rubricJobQuestionVersion(question: Question) {
  return JSON.stringify(canonical({ id: question.id, tenant_id: question.tenant_id, exam_id: question.exam_id, question_no: question.question_no,
    question_type: question.question_type, score: question.score, stem: question.stem ?? "", options: question.options, knowledge_points: question.knowledge_points,
    paper_import_source_refs: question.paper_import_source_refs?.length ? question.paper_import_source_refs : undefined,
    answer_key: question.answer_key ?? null, solution: question.solution ?? null, rubric: question.rubric ?? null }));
}
export function rubricJobMatches(job: RubricDraftJob | undefined, question: Question) {
  return Boolean(job?.input.expected_question && rubricJobQuestionVersion(job.input.expected_question) === rubricJobQuestionVersion(question));
}

interface JobsContext {
  jobs: RubricDraftJob[];
  latest: Map<string, RubricDraftJob>;
  submitting: Set<string>;
  start: (question: Question, segments: QuestionRubricSegment[], regenerate?: boolean) => Promise<RubricDraftJob>;
  read: (id: string) => Promise<RubricDraftJob>;
  cancel: (id: string) => Promise<void>;
  show: () => void;
}
const Context = createContext<JobsContext | null>(null);
export function usePaperRubricJobs() { return useContext(Context); }

// 挂在考试页面，题目编辑器卸载时只停止界面订阅，后台任务继续执行。
// 所有迟到响应都核对页面归属，不能把旧考试或旧身份的结果放入新页面。
export function PaperRubricJobsProvider({ context, children }: Readonly<{ context: PaperRubricViewContext; children: ReactNode }>) {
  const examId = context.selectedExam?.id ?? "";
  const scope = `${context.selectedExam?.tenant_id}:${context.options.questionBankScope}:${examId}`;
  return <PaperRubricJobsSession key={scope} examId={examId} context={context}>{children}</PaperRubricJobsSession>;
}

function PaperRubricJobsSession({ examId, context, children }: Readonly<{ examId: string; context: PaperRubricViewContext; children: ReactNode }>) {
  const { message } = App.useApp();
  const [jobs, setJobs] = useState<RubricDraftJob[]>([]);
  const [error, setError] = useState("");
  const [open, setOpen] = useState(false);
  const [submitting, setSubmitting] = useState<Set<string>>(new Set());
  const alive = useRef(true);
  const requestIds = useRef(new Map<string, string>());
  const starts = useRef(new Map<string, Promise<RubricDraftJob>>());
  const previous = useRef(new Map<string, string>());
  const mutationVersion = useRef(0);
  const latestContext = useRef(context); latestContext.current = context;
  const merge = useCallback((job: RubricDraftJob) => {
    mutationVersion.current++;
    if (alive.current) setJobs(items => [job, ...items.filter(item => item.id !== job.id)].sort((a, b) => b.created_at.localeCompare(a.created_at)));
  }, []);

  useEffect(() => {
    alive.current = true;
    const controller = new AbortController();
    let timer: ReturnType<typeof setTimeout>;
    async function poll() {
      let active = false;
      const version = mutationVersion.current;
      try {
        const response = await listQuestionRubricDraftJobs(examId, controller.signal);
        if (!alive.current || controller.signal.aborted) return;
        // 较早发出的列表不能覆盖提交、取消或校验刚确认的状态；下一次轮询再同步。
        if (version !== mutationVersion.current) { timer = setTimeout(() => void poll(), 1500); return; }
        const next = [...response.jobs].sort((a, b) => b.created_at.localeCompare(a.created_at));
        for (const job of next) {
          if (job.status === "succeeded" && ["queued", "running"].includes(previous.current.get(job.id) ?? "")) {
            void message?.success({ content: <span>第 {job.question_no} 题的 AI 建议已就绪 <Button type="link" size="small" onClick={() => setOpen(true)}>查看任务</Button></span>, duration: 5 });
          }
          previous.current.set(job.id, job.status);
        }
        setJobs(items => next.map(job => ({ ...job, result: job.status === "succeeded" ? items.find(item => item.id === job.id)?.result : undefined })));
        setError(""); active = next.some(rubricJobActive);
      } catch (failure) {
        if (alive.current && !controller.signal.aborted) setError(getUserErrorMessage(failure, "任务状态读取失败，后台生成仍会继续"));
      }
      if (alive.current && !controller.signal.aborted) timer = setTimeout(() => void poll(), active ? 1500 : 5000);
    }
    if (examId) void poll();
    return () => { alive.current = false; controller.abort(); clearTimeout(timer); };
  }, [examId, message]);

  const start = useCallback((question: Question, segments: QuestionRubricSegment[], regenerate = false) => {
    const existing = starts.current.get(question.id);
    if (existing) return existing;
    const key = JSON.stringify([rubricJobQuestionVersion(question), segments, regenerate]);
    const requestId = requestIds.current.get(key) ?? crypto.randomUUID();
    requestIds.current.set(key, requestId);
    setSubmitting(items => new Set([...items, question.id]));
    // 不因换题中断短提交请求；失败保留幂等 ID，网络恢复后重试不会额外创建任务。
    const pending = createQuestionRubricDraftJob(structuredClone(question), structuredClone(segments), requestId, regenerate).then(({ job }) => {
      if (alive.current) { merge(job); previous.current.set(job.id, job.status); requestIds.current.delete(key); }
      return job;
    }).finally(() => {
      starts.current.delete(question.id);
      if (alive.current) setSubmitting(items => { const next = new Set(items); next.delete(question.id); return next; });
    });
    starts.current.set(question.id, pending);
    return pending;
  }, [merge]);
  const read = useCallback(async (id: string) => { const { job } = await getQuestionRubricDraftJob(examId, id); merge(job); return job; }, [examId, merge]);
  const cancel = useCallback(async (id: string) => { const { job } = await cancelQuestionRubricDraftJob(examId, id); merge(job); }, [examId, merge]);
  const latest = useMemo(() => {
    const items = new Map<string, RubricDraftJob>();
    for (const job of jobs) if (!items.has(job.question_id)) items.set(job.question_id, job);
    return items;
  }, [jobs]);
  const value = useMemo(() => ({ jobs, latest, submitting, start, read, cancel, show: () => setOpen(true) }), [jobs, latest, submitting, start, read, cancel]);
  const jump = (job: RubricDraftJob) => {
    const question = latestContext.current.config.questions.find(item => item.id === job.question_id);
    if (question) { setOpen(false); latestContext.current.actions.openQuestionIssue(question, "rubric"); }
  };
  return <Context.Provider value={value}>{children}
    <Drawer title="AI 建议任务" open={open} onClose={() => setOpen(false)} width="min(480px, 100vw)">
      <p className="muted">同时生成最多 5 题，超出后排队。切换题目或刷新页面可继续；建议须核对后采用。</p>
      {error && <Alert type="warning" showIcon message={error} />}
      {!jobs.length && <Empty description="暂无生成任务" />}
      <div className="rubric-job-list">{jobs.map(job => <RubricJobRow key={job.id} job={job} onJump={() => jump(job)} onCancel={() => cancel(job.id)} />)}</div>
    </Drawer>
  </Context.Provider>;
}

function RubricJobRow({ job, onJump, onCancel }: Readonly<{ job: RubricDraftJob; onJump: () => void; onCancel: () => Promise<void> }>) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  return <section className="rubric-job-row"><div><strong>第 {job.question_no} 题</strong><Tag>{rubricJobLabel[job.status]}</Tag></div>
    <small className="muted">{job.model_name} · {new Date(job.created_at).toLocaleString()}</small>
    {job.error && <p>{job.error}</p>}{job.status === "cancelling" && <p>结果将丢弃，调用结束后取消完成；本次调用可能计费。</p>}
    <Space><Button size="small" onClick={onJump}>查看题目</Button>
      {["queued", "running"].includes(job.status) && <Button size="small" loading={busy} onClick={() => {
        setBusy(true); setError(""); void onCancel().catch(failure => setError(getUserErrorMessage(failure, "取消失败，请重试"))).finally(() => setBusy(false));
      }}>取消生成</Button>}</Space>{error && <p role="alert">{error}</p>}
  </section>;
}

export function PaperRubricJobsButton() {
  const tasks = usePaperRubricJobs();
  if (!tasks) return null;
  const count = tasks.jobs.filter(rubricJobActive).length;
  return <Button onClick={tasks.show}>AI 任务{count ? ` · ${count}` : ""}</Button>;
}
export function PaperRubricJobMarker({ questionId }: Readonly<{ questionId: string }>) {
  const job = usePaperRubricJobs()?.latest.get(questionId);
  if (!job || job.status === "cancelled") return null;
  const Icon = job.status === "queued" ? Clock3 : job.status === "running" || job.status === "cancelling" ? LoaderCircle : job.status === "succeeded" ? Check : TriangleAlert;
  return <span className={`rubric-job-marker is-${job.status}`} title={`AI ${rubricJobLabel[job.status]}`} aria-label={`AI ${rubricJobLabel[job.status]}`}><Icon size={11} /></span>;
}
