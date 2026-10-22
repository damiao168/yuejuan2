import { Alert, Button, Drawer, InputNumber, Space } from "antd";
import { useEffect, useRef, useState } from "react";
import { downloadScoringResultImage } from "../../../../api/review";
import type { ScoringTrialSampleRole } from "../../../../api/scoringTrials";
import { materialReceiptLabel } from "../../../../api/referenceMaterials";
import { FrozenReferenceImages } from "../../../paper-rubric/FrozenReferenceImages";
import { MathMarkdown } from "../../../../components/MathMarkdown";
import { useScoringTrials } from "../hooks/useScoringTrials";
import { reasonLabels, riskFlagLabels } from "../gradingWorkbench.model";
import { trialRoles, trialSamplesFinished, trialScoreDifference, validateTrialDraft, TRIAL_MAX_SAMPLE_SIZE } from "../trialScoring";
import { referenceText } from "./FrozenQuestionReference";
import "../trial-scoring.css";

interface TrialScoringDrawerProps {
  examId: string;
  runId: string;
  scopeKey: string;
  onClose: () => void;
}

const statusLabels: Record<string, string> = {
  awaiting_enqueue: "人工基准已保存，待安排试改", running: "试改运行中", reviewing: "待负责人核对",
  stopped: "已停止", expanded: "已批准同题扩批", invalidated: "版本已变化，试改失效",
  queued: "排队中", processing: "处理中", succeeded: "已返回建议", failed: "执行失败", conflict: "版本冲突"
};

function TrialAnswerPreview({ segmentId, label }: Readonly<{ segmentId: string; label: string }>) {
  const [preview, setPreview] = useState<string | null>(null);
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(false);
  const mounted = useRef(true);
  const url = useRef<string | null>(null);
  useEffect(() => {
    mounted.current = true;
    return () => { mounted.current = false; if (url.current) URL.revokeObjectURL(url.current); };
  }, []);
  const show = async () => {
    if (loading || preview) return;
    setLoading(true); setError("");
    try {
      const file = await downloadScoringResultImage(segmentId);
      if (!mounted.current) return;
      if (!file.contentType.startsWith("image/")) { setError("该作答无法作为图片预览，请回到人工阅卷核对。"); return; }
      const next = URL.createObjectURL(file.blob);
      url.current = next; setPreview(next);
    } catch { if (mounted.current) setError("原卷加载失败，请重试或回到人工阅卷核对。"); }
    finally { if (mounted.current) setLoading(false); }
  };
  return <div className="trial-answer-preview">
    <Button size="small" loading={loading} onClick={() => void show()}>查看本份原卷</Button>
    {error ? <p role="alert">{error}</p> : null}
    {preview ? <img src={preview} alt={label} /> : null}
  </div>;
}

/** 样本、人工基准和决定均来自服务端记录；关闭或刷新后仍恢复同一试改，不用全场计数推断完成。 */
export function TrialScoringDrawer({ examId, runId, scopeKey, onClose }: Readonly<TrialScoringDrawerProps>) {
  const state = useScoringTrials(examId, runId, scopeKey);
  const [selected, setSelected] = useState<string[]>([]);
  const [baselines, setBaselines] = useState<Record<string, number | undefined>>({});
  const [roles, setRoles] = useState<Record<string, ScoringTrialSampleRole[]>>({});
  const [reason, setReason] = useState("");
  const [validation, setValidation] = useState("");
  const [page, setPage] = useState(0);
  const trial = state.trial;
  const candidates = state.items.filter(item => item.state === "review" && item.question_id === state.questionId);
  const questions = [...new Map(state.items.filter(item => ["short_answer", "calculation", "essay", "discussion"].includes(item.question_type))
    .map(item => [item.question_id, item.question_no])).entries()];
  const maxScore = typeof state.snapshot?.rubric_snapshot.max_score === "number" ? state.snapshot.rubric_snapshot.max_score : undefined;
  const aiAllowed = state.snapshot && ["AI_ASSIST", "AI_FAST_CONFIRM"].includes(state.snapshot.scoring_policy_snapshot.mode);

  useEffect(() => {
    const pending = state.pending;
    if (pending) {
      setSelected(pending.samples.map(sample => sample.segment_id));
      setBaselines(Object.fromEntries(pending.samples.map(sample => [sample.segment_id, sample.human_score])));
      setRoles(Object.fromEntries(pending.samples.map(sample => [sample.segment_id, sample.roles])));
    }
  }, [state.pending]);

  const changeQuestion = (id: string) => {
    state.setQuestionId(id); setSelected([]); setBaselines({}); setRoles({}); setPage(0); setValidation("");
  };
  const save = async () => {
    if (state.pending) { await state.create(state.pending); return; }
    const issue = validateTrialDraft(state.questionId, selected, candidates, baselines, roles, maxScore);
    if (issue) { setValidation(issue); return; }
    setValidation("");
    await state.create({ question_id: state.questionId, samples: selected.map(id => ({ segment_id: id, human_score: baselines[id]!, roles: roles[id] })) });
  };
  const labelFor = (segmentId: string) => {
    const item = state.items.find(candidate => candidate.answer_segment_id === segmentId);
    return item ? `${item.question_no} · ${item.anonymous_code || segmentId}` : segmentId;
  };

  return <Drawer title="本场 AI 试改" open onClose={onClose} width="min(1000px, 98vw)" destroyOnHidden>
    <div className="trial-scoring-workspace">
      <Alert type="info" showIcon message="先核对少量答卷，再决定同题扩批" description="人工基准与 AI 建议分开保存；试改不改正式成绩，也不能代替教师双评。少量样本只用于发现问题，不能证明整场评分质量。" />
      {state.loading ? <p role="status">正在恢复本场试改记录…</p> : null}
      {state.error || validation ? <Alert type="error" showIcon message={state.error || validation} /> : null}
      <div className="trial-scoring-toolbar">
        <label>试改记录 <select aria-label="试改记录" value={state.trialId} disabled={state.busy || Boolean(state.pending)} onChange={event => state.selectTrial(event.target.value)}>
          <option value="">新建试改</option>
          {state.trials.map(item => <option key={item.id} value={item.id}>第 {state.items.find(row => row.question_id === item.question_id)?.question_no ?? item.question_id} 题 · {statusLabels[item.status] ?? "状态待核对"}</option>)}
        </select></label>
        <Button onClick={() => void state.refresh()} disabled={state.busy}>刷新试改结果</Button>
      </div>
      {!trial ? <label>试改题目 <select aria-label="试改题目" value={state.questionId} disabled={state.busy || Boolean(state.pending)} onChange={event => changeQuestion(event.target.value)}>
        <option value="">请选择题目</option>{questions.map(([id, number]) => <option key={id} value={id}>第 {number} 题</option>)}
      </select></label> : <p><strong>第 {state.items.find(item => item.question_id === trial.question_id)?.question_no ?? trial.question_id} 题 · {statusLabels[trial.status]}</strong></p>}

      {state.snapshotLoading ? <p role="status">正在读取冻结评分依据…</p> : null}
      {state.snapshot ? <details className="trial-frozen-reference" open>
        <summary>本题冻结评分依据 · {state.snapshot.id.slice(0, 8)}</summary>
        <MathMarkdown>{referenceText(state.snapshot.source_snapshot) || "本题未保存额外编写材料，请结合原卷与评分细则核对。"}</MathMarkdown>
        <MathMarkdown>{referenceText(state.snapshot.rubric_snapshot)}</MathMarkdown>
      </details> : null}
      {state.questionId && <FrozenReferenceImages runId={runId} questionId={state.questionId}/>}

      {!trial && !state.loading ? <>
        {!aiAllowed && !state.snapshotLoading ? <Alert type="warning" showIcon message="本题冻结策略不允许 AI 建议，或评分依据尚不可读。" /> : null}
        <p>选择 5–10 份尚未开始 AI 评分的同题作答，先看原卷并独立填写人工分；标注代表、边界及证据不全样本。已选 {selected.length} / {candidates.length} 份。</p>
        <div className="trial-sample-list">
          {candidates.slice(page * 20, page * 20 + 20).map(item => {
            const id = item.answer_segment_id;
            const checked = selected.includes(id);
            return <div className="trial-sample-row" key={id}>
              <label><input type="checkbox" aria-label={`选择样本 ${labelFor(id)}`} checked={checked} disabled={state.busy || Boolean(state.pending) || (!checked && selected.length >= TRIAL_MAX_SAMPLE_SIZE)} onChange={() => setSelected(current => checked ? current.filter(value => value !== id) : [...current, id])} /> {labelFor(id)}</label>
              {checked ? <>
                <TrialAnswerPreview key={`${scopeKey}:${id}`} segmentId={id} label={labelFor(id)} />
                <label>独立人工分 <InputNumber aria-label={`人工基准 ${labelFor(id)}`} min={0} max={maxScore} value={baselines[id]} disabled={state.busy || Boolean(state.pending)} onChange={value => setBaselines(current => ({ ...current, [id]: value ?? undefined }))} /> / {maxScore ?? "满分待服务端核验"}</label>
                <fieldset disabled={state.busy || Boolean(state.pending)}><legend>样本类型（可多选）</legend>{trialRoles.map(role => <label key={role.value}><input type="checkbox" checked={roles[id]?.includes(role.value) ?? false} onChange={event => setRoles(current => ({ ...current, [id]: event.target.checked ? [...(current[id] ?? []), role.value] : (current[id] ?? []).filter(value => value !== role.value) }))} /> {role.label}</label>)}</fieldset>
              </> : null}
            </div>;
          })}
        </div>
        {candidates.length > 20 ? <Space><Button disabled={page === 0} onClick={() => setPage(value => value - 1)}>上一页</Button><span>第 {page + 1} / {Math.ceil(candidates.length / 20)} 页</span><Button disabled={(page + 1) * 20 >= candidates.length} onClick={() => setPage(value => value + 1)}>下一页</Button></Space> : null}
        <Button type="primary" loading={state.busy} disabled={!aiAllowed || state.snapshotLoading || !state.questionId} onClick={() => void save()}>{state.pending ? "继续确认原试改命令" : "保存人工基准并试改"}</Button>
      </> : null}

      {trial ? <>
        <p className="muted">样本、评分依据、模型与提示词版本已绑定；改变依赖后旧结论会失效。费用未取得可核对回执时显示未知。</p>
        <div className="trial-sample-list">{trial.samples.map(sample => {
          const result = sample.result;
          const difference = trialScoreDifference(sample);
          return <article className="trial-sample-row" key={sample.segment_id}>
            <strong>{labelFor(sample.segment_id)} · {sample.roles.map(role => trialRoles.find(item => item.value === role)?.label ?? role).join("、")}</strong>
            <TrialAnswerPreview key={`${scopeKey}:${sample.segment_id}`} segmentId={sample.segment_id} label={labelFor(sample.segment_id)} />
            <p>人工基准 {sample.human_score} / {sample.max_score} · AI 建议 {result?.status === "succeeded" && typeof result.suggested_score === "number" ? result.suggested_score : "未知"} · 分差 {difference === null ? "未知" : difference > 0 ? `+${difference}` : difference}</p>
            <p>{statusLabels[result?.status ?? "queued"] ?? "待核对状态"} · 耗时 {result?.latency_ms === undefined ? "未知" : `${result.latency_ms} ms`} · Token {result?.total_tokens === undefined ? "未报告" : result.total_tokens} · 费用未知</p>
            <p>{materialReceiptLabel(result?.material_receipt)}</p>
            {result?.error_code ? <p role="alert" title={result.error_code}>处理失败：{reasonLabels[result.error_code] ?? "本次建议未完成，请人工核对或重试原批次。"}</p> : null}
            <p title={result?.risk_flags.join("、")}>风险：{result?.risk_flags.length ? result.risk_flags.map(flag => riskFlagLabels[flag] ?? "其他风险，需核对").join("、") : "尚无可核对风险信息"}</p>
            {result?.evidence.length ? <details><summary>查看本次建议证据</summary><MathMarkdown>{referenceText(result.evidence)}</MathMarkdown></details> : <p className="muted">尚无本次建议证据，请人工核对。</p>}
          </article>;
        })}</div>
        {["awaiting_enqueue", "running", "expanded"].includes(trial.status) ? <Button loading={state.busy} onClick={() => void state.resume(trial)}>{trial.status === "expanded" ? "继续安排已批准扩批" : "继续安排原试改批次"}</Button> : null}
        {["awaiting_enqueue", "running", "reviewing"].includes(trial.status) ? <div className="trial-decision">
          <label>负责人核对说明 <textarea aria-label="负责人核对说明" value={reason} disabled={state.busy} onChange={event => setReason(event.target.value)} placeholder="记录分歧、证据问题和继续或停止的依据" rows={3} maxLength={500} /></label>
          <p>拟扩批：本题剩余 {trial.remaining_segment_ids.length} 份；只生成 AI 建议，教师仍须逐题确认。费用预计区间与剩余额度：当前未取得可核对数据，按未知处理，实际调用仍受模型预算策略限制。</p>
          <Space><Button disabled={state.busy || !reason.trim()} onClick={() => void state.decide(trial, "stop", reason.trim())}>停止试改</Button>
            <Button type="primary" loading={state.busy} disabled={!reason.trim() || trial.status !== "reviewing" || !trialSamplesFinished(trial.samples) || trial.remaining_segment_ids.length === 0} onClick={() => void state.decide(trial, "expand", reason.trim())}>确认并扩批本题</Button></Space>
        </div> : null}
        {trial.decision_reason ? <p>决定说明：{trial.decision_reason} · 决定人 {trial.decided_by ?? "未记录"}</p> : null}
      </> : null}
    </div>
  </Drawer>;
}
