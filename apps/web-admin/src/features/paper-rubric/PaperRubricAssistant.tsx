import { useEffect, useMemo, useRef, useState } from "react";
import { Alert, App, Button, Input, InputNumber, Modal, Segmented, Space } from "antd";
import { generateQuestionRubricDraft, type Question, type RubricPoint, type SuggestedRubricCandidate } from "../../api/papers";
import { getUserErrorMessage } from "../../api/client";
import { MathMarkdown } from "../../components/MathMarkdown";
import { PaperRubricStepAllocator } from "./PaperRubricStepAllocator";
import { newRubricPointId } from "./rubricPointIdentity";
import { rubricSolutionLines, rubricSolutionSources, type RubricSolutionLine, type RubricSolutionSource } from "./rubricSolutionLines";
import { newRubricAssistantDraft, type RubricAssistantDraft, type SuggestedPointDraft } from "./rubricAssistantDraft";
import { mergeSolutionLines, proposedSolutionGroups, reindexSolutionLines } from "./rubricSegmentation";
import { PaperRubricSplitDialog } from "./PaperRubricSplitDialog";
import "./paper-rubric-assistant.css";
import { PaperRubricJobBoundary, PaperRubricJobSuggestion } from "./PaperRubricJobSuggestion";
import { rubricJobLabel, usePaperRubricJobs } from "./PaperRubricJobs";

interface AssistantProps {
  question: Question;
  disabled: boolean;
  contentDirty: boolean;
  hasExistingPoints: boolean;
  dirty: boolean;
  onDirtyChange: (dirty: boolean) => void;
  onApply: (points: RubricPoint[]) => void;
  draftStore?: { current: RubricAssistantDraft | null };
}

function readSuggestion(suggestion: SuggestedRubricCandidate | undefined, question: Question, lines: RubricSolutionLine[]): SuggestedPointDraft[] {
  if (!suggestion || suggestion.candidate_id !== question.id || suggestion.origin !== "ai_suggestion_from_solution" || suggestion.status !== "review_required"
    || suggestion.max_score !== question.score || !suggestion.points?.length) throw new Error("建议与当前题目不一致，请重新生成");
  return suggestion.points.map((point) => {
    const steps = point.evidence_step_ids.map((id) => {
      const match = /^step-([1-9]\d*)$/.exec(id);
      return match ? lines[Number(match[1]) - 1] : undefined;
    });
    if (!point.description.trim() || !steps.length || steps.some((step) => !step)
      || (point.suggested_score !== null && (!Number.isFinite(point.suggested_score) || point.suggested_score < 0))) {
      throw new Error("建议缺少有效的解答步骤或分值，请重新生成");
    }
    return { id: newRubricPointId(), description: point.description, score: point.suggested_score, steps: steps as RubricSolutionLine[], note: point.review_note };
  });
}

export function PaperRubricAssistant(props: Readonly<AssistantProps>) {
  const { question, disabled, contentDirty, hasExistingPoints, dirty, onDirtyChange, onApply } = props;
  const { modal } = App.useApp();
  const tasks = usePaperRubricJobs();
  const job = tasks?.latest.get(question.id);
  const localDraft = useRef<RubricAssistantDraft | null>(null);
  const store = props.draftStore ?? localDraft;
  const scope = JSON.stringify(question);
  const [sourceChanged] = useState(() => Boolean(store.current && store.current.scope !== scope
    && (store.current.ai.points.length || store.current.segmentHistory?.length || store.current.boundarySuggestion?.length || (store.current.manual.current as { groups?: unknown[] } | null)?.groups?.length)));
  if (!store.current || store.current.scope !== scope) store.current = newRubricAssistantDraft(question, store.current);
  const draft = store.current;
  const [method, setMethod] = useState<"steps" | "ai">(() => draft.method);
  const [methodVersion, setMethodVersion] = useState(0);
  const initialLines = useMemo(() => rubricSolutionLines(question), [question]);
  const sources = useMemo(() => rubricSolutionSources(question), [question]);
  const [lines, setLines] = useState(() => draft.segments ?? initialLines);
  const [history, setHistory] = useState(() => draft.segmentHistory ?? []);
  const [manualDirty, setManualDirty] = useState(false);
  const [aiDirty, setAiDirty] = useState(draft.ai.edited ?? draft.ai.points.length > 0);
  const [boundaryDirty, setBoundaryDirty] = useState(Boolean(draft.boundarySuggestion?.length));
  const [splitId, setSplitId] = useState<string | null>(null);
  const [showUnits, setShowUnits] = useState(false);
  const segmentationDirty = JSON.stringify(lines) !== JSON.stringify(initialLines);
  const lineScope = JSON.stringify(lines);
  useEffect(() => { draft.segments = lines; draft.segmentHistory = history; }, [draft, lines, history]);
  useEffect(() => { onDirtyChange(manualDirty || aiDirty || boundaryDirty || segmentationDirty); }, [onDirtyChange, manualDirty, aiDirty, boundaryDirty, segmentationDirty]);
  const blocked = disabled || contentDirty || question.score <= 0;
  const latest = useRef({ blocked, onApply, lineScope });
  latest.current = { blocked, onApply, lineScope };
  const mounted = useRef(true);
  useEffect(() => { mounted.current = true; return () => { mounted.current = false; }; }, []);

  function apply(points: RubricPoint[], validate?: () => Promise<boolean>) {
    if (blocked) return;
    if (!hasExistingPoints) { onApply(points); return; }
    modal.confirm({ title: "替换当前评分细则草稿？", content: "将替换现有采分点，并清空原扣分规则与评分示例。核对后仍需保存、锁定。",
      okText: "替换为新草稿", cancelText: "继续核对", onOk: async () => {
        if (!mounted.current || latest.current.blocked || latest.current.lineScope !== lineScope) return;
        if (validate && !await validate()) return;
        if (mounted.current && !latest.current.blocked && latest.current.lineScope === lineScope) latest.current.onApply(points);
      } });
  }

  function changeLines(next: RubricSolutionLine[]) {
    if (latest.current.blocked || !mounted.current) return;
    setHistory((current) => [...current.slice(-19), lines]);
    setLines(reindexSolutionLines(next));
    // 新边界会改变步骤编号，清除当前预览；后台任务保留原输入，教师仍可在任务中查看。
    draft.ai = { points: [], issues: [] }; draft.boundarySuggestion = undefined; setAiDirty(false); setBoundaryDirty(false); setSplitId(null);
  }

  function undo() {
    if (blocked || !history.length) return;
    setLines(history[history.length - 1]); setHistory((current) => current.slice(0, -1));
    draft.ai = { points: [], issues: [] }; draft.boundarySuggestion = undefined; setAiDirty(false); setBoundaryDirty(false); setSplitId(null);
  }

  function lineActions(line: RubricSolutionLine, index: number) {
    return <Space wrap size={4}>
      <Button size="small" disabled={blocked} onClick={() => setSplitId(line.id)} aria-label={`拆分解答单元 ${line.position}`}>拆分</Button>
      <Button size="small" disabled={blocked || index === lines.length - 1} aria-label={`合并解答单元 ${line.position} 与下一单元`}
        onClick={() => { if (!blocked && lines[index + 1]) changeLines([...lines.slice(0, index), mergeSolutionLines(lines.slice(index, index + 2), sources), ...lines.slice(index + 2)]); }}>与下步合并</Button>
    </Space>;
  }

  function changeMethod(value: string | number) {
    const next = value as "steps" | "ai";
    if (next === method) return;
    const change = () => {
      // 放弃草稿的确认也可能迟到；进入只读或保存中后，不能清除仍待教师处理的预览。
      if (!mounted.current || latest.current.blocked) return;
      draft.method = next; draft.manual.current = null; draft.ai = { points: [], issues: [] }; draft.boundarySuggestion = undefined;
      setManualDirty(false); setAiDirty(false); setBoundaryDirty(false); setMethodVersion((version) => version + 1); setMethod(next);
    };
    if (!manualDirty && !aiDirty && !boundaryDirty) { change(); return; }
    modal.confirm({ title: "放弃尚未应用的分值划分？", content: "切换方式将清空当前分段或 AI 建议，已应用的采分点草稿会保留。",
      okText: "放弃并切换", cancelText: "继续编辑", onOk: change });
  }

  return <section className="paper-rubric-assistant" aria-label="快捷设置评分细则">
    {sourceChanged && <Alert type="warning" showIcon message="题目或参考解答已更新，原划分已失效，请按最新内容重新划分" />}
    <Segmented aria-label="选择分值划分方式" value={method} onChange={changeMethod} disabled={disabled}
      options={[{ label: "按解答步骤配分", value: "steps" }, { label: "AI 建议分值划分", value: "ai" }]} />
    {method === "steps" && job && <Space><span>本题 AI {rubricJobLabel[job.status]}</span><Button type="link" size="small" disabled={blocked} onClick={() => changeMethod("ai")}>查看分值建议</Button></Space>}
    {contentDirty && <Alert type="info" showIcon message="请先保存题目与答案，再按最新内容划分分值" />}
    {lines.length > 0 && <div className="paper-rubric-segment-toolbar"><p>参考解答已细分为 <strong>{lines.length}</strong> 个单元，可拆分或合并后再配分。</p>
      <Space wrap><Button disabled={blocked || !history.length} onClick={undo}>撤销上次拆分或合并</Button>
        {method === "ai" && <Button disabled={blocked} onClick={() => setShowUnits(true)}>查看并调整解答拆分</Button>}</Space>
    </div>}
    {method === "steps" ? <PaperRubricStepAllocator key={methodVersion} lines={lines} maxScore={question.score}
      disabled={blocked} onApply={apply} onDirtyChange={setManualDirty} draftStore={draft.manual} renderLineActions={lineActions} />
      : tasks ? <PaperRubricJobSuggestion key={`${methodVersion}:${scope}:${lineScope}`} question={question} lines={lines} disabled={blocked} onApply={apply} onDirtyChange={setAiDirty} draft={draft} />
        : <PaperRubricAISuggestion key={`${methodVersion}:${scope}:${lineScope}`} question={question} lines={lines} disabled={blocked} onApply={apply} onDirtyChange={setAiDirty} draft={draft} />}
    {method === "steps" && lines.length > 1 && (tasks ? <PaperRubricJobBoundary key={lineScope} question={question} lines={lines} sources={sources} disabled={blocked} onApply={changeLines} onDirtyChange={setBoundaryDirty} draft={draft} />
      : <PaperRubricBoundarySuggestion key={lineScope} question={question} lines={lines} sources={sources} disabled={blocked} onApply={changeLines} onDirtyChange={setBoundaryDirty} draft={draft} />)}
    {splitId && lines.find((line) => line.id === splitId) && <PaperRubricSplitDialog key={splitId} line={lines.find((line) => line.id === splitId) as RubricSolutionLine}
      sources={sources} disabled={blocked} onClose={() => setSplitId(null)} onSplit={(parts) => {
        const index = lines.findIndex((line) => line.id === splitId); if (index >= 0) changeLines([...lines.slice(0, index), ...parts, ...lines.slice(index + 1)]);
      }} />}
    <Modal open={showUnits} title="调整解答拆分" width="min(900px, calc(100vw - 24px))" footer={null} onCancel={() => setShowUnits(false)}>
      <div className="paper-rubric-unit-list">{lines.map((line, index) => <section key={line.id}><strong>单元 {line.position}</strong><MathMarkdown>{line.content}</MathMarkdown>{lineActions(line, index)}</section>)}</div>
    </Modal>
    {dirty && <p className="muted">当前划分尚未应用。应用到下方采分点后，再保存评分细则。</p>}
  </section>;
}

function PaperRubricBoundarySuggestion({ question, lines, sources, disabled, onApply, onDirtyChange, draft }: Readonly<{
  question: Question; lines: RubricSolutionLine[]; sources: RubricSolutionSource[]; disabled: boolean;
  onApply: (lines: RubricSolutionLine[]) => void; onDirtyChange: (dirty: boolean) => void; draft: RubricAssistantDraft;
}>) {
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const [plan, setPlan] = useState<RubricSolutionLine[]>(draft.boundarySuggestion ?? []);
  const request = useRef<AbortController | null>(null);
  const blocked = useRef(disabled); blocked.current = disabled;
  useEffect(() => () => { request.current?.abort(); request.current = null; }, []);
  useEffect(() => {
    if (disabled) { request.current?.abort(); request.current = null; setLoading(false); }
  }, [disabled]);
  useEffect(() => { draft.boundarySuggestion = plan; onDirtyChange(plan.length > 0); }, [draft, plan, onDirtyChange]);

  async function suggest() {
    if (blocked.current || request.current) return;
    const controller = new AbortController(); request.current = controller;
    setLoading(true); setError("");
    try {
      const result = await generateQuestionRubricDraft(structuredClone(question), controller.signal, lines.map((line) => ({ spans: line.spans ?? [] })));
      if (request.current !== controller || blocked.current) return;
      const suggestion = result.suggested_rubric_candidates[0];
      readSuggestion(suggestion, question, lines);
      const next = proposedSolutionGroups(lines, suggestion.points.map((point) => point.evidence_step_ids), sources);
      if (JSON.stringify(next) === JSON.stringify(lines)) { setError("当前拆分已适合逐步配分，无需合并。也可以手动调整。"); return; }
      setPlan(next);
    } catch (failure) {
      if (request.current === controller && !controller.signal.aborted && !blocked.current) setError(getUserErrorMessage(failure, "分段建议暂不可用，可继续手动拆分或合并"));
    } finally {
      if (request.current === controller) { request.current = null; setLoading(false); }
    }
  }

  return <details className="paper-rubric-boundary-suggestion"><summary>让 AI 建议采分段（可选）</summary><div>
    <p>AI 根据关键方法与结论建议组合相邻解答单元；采用后由你填写分值。</p>
    <Button disabled={disabled || plan.length > 0} loading={loading} onClick={() => void suggest()}>生成分段建议</Button>
    {error && <Alert type="info" showIcon message={error} />}
    {plan.length > 0 && <><p>建议组合为 {plan.length} 个解答单元，原文内容完整保留：</p>
      <ol>{plan.map((line) => <li key={line.id}><MathMarkdown>{line.content}</MathMarkdown></li>)}</ol>
      <Space wrap><Button type="primary" disabled={disabled || loading} onClick={() => { if (!blocked.current) onApply(plan); }}>采用分段建议，不采用建议分值</Button>
        <Button disabled={disabled} onClick={() => setPlan([])}>放弃分段建议</Button></Space></>}
  </div></details>;
}

function PaperRubricAISuggestion({ question, lines, disabled, onApply, onDirtyChange, draft }: Readonly<{
  question: Question; lines: RubricSolutionLine[]; disabled: boolean; onApply: (points: RubricPoint[]) => void; onDirtyChange: (dirty: boolean) => void; draft: RubricAssistantDraft;
}>) {
  const { modal } = App.useApp();
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const [points, setPoints] = useState<SuggestedPointDraft[]>(() => draft.ai.points);
  const [issues, setIssues] = useState<string[]>(() => draft.ai.issues);
  const scope = JSON.stringify([question, lines]);
  const latest = useRef({ scope, disabled });
  latest.current = { scope, disabled };
  const request = useRef<AbortController | null>(null);
  const resultScope = useRef(scope);
  const mounted = useRef(true);

  useEffect(() => {
    mounted.current = true;
    // 换题、更新参考解答或退出编辑时取消旧请求；旧响应不能覆盖新题或教师已经应用的草稿。
    request.current?.abort();
    request.current = null;
    resultScope.current = scope;
    setLoading(false); setPoints(draft.ai.points); setIssues(draft.ai.issues); setError("");
    onDirtyChange(draft.ai.points.length > 0);
    return () => { mounted.current = false; request.current?.abort(); request.current = null; };
  }, [scope, onDirtyChange, draft]);
  useEffect(() => { draft.ai = { points, issues }; }, [draft, points, issues]);
  useEffect(() => {
    if (disabled && request.current) { request.current.abort(); request.current = null; setLoading(false); }
  }, [disabled]);

  async function generate() {
    if (!mounted.current || latest.current.disabled || latest.current.scope !== scope || !lines.length || request.current) return;
    const controller = new AbortController();
    request.current = controller;
    setLoading(true); setError("");
    try {
      const response = await generateQuestionRubricDraft(structuredClone(question), controller.signal, lines.map((line) => ({ spans: line.spans ?? [] })));
      if (request.current !== controller || latest.current.scope !== scope || latest.current.disabled) return;
      const suggestion = response.suggested_rubric_candidates[0];
      const next = readSuggestion(suggestion, question, lines);
      setPoints(next); setIssues(suggestion.issues ?? []); onDirtyChange(true);
    } catch (failure) {
      if (request.current === controller && !controller.signal.aborted && latest.current.scope === scope && !latest.current.disabled) {
        setError(getUserErrorMessage(failure, "评分建议暂时无法生成，可稍后重试，或按解答步骤配分"));
      }
    } finally {
      if (request.current === controller) { request.current = null; setLoading(false); }
    }
  }

  function regenerate() {
    if (!points.length) { void generate(); return; }
    modal.confirm({ title: "重新生成分值建议？", content: "新的建议将替换当前预览和已调整的建议分值。",
      okText: "重新生成", cancelText: "保留当前建议", onOk: () => {
        // 全局确认框可能晚于换题或只读状态变化；旧确认不能清除新草稿，也不能重启旧题请求。
        if (!mounted.current || latest.current.disabled || latest.current.scope !== scope || request.current) return;
        setPoints([]); setIssues([]); onDirtyChange(false); void generate();
      } });
  }

  const total = points.reduce((sum, point) => sum + (point.score ?? 0), 0);
  const complete = resultScope.current === scope && points.length > 0 && points.every((point) => point.description.trim() && point.score !== null && Number.isFinite(point.score) && point.score >= 0)
    && Math.abs(total - question.score) < 0.0001;
  function apply() {
    if (disabled || !complete || loading) return;
    // 把引用的原解答写入得分条件，保存后仍能理解采分目标，不依赖临时预览中的步骤编号。
    onApply(points.map((point) => ({ id: point.id, description: `${point.description.trim()}\n参考解答：\n${point.steps.map((step) => step.content).join("\n")}`, score: point.score as number, required: false })));
  }

  return <div className="paper-rubric-ai">
    <p>生成建议 → 核对分值 → 采用到下方评分细则。检查无误后，点击“确认并锁定细则”。</p>
    {!lines.length && <Alert type="info" showIcon message="暂无可用参考解答" description="请先核对并应用答案或教师解析的识别内容，再生成建议。" />}
    <Button loading={loading} disabled={disabled || !lines.length} onClick={regenerate}>{points.length ? "重新生成分值建议" : "生成分值建议"}</Button>
    {loading && <p role="status">正在生成本题建议，可继续查看其他内容。</p>}
    {error && <Alert type="error" showIcon message={error} />}
    {points.length > 0 && <>
      <Alert type="info" showIcon message="AI 建议 · 待教师核对" description="核对采分条件、分值及等价解法；确认后应用为草稿。" />
      {issues.length > 0 && <Alert type="warning" showIcon message="模型提示需核对" description={issues.join("；")} />}
      {points.map((point, index) => <section className="paper-rubric-ai-point" key={point.id}>
        <strong>建议采分点 {index + 1}</strong>
        <label><span>得分条件</span><Input.TextArea aria-label={`建议采分点 ${index + 1} 得分条件`} disabled={disabled || loading} value={point.description}
          autoSize={{ minRows: 2, maxRows: 6 }} onChange={(event) => setPoints((current) => current.map((item) => item.id === point.id ? { ...item, description: event.target.value } : item))} /></label>
        <label><span>建议分值</span><InputNumber aria-label={`建议采分点 ${index + 1} 分值`} disabled={disabled || loading} min={0} max={question.score} precision={2}
          value={point.score} onChange={(score) => setPoints((current) => current.map((item) => item.id === point.id ? { ...item, score } : item))} suffix="分" /></label>
        <details><summary>对应参考解答第 {point.steps.map((step) => step.position).join("、")} 行</summary>
          {point.steps.map((step) => <MathMarkdown key={step.id}>{step.content}</MathMarkdown>)}</details>
        {point.note && <p className="muted">核对提示：{point.note}</p>}
      </section>)}
      <Space wrap><strong>建议合计 {Number(total.toFixed(4))} / {question.score} 分</strong>
        <Button type="primary" disabled={disabled || loading || !complete} onClick={apply}>采用建议到评分细则</Button></Space>
      {!complete && <p className="needs-attention">请补齐分值并调整合计至本题满分后应用。</p>}
    </>}
  </div>;
}
