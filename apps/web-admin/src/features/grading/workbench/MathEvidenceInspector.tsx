import { useEffect, useMemo, useRef, useState } from "react";
import { Alert, Button, Empty, Input, Select, Space, Spin, Tag } from "antd";
import { ArrowDown, ArrowUp, RefreshCw, Save, Split, Undo2, Unlink } from "lucide-react";
import { getUserErrorMessage } from "../../../api/client";
import {
  createMathUnderstandingCorrection,
  type CreateMathCorrectionRequest,
  type MathCorrectionResponse,
  type MathUnderstandingResponse
} from "../../../api/mathUnderstanding";
import { isFormulaEvidenceSubject } from "./mathEvidenceSubjects";
import { selectEffectiveMathArtifact } from "./mathEffectiveEvidence";

export { isFormulaEvidenceSubject } from "./mathEvidenceSubjects";

type JsonRecord = Record<string, unknown>;

function records(value: unknown): JsonRecord[] {
  return Array.isArray(value) ? value.filter((item): item is JsonRecord => Boolean(item) && typeof item === "object") : [];
}

function text(value: unknown): string {
  return typeof value === "string" ? value : "";
}

function number(value: unknown): number {
  return typeof value === "number" ? value : 0;
}

function strings(value: unknown): string[] {
  return Array.isArray(value) ? value.filter((item): item is string => typeof item === "string") : [];
}

export interface MathEvidenceInspectorProps {
  segmentId: string;
  subjectCode: string;
  disabled?: boolean;
  data: MathUnderstandingResponse | null;
  loading: boolean;
  onRefresh: () => void;
  onCorrectionSaved: (result: MathCorrectionResponse) => void;
  onDirtyChange: (dirty: boolean) => void;
  onSelectStep: (stepId: string) => void;
}

export function MathEvidenceInspector({ segmentId, subjectCode, disabled = false, data, loading, onRefresh, onCorrectionSaved, onDirtyChange, onSelectStep }: MathEvidenceInspectorProps) {
  const [expanded, setExpanded] = useState(false);
  const [formulaDrafts, setFormulaDrafts] = useState<Record<string, string>>({});
  const [blockDrafts, setBlockDrafts] = useState<JsonRecord[]>([]);
  const [stepDrafts, setStepDrafts] = useState<JsonRecord[]>([]);
  const [edgeDrafts, setEdgeDrafts] = useState<JsonRecord[]>([]);
  const [pendingOperations, setPendingOperations] = useState<CreateMathCorrectionRequest["operations"]>([]);
  const [edgeFrom, setEdgeFrom] = useState("");
  const [edgeTo, setEdgeTo] = useState("");
  const [mergePrimary, setMergePrimary] = useState("");
  const [mergeSecondary, setMergeSecondary] = useState("");
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");
  const [saveNotice, setSaveNotice] = useState("");
  const currentSegment = useRef(segmentId);
  currentSegment.current = segmentId;

  const enabled = isFormulaEvidenceSubject(subjectCode);
  const baseArtifact = data?.artifact.answer_segment_id === segmentId ? data.artifact : undefined;
  const artifact = data && baseArtifact ? selectEffectiveMathArtifact(data) : undefined;
  const formulas = useMemo(() => records(artifact?.formulas), [artifact]);
  const blocks = blockDrafts;
  const graph = artifact?.solution_graph && typeof artifact.solution_graph === "object"
    ? artifact.solution_graph as JsonRecord
    : {};
  const steps = stepDrafts;
  const edges = edgeDrafts;
  const rubricEvidence = useMemo(() => records(artifact?.rubric_evidence), [artifact]);

  useEffect(() => {
      if (!data || data.artifact.answer_segment_id !== segmentId) {
        setFormulaDrafts({}); setBlockDrafts([]); setStepDrafts([]); setEdgeDrafts([]); setPendingOperations([]);
        return;
      }
      const effectiveArtifact = selectEffectiveMathArtifact(data);
      const next: Record<string, string> = {};
      for (const formula of records(effectiveArtifact.formulas)) {
        const id = text(formula.id);
        if (id) next[id] = text(formula.canonical_latex) || text(formula.raw_latex);
      }
      setFormulaDrafts(next);
      setBlockDrafts(records(effectiveArtifact.blocks));
      const responseGraph = effectiveArtifact.solution_graph && typeof effectiveArtifact.solution_graph === "object" ? effectiveArtifact.solution_graph as JsonRecord : {};
      setStepDrafts(records(responseGraph.steps));
      setEdgeDrafts(records(responseGraph.edges));
      setPendingOperations([]);
      setEdgeFrom("");
      setEdgeTo("");
      setMergePrimary("");
      setMergeSecondary("");
  }, [data, segmentId]);

  useEffect(() => {
    setExpanded(false);
    setError("");
    setSaveNotice("");
  }, [segmentId]);

  const changedFormulas = formulas.filter((formula) => {
    const id = text(formula.id);
    const original = text(formula.canonical_latex) || text(formula.raw_latex);
    return id && formulaDrafts[id] !== undefined && formulaDrafts[id].trim() !== original.trim();
  });
  const attentionCount = formulas.filter((formula) => number(formula.confidence) < 0.7).length + steps.filter((step) => number(step.confidence) < 0.7).length;
  const dirty = changedFormulas.length + pendingOperations.length > 0;
  useEffect(() => { onDirtyChange(dirty); }, [dirty, onDirtyChange]);

  if (!enabled) return null;

  const save = async () => {
    if (!artifact || !baseArtifact || changedFormulas.length + pendingOperations.length === 0) return;
    const correctedFormulas = formulas.map((formula) => {
      const id = text(formula.id);
      return id && formulaDrafts[id] !== undefined
        ? { ...formula, canonical_latex: formulaDrafts[id].trim() }
        : formula;
    });
    const correctedGraph = { ...graph, steps, edges };
    const corrected: JsonRecord = {
      subject_code: artifact.subject_code,
      answer_segment_id: artifact.answer_segment_id,
      exam_question_snapshot_id: artifact.exam_question_snapshot_id,
      input_hash: artifact.input_hash,
      engine_version: artifact.engine_version,
      blocks,
      formulas: correctedFormulas,
      relations: artifact.relations ?? [],
      solution_graph: correctedGraph,
      verifications: artifact.verifications,
      rubric_evidence: artifact.rubric_evidence
    };
    const formulaOperations: CreateMathCorrectionRequest["operations"] = changedFormulas.map((formula) => ({
      type: "correct_formula",
      target_id: text(formula.id),
      payload: {
        before: text(formula.canonical_latex) || text(formula.raw_latex),
        after: formulaDrafts[text(formula.id)].trim()
      }
    }));
    // 请求同时带原始工件版本和校正修订号；版本已被别人推进时由服务端拒绝，避免覆盖并发校正。
    const body: CreateMathCorrectionRequest = {
      expected_artifact_version: baseArtifact.version,
      expected_correction_revision: data?.correction_revision,
      reason: "阅卷教师校正公式与步骤结构",
      operations: [...pendingOperations, ...formulaOperations],
      corrected_contract: corrected
    };
    setSaving(true);
    setError("");
    try {
      const result = await createMathUnderstandingCorrection(baseArtifact.id, body);
      if (currentSegment.current !== segmentId) return;
      setPendingOperations([]);
      setFormulaDrafts(Object.fromEntries(correctedFormulas.map((formula) => [text(formula.id), text(formula.canonical_latex)])));
      setSaveNotice(result.verification_status === "queued" ? "校正已保存，数学步骤已进入重新验证队列。"
        : "校正已保存；尚未确认重新验证已入队，请检查队列或联系管理员。");
      onCorrectionSaved(result);
    } catch (cause) {
      if (currentSegment.current !== segmentId) return;
      const message = getUserErrorMessage(cause, "保存校正失败");
      setError(/409|revision|冲突/i.test(message) ? "识别结果已更新，请刷新后重新校正。" : message);
    } finally {
      if (currentSegment.current === segmentId) setSaving(false);
    }
  };

  const recordOperation = (operation: CreateMathCorrectionRequest["operations"][number]) => {
    setPendingOperations((current) => [...current, operation]);
  };

  const moveStep = (index: number, offset: -1 | 1) => {
    const target = index + offset;
    if (target < 0 || target >= steps.length) return;
    const next = [...steps];
    const [moved] = next.splice(index, 1);
    next.splice(target, 0, moved);
    setStepDrafts(next.map((step, orderHint) => ({ ...step, order_hint: orderHint + 1 })));
    recordOperation({ type: "move_step", target_id: text(moved.id), payload: { from: index + 1, to: target + 1 } });
  };

  const deleteEdge = (index: number) => {
    const edge = edges[index];
    setEdgeDrafts((current) => current.filter((_, itemIndex) => itemIndex !== index));
    recordOperation({ type: "delete_edge", target_id: `${text(edge.from_step_id)}:${text(edge.to_step_id)}`, payload: { edge } });
  };

  const connectEdge = () => {
    if (!edgeFrom || !edgeTo || edgeFrom === edgeTo || edges.some((edge) => text(edge.from_step_id) === edgeFrom && text(edge.to_step_id) === edgeTo)) return;
    const edge = { from_step_id: edgeFrom, to_step_id: edgeTo, kind: "derives" };
    setEdgeDrafts((current) => [...current, edge]);
    recordOperation({ type: "connect_edge", target_id: `${edgeFrom}:${edgeTo}`, payload: edge });
    setEdgeFrom("");
    setEdgeTo("");
  };

  const restoreBlock = (blockId: string) => {
    setBlockDrafts((current) => current.map((block) => text(block.id) === blockId ? { ...block, status: "active" } : block));
    recordOperation({ type: "restore_block", target_id: blockId, payload: { status: "active" } });
  };

  const mergeBlocks = () => {
    if (!mergePrimary || !mergeSecondary || mergePrimary === mergeSecondary) return;
    const secondary = blocks.find((block) => text(block.id) === mergeSecondary);
    setBlockDrafts((current) => current.map((block) => {
      if (text(block.id) === mergePrimary) {
        const combined = [text(block.normalized) || text(block.text), text(secondary?.normalized) || text(secondary?.text)].filter(Boolean).join(" ");
        return { ...block, normalized: combined };
      }
      return text(block.id) === mergeSecondary ? { ...block, status: "crossed_out" } : block;
    }));
    recordOperation({ type: "merge_blocks", target_id: mergePrimary, payload: { merged_block_id: mergeSecondary } });
    setMergePrimary("");
    setMergeSecondary("");
  };

  const splitStep = (index: number) => {
    const step = steps[index];
    const blockIds = strings(step.block_ids);
    if (blockIds.length < 2) return;
    const midpoint = Math.ceil(blockIds.length / 2);
    const splitId = `${text(step.id)}-split-${pendingOperations.length + 1}`;
    const first = { ...step, block_ids: blockIds.slice(0, midpoint) };
    const second = { ...step, id: splitId, block_ids: blockIds.slice(midpoint), formula_ids: [] };
    const next = [...steps.slice(0, index), first, second, ...steps.slice(index + 1)].map((item, orderHint) => ({ ...item, order_hint: orderHint + 1 }));
    setStepDrafts(next);
    recordOperation({ type: "split_step", target_id: text(step.id), payload: { created_step_id: splitId, split_after_block: blockIds[midpoint - 1] } });
  };

  return (
    <section className="math-evidence-inspector" aria-label="公式与解题步骤证据">
      <div className="math-evidence-head">
        <div>
          <h3>公式与解题步骤</h3>
          <p>仅作为评分证据建议，最终得分仍由教师确认。</p>
        </div>
        <Button size="small" icon={<RefreshCw size={14} />} loading={loading} disabled={saving} onClick={onRefresh}>刷新</Button>
      </div>
      {error ? <Alert type="warning" showIcon message={error} /> : null}
      {saveNotice ? <Alert type="info" showIcon message={saveNotice} /> : null}
      {loading && !data ? <Spin size="small" /> : null}
      {!loading && !data ? <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="暂无线性化公式证据，按原答题图阅卷" /> : null}
      {artifact ? (<>
        <div className="math-evidence-summary"><span>{formulas.length} 个公式 · {steps.length} 个步骤</span><strong>{attentionCount ? `${attentionCount} 项识别需要确认` : "未发现需要人工校正的公式问题"}</strong><Button size="small" onClick={() => setExpanded((value) => !value)}>{expanded ? "收起详情" : attentionCount ? "查看问题" : "查看详情"}</Button></div>
        {expanded ? <div className="math-evidence-body">
          <div className="math-evidence-meta">
            <Tag>{subjectCode === "mathematics" ? "数学" : subjectCode === "physics" ? "物理" : "化学"}</Tag>
            <span>识别版本 {baseArtifact?.version}{data?.correction_revision ? ` · 校正 #${data.correction_revision}` : ""}</span>
            <span>{formulas.length} 个公式 · {steps.length} 个步骤</span>
          </div>
          {data?.corrected ? <Alert type="info" showIcon message="当前显示教师校正后的数学证据，数学有效性仍需复核。" /> : null}
          {formulas.map((formula) => {
            const id = text(formula.id);
            return (
              <div className="math-formula-row" key={id}>
                <div className="math-formula-label">
                  <strong>{id}</strong>
                  <span>置信度 {Math.round(number(formula.confidence) * 100)}%</span>
                </div>
                <Input
                  value={formulaDrafts[id] ?? ""}
                  disabled={disabled || loading || saving}
                  aria-label={`校正公式 ${id}`}
                  onChange={(event) => setFormulaDrafts((current) => ({ ...current, [id]: event.target.value }))}
                />
              </div>
            );
          })}
          <div className="math-step-list">
            {steps.map((step, index) => (
              <div className="math-step-row" key={text(step.id)}>
                <Button type="link" size="small" onClick={() => onSelectStep(text(step.id))} aria-label={`定位步骤 ${text(step.id)}`}>{text(step.id)}</Button>
                <span>{text(step.normalized_text) || strings(step.formula_ids).join(" → ") || "图像步骤"}</span>
                <Tag color={number(step.confidence) < 0.7 ? "orange" : "blue"}>{Math.round(number(step.confidence) * 100)}%</Tag>
                <Button size="small" type="text" aria-label="上移步骤" disabled={disabled || index === 0} icon={<ArrowUp size={13} />} onClick={() => moveStep(index, -1)} />
                <Button size="small" type="text" aria-label="下移步骤" disabled={disabled || index === steps.length - 1} icon={<ArrowDown size={13} />} onClick={() => moveStep(index, 1)} />
                <Button size="small" type="text" aria-label="拆分步骤" disabled={disabled || strings(step.block_ids).length < 2} icon={<Split size={13} />} onClick={() => splitStep(index)} />
              </div>
            ))}
            {edges.map((edge, index) => (
              <div className="math-edge-row" key={`${text(edge.from_step_id)}-${text(edge.to_step_id)}-${index}`}>
                <span>{text(edge.from_step_id)} → {text(edge.to_step_id)} · {text(edge.kind)}</span>
                <Button size="small" type="text" danger disabled={disabled} icon={<Unlink size={13} />} onClick={() => deleteEdge(index)}>删除关系</Button>
              </div>
            ))}
            <Space.Compact block>
              <Select placeholder="起始步骤" value={edgeFrom || undefined} options={steps.map((step) => ({ label: text(step.id), value: text(step.id) }))} onChange={setEdgeFrom} />
              <Select placeholder="后续步骤" value={edgeTo || undefined} options={steps.map((step) => ({ label: text(step.id), value: text(step.id) }))} onChange={setEdgeTo} />
              <Button disabled={disabled || !edgeFrom || !edgeTo || edgeFrom === edgeTo} onClick={connectEdge}>连接</Button>
            </Space.Compact>
          </div>
          {blocks.filter((block) => text(block.status) === "crossed_out").map((block) => (
            <div className="math-crossed-block" key={text(block.id)}>
              <span>{text(block.id)} · 已划除：{text(block.text) || text(block.normalized) || "图像内容"}</span>
              <Button size="small" disabled={disabled} icon={<Undo2 size={13} />} onClick={() => restoreBlock(text(block.id))}>恢复参与推断</Button>
            </div>
          ))}
          {blocks.length > 1 ? (
            <Space.Compact block>
              <Select placeholder="保留块" value={mergePrimary || undefined} options={blocks.filter((block) => text(block.status) !== "crossed_out").map((block) => ({ label: text(block.id), value: text(block.id) }))} onChange={setMergePrimary} />
              <Select placeholder="合并块" value={mergeSecondary || undefined} options={blocks.filter((block) => text(block.status) !== "crossed_out").map((block) => ({ label: text(block.id), value: text(block.id) }))} onChange={setMergeSecondary} />
              <Button disabled={disabled || !mergePrimary || !mergeSecondary || mergePrimary === mergeSecondary} onClick={mergeBlocks}>合并块</Button>
            </Space.Compact>
          ) : null}
          {rubricEvidence.length ? (
            <div className="math-rubric-evidence">
              {rubricEvidence.map((item) => (
                <div key={text(item.id)}>
                  <Tag color={text(item.status) === "supported" ? "green" : text(item.status) === "unsupported" ? "red" : "orange"}>{text(item.status)}</Tag>
                  <span>{text(item.rubric_criterion_key)} · {text(item.explanation) || "证据待教师确认"}</span>
                </div>
              ))}
            </div>
          ) : null}
          <Space className="math-evidence-actions">
            <Button type="primary" icon={<Save size={14} />} disabled={disabled || loading || !dirty} loading={saving} onClick={() => void save()}>
              保存结构校正{changedFormulas.length + pendingOperations.length ? `（${changedFormulas.length + pendingOperations.length}）` : ""}
            </Button>
            <span>校正保存为当前数学证据，不直接改分。</span>
          </Space>
        </div> : null}
      </>) : null}
    </section>
  );
}
