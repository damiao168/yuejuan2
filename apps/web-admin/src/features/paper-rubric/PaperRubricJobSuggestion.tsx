import { useCallback, useEffect, useRef, useState } from "react";
import { Alert, App, Button, Input, InputNumber, Space } from "antd";
import type { Question, RubricDraftJob, RubricPoint } from "../../api/papers";
import { getUserErrorMessage } from "../../api/client";
import { MathMarkdown } from "../../components/MathMarkdown";
import { rubricSolutionSources, type RubricSolutionLine, type RubricSolutionSource } from "./rubricSolutionLines";
import { lineFromSpans, proposedSolutionGroups, reindexSolutionLines } from "./rubricSegmentation";
import { rubricJobActive, rubricJobLabel, rubricJobMatches, usePaperRubricJobs } from "./PaperRubricJobs";
import { readRubricSuggestion } from "./rubricSuggestion";
import type { RubricAssistantDraft, SuggestedPointDraft } from "./rubricAssistantDraft";

function jobLines(job: RubricDraftJob, question: Question, fallback: RubricSolutionLine[]) {
  const segments = job.input.solution_segments;
  return segments?.length ? reindexSolutionLines(segments.map(segment => lineFromSpans(segment.spans, rubricSolutionSources(question)))) : fallback;
}

// 生成只提交任务，预览只读持久化结果。输入框修改与正式细则采用各有独立边界。
function useJobSuggestion(question: Question, lines: RubricSolutionLine[], disabled: boolean) {
  const tasks = usePaperRubricJobs()!;
  const job = tasks.latest.get(question.id);
  const [error, setError] = useState("");
  const [reading, setReading] = useState(false);
  const readingToken = useRef(0);
  const alive = useRef(true);
  const version = JSON.stringify([question, lines, job?.id]);
  const latest = useRef({ version, disabled }); latest.current = { version, disabled };
  useEffect(() => { alive.current = true; return () => { alive.current = false; }; }, []);
  useEffect(() => { readingToken.current++; setReading(false); }, [version]);
  const current = useCallback(() => alive.current && latest.current.version === version && !latest.current.disabled, [version]);
  async function generate(regenerate = false) {
    if (!current() || tasks.submitting.has(question.id) || rubricJobActive(job)) return;
    setError("");
    try { await tasks.start(question, lines.map(line => ({ spans: line.spans ?? [] })), regenerate); }
    catch (failure) { if (current()) setError(getUserErrorMessage(failure, "任务提交失败，请重试")); }
  }
  const read = useCallback(async () => {
    if (!job || !current()) return null;
    const token = ++readingToken.current;
    setReading(true); setError("");
    try {
      const loaded = await tasks.read(job.id);
      if (!current()) return null;
      if (loaded.status !== "succeeded" || !rubricJobMatches(loaded, question) || !loaded.result) throw new Error(loaded.error || "本轮建议已失效，请重新生成");
      return { loaded, lines: jobLines(loaded, question, lines) };
    } catch (failure) { if (current()) setError(getUserErrorMessage(failure, "建议读取失败，请重试")); return null; }
    finally { if (alive.current && readingToken.current === token) setReading(false); }
  }, [job?.id, tasks.read, current, question, lines]);
  return { tasks, job, error, setError, reading, generate, read, current, busy: tasks.submitting.has(question.id) || rubricJobActive(job) };
}

function JobStatus({ job }: Readonly<{ job?: RubricDraftJob }>) {
  if (!job) return null;
  if (job.status === "queued" || job.status === "running") return <p role="status">{rubricJobLabel[job.status]}，可切换题目；刷新后也能继续查看。</p>;
  if (job.status === "cancelling") return <p role="status">正在取消，结果将丢弃；本次调用可能计费。</p>;
  if (job.status === "failed" || job.status === "stale") return <Alert type="warning" showIcon message={job.error || rubricJobLabel[job.status]} />;
  if (job.status === "cancelled") return <p>本轮生成已取消，可重新生成。</p>;
  return null;
}

export function PaperRubricJobSuggestion({ question, lines, disabled, onApply, onDirtyChange, draft }: Readonly<{
  question: Question; lines: RubricSolutionLine[]; disabled: boolean; onApply: (points: RubricPoint[], validate?: () => Promise<boolean>) => void;
  onDirtyChange: (dirty: boolean) => void; draft: RubricAssistantDraft;
}>) {
  const { modal } = App.useApp();
  const flow = useJobSuggestion(question, lines, disabled);
  const [points, setPoints] = useState<SuggestedPointDraft[]>(() => draft.ai.points);
  const [issues, setIssues] = useState<string[]>(() => draft.ai.issues);
  const [edited, setEdited] = useState(Boolean(draft.ai.edited));
  const [previewId, setPreviewId] = useState(draft.ai.jobId ?? "");
  const autoRead = useRef("");
  const pointsVersion = JSON.stringify(points);
  const latestPoints = useRef(pointsVersion); latestPoints.current = pointsVersion;
  useEffect(() => { draft.ai = { points, issues, edited, jobId: previewId }; onDirtyChange(edited); }, [draft, points, issues, edited, previewId, onDirtyChange]);

  async function loadPreview() {
    const version = pointsVersion;
    const data = await flow.read();
    if (!data || latestPoints.current !== version) return;
    try {
      const suggestion = data.loaded.result!.suggested_rubric_candidates[0];
      setPoints(readRubricSuggestion(suggestion, question, data.lines)); setIssues(suggestion.issues ?? []); setEdited(false); setPreviewId(data.loaded.id);
    } catch (failure) { flow.setError(getUserErrorMessage(failure, "建议内容无法用于当前题目，请重新生成")); }
  }
  function viewCurrent() {
    if (!edited) { void loadPreview(); return; }
    modal.confirm({ title: "查看本轮建议？", content: "这会替换当前预览中尚未采用的调整。", okText: "查看本轮建议", cancelText: "保留当前预览",
      onOk: async () => { if (flow.current() && latestPoints.current === pointsVersion) await loadPreview(); } });
  }
  useEffect(() => {
    if (flow.job?.status !== "succeeded" || points.length || disabled || autoRead.current === flow.job.id) return;
    autoRead.current = flow.job.id;
    void loadPreview();
  }, [flow.job?.id, flow.job?.status, disabled]);

  function generate() {
    if (flow.busy || disabled) return;
    const run = () => {
      if (!flow.current() || latestPoints.current !== pointsVersion) return;
      setPoints([]); setIssues([]); setEdited(false); setPreviewId("");
      void flow.generate(Boolean(flow.job));
    };
    if (!points.length) { run(); return; }
    modal.confirm({ title: "重新生成分值建议？", content: "当前预览与已调整的建议分值将被替换。", okText: "重新生成", cancelText: "保留当前建议", onOk: run });
  }
  const total = points.reduce((sum, point) => sum + (point.score ?? 0), 0);
  const complete = points.length > 0 && points.every(point => point.description.trim() && point.score !== null && Number.isFinite(point.score) && point.score >= 0) && Math.abs(total - question.score) < 0.0001;
  async function apply() {
    if (disabled || flow.busy || !complete || previewId !== flow.job?.id) return;
    const version = pointsVersion;
    // 教师调整分值后仍核对服务端来源；校验期间再编辑或换题时丢弃迟到的采用动作。
    const data = await flow.read();
    if (!data || latestPoints.current !== version || data.loaded.id !== previewId) return;
    onApply(points.map(point => ({ id: point.id, description: `${point.description.trim()}\n参考解答：\n${point.steps.map(step => step.content).join("\n")}`, score: point.score as number, required: false })), async () => {
      // 替换确认框可以长时间停留；最终确认时再核对本轮任务和教师预览，不能采用期间失效的建议。
      if (!flow.current() || latestPoints.current !== version) return false;
      const checked = await flow.read();
      return Boolean(checked && checked.loaded.id === previewId && latestPoints.current === version);
    });
  }
  return <div className="paper-rubric-ai">
    <p>核对建议后采用，再保存并确认评分细则。</p>
    {!lines.length && <Alert type="info" showIcon message="请先保存参考答案或教师解析" />}
    <Space wrap><Button loading={flow.tasks.submitting.has(question.id)} disabled={disabled || !lines.length || flow.busy || flow.reading} onClick={generate}>
      {flow.job?.status === "queued" ? "已加入队列" : flow.job?.status === "running" ? "正在生成建议" : flow.job ? "重新生成分值建议" : "生成分值建议"}</Button>
      {flow.job?.status === "succeeded" && previewId !== flow.job.id && <Button loading={flow.reading} disabled={disabled} onClick={viewCurrent}>{points.length ? "查看本轮建议" : "查看已生成建议"}</Button>}
      {flow.job && <Button type="link" onClick={flow.tasks.show}>查看任务</Button>}</Space>
    <JobStatus job={flow.job} />
    {flow.error && <Alert type="error" showIcon message={flow.error} />}
    {points.length > 0 && <>
      {flow.job?.status === "succeeded" && previewId !== flow.job.id && <p>当前显示上一轮预览，请查看本轮建议后再采用。</p>}
      <p className="muted">AI 建议 · 待教师核对。本轮建议沿用生成时的解答拆分。</p>
      {issues.length > 0 && <Alert type="warning" showIcon message={issues.join("；")} />}
      {points.map((point, index) => <section className="paper-rubric-ai-point" key={point.id}>
        <strong>建议采分点 {index + 1}</strong>
        <label><span>得分条件</span><Input.TextArea aria-label={`建议采分点 ${index + 1} 得分条件`} disabled={disabled || flow.reading} value={point.description} autoSize={{ minRows: 2, maxRows: 6 }} onChange={event => { setEdited(true); setPoints(items => items.map(item => item.id === point.id ? { ...item, description: event.target.value } : item)); }} /></label>
        <label><span>建议分值</span><InputNumber aria-label={`建议采分点 ${index + 1} 分值`} disabled={disabled || flow.reading} min={0} max={question.score} precision={2} value={point.score} suffix="分" onChange={score => { setEdited(true); setPoints(items => items.map(item => item.id === point.id ? { ...item, score } : item)); }} /></label>
        <details><summary>对应参考解答</summary>{point.steps.map(step => <MathMarkdown key={step.id}>{step.content}</MathMarkdown>)}</details>
        {point.note && <p className="muted">核对提示：{point.note}</p>}
      </section>)}
      <Space wrap><strong>建议合计 {Number(total.toFixed(4))} / {question.score} 分</strong><Button type="primary" loading={flow.reading} disabled={disabled || flow.busy || !complete || previewId !== flow.job?.id || flow.job?.status !== "succeeded"} onClick={() => void apply()}>采用建议到评分细则</Button></Space>
      {!complete && <p className="needs-attention">请补齐分值并调整合计至本题满分后采用。</p>}
    </>}
  </div>;
}

export function PaperRubricJobBoundary({ question, lines, sources, disabled, onApply, onDirtyChange, draft }: Readonly<{
  question: Question; lines: RubricSolutionLine[]; sources: RubricSolutionSource[]; disabled: boolean;
  onApply: (lines: RubricSolutionLine[]) => void; onDirtyChange: (dirty: boolean) => void; draft: RubricAssistantDraft;
}>) {
  const flow = useJobSuggestion(question, lines, disabled);
  const [plan, setPlan] = useState<RubricSolutionLine[]>(draft.boundarySuggestion ?? []);
  const [planJobId, setPlanJobId] = useState(draft.boundaryJobId ?? "");
  const planVersion = JSON.stringify([plan, planJobId]);
  const latestPlan = useRef(planVersion); latestPlan.current = planVersion;
  useEffect(() => { draft.boundarySuggestion = plan; draft.boundaryJobId = planJobId; onDirtyChange(plan.length > 0); }, [draft, plan, planJobId, onDirtyChange]);
  async function preview() {
    const data = await flow.read(); if (!data) return;
    try {
      const suggestion = data.loaded.result!.suggested_rubric_candidates[0]; readRubricSuggestion(suggestion, question, data.lines);
      const next = proposedSolutionGroups(data.lines, suggestion.points.map(point => point.evidence_step_ids), sources);
      if (JSON.stringify(next) === JSON.stringify(lines)) { flow.setError("当前拆分已适合逐步配分，也可继续手动调整。"); return; }
      setPlan(next); setPlanJobId(data.loaded.id);
    } catch (failure) { flow.setError(getUserErrorMessage(failure, "分段建议暂不可用，可继续手动调整")); }
  }
  async function applyPlan() {
    if (disabled || flow.busy || flow.reading || !plan.length || planJobId !== flow.job?.id) return;
    // 预览和采用之间参考答案仍可能更新；必须重新核对后台任务的来源。
    const data = await flow.read();
    if (data && flow.current() && data.loaded.id === planJobId && latestPlan.current === planVersion) onApply(plan);
  }
  return <details className="paper-rubric-boundary-suggestion"><summary>让 AI 建议采分段（可选）</summary><div>
    <p>建议组合相邻解答单元，采用后由你填写分值。</p>
    <Space wrap><Button loading={flow.tasks.submitting.has(question.id)} disabled={disabled || flow.busy || plan.length > 0} onClick={() => void flow.generate(Boolean(flow.job))}>生成分段建议</Button>
      {flow.job?.status === "succeeded" && <Button disabled={disabled} loading={flow.reading} onClick={() => void preview()}>查看分段建议</Button>}</Space>
    <JobStatus job={flow.job} />{flow.error && <Alert type="info" showIcon message={flow.error} />}
    {plan.length > 0 && <><ol>{plan.map(line => <li key={line.id}><MathMarkdown>{line.content}</MathMarkdown></li>)}</ol><Space wrap>
      <Button type="primary" loading={flow.reading} disabled={disabled || flow.busy || planJobId !== flow.job?.id} onClick={() => void applyPlan()}>采用分段建议，不采用建议分值</Button>
      <Button disabled={disabled} onClick={() => setPlan([])}>放弃分段建议</Button></Space></>}
  </div></details>;
}
