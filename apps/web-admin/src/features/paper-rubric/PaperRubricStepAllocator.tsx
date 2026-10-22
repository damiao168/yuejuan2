import { useEffect, useRef, useState, type ReactNode } from "react";
import { Alert, Button, Checkbox, InputNumber, Space, type GetRef } from "antd";
import type { RubricPoint } from "../../api/papers";
import { MathMarkdown } from "../../components/MathMarkdown";
import { newRubricPointId } from "./rubricPointIdentity";
import type { RubricSolutionLine } from "./rubricSolutionLines";
import "./paper-rubric-step-allocator.css";

interface StepGroup {
  id: string;
  lineIds: string[];
  score: number | null;
}

interface AllocatorState {
  sourceKey: string;
  sourceLines: { id: string; content: string }[];
  maxScore: number;
  groups: StepGroup[];
  selection: [number, number] | null;
  selectionScore: number | null;
  error: string;
  invalidatedNotice: string;
}

export interface PaperRubricStepAllocatorProps {
  lines: RubricSolutionLine[];
  maxScore: number;
  disabled?: boolean;
  onApply: (points: RubricPoint[]) => void;
  onDirtyChange?: (dirty: boolean) => void;
  draftStore?: { current: unknown };
  renderLineActions?: (line: RubricSolutionLine, index: number) => ReactNode;
}

function emptyState(sourceKey: string, lines: RubricSolutionLine[], maxScore: number): AllocatorState {
  return { sourceKey, sourceLines: lines.map(({ id, content }) => ({ id, content })), maxScore, groups: [], selection: null, selectionScore: null, error: "", invalidatedNotice: "" };
}

function reconcileState(draft: AllocatorState, sourceKey: string, lines: RubricSolutionLine[], maxScore: number): AllocatorState {
  if (draft.sourceKey === sourceKey) return draft;
  const next = emptyState(sourceKey, lines, maxScore);
  if (draft.maxScore !== maxScore) return { ...next, invalidatedNotice: "本题满分已改变，请重新核对并分配各段分值。" };
  const oldContents = new Map(draft.sourceLines.map((line) => [line.id, line.content]));
  const positions = new Map(lines.map((line, index) => [line.id, index]));
  // 拆分和合并只作废内容发生变化的采分段；保留编号和原文均相同且仍连续的区间，避免教师重复配分。
  const groups = draft.groups.filter((group) => {
    const start = positions.get(group.lineIds[0]);
    return start !== undefined && group.lineIds.every((id, offset) => lines[start + offset]?.id === id && oldContents.get(id) === lines[start + offset].content);
  }).sort((left, right) => (positions.get(left.lineIds[0]) ?? 0) - (positions.get(right.lineIds[0]) ?? 0));
  const removed = draft.groups.length - groups.length;
  const invalidatedNotice = removed
    ? `解答内容或分段边界已改变，已移除 ${removed} 个受影响采分段，请重新配分。${groups.length ? "其他采分段已保留。" : ""}`
    : "解答分段已调整，已有采分段保持不变，请核对尚未配分的内容。";
  return { ...next, groups, invalidatedNotice };
}

function readStoredDraft(value: unknown, sourceKey: string, lines: RubricSolutionLine[], maxScore: number): AllocatorState {
  if (!value || typeof value !== "object") return emptyState(sourceKey, lines, maxScore);
  const draft = value as AllocatorState;
  if (typeof draft.sourceKey !== "string" || typeof draft.maxScore !== "number" || !Array.isArray(draft.sourceLines)
    || !draft.sourceLines.every((line) => line && typeof line.id === "string" && typeof line.content === "string")
    || !Array.isArray(draft.groups) || !draft.groups.every((group) => group && typeof group.id === "string" && Array.isArray(group.lineIds) && group.lineIds.length > 0 && group.lineIds.every((id) => typeof id === "string")
    && (group.score === null || typeof group.score === "number")) || !(draft.selection === null || Array.isArray(draft.selection))
    || !(draft.selectionScore === null || typeof draft.selectionScore === "number") || typeof draft.error !== "string" || typeof draft.invalidatedNotice !== "string") return emptyState(sourceKey, lines, maxScore);
  return reconcileState(draft, sourceKey, lines, maxScore);
}

function scoreText(value: number) {
  return Number(value.toFixed(4)).toString();
}

function rangeText(start: number, end: number, lines: RubricSolutionLine[]) {
  return start === end ? `第 ${lines[start].position} 行` : `第 ${lines[start].position}—${lines[end].position} 行`;
}

export function PaperRubricStepAllocator({ lines, maxScore, disabled = false, onApply, onDirtyChange, draftStore, renderLineActions }: Readonly<PaperRubricStepAllocatorProps>) {
  // 显示行号和原文位置会随拆分调整，不能单独触发已有采分段失效。
  const sourceKey = JSON.stringify([maxScore, lines.map(({ id, content }) => [id, content])]);
  const [stored, setStored] = useState<AllocatorState>(() => readStoredDraft(draftStore?.current, sourceKey, lines, maxScore));
  const scoreInput = useRef<GetRef<typeof InputNumber>>(null);
  const state = reconcileState(stored, sourceKey, lines, maxScore);
  const dirty = state.groups.length > 0 || state.selection !== null || state.selectionScore !== null;
  useEffect(() => onDirtyChange?.(dirty), [onDirtyChange, dirty]);
  // 内容变更立即影响渲染和采用结果；随后提交新版本，撤销拆分时也不能悄悄恢复已作废的旧分值。
  useEffect(() => { if (stored !== state) setStored(state); }, [stored, state]);
  // 父页刷新同一题可能重挂快捷编辑器；保留未采用的分组和未添加的选择，重挂时仍校验原文是否一致。
  useEffect(() => { if (draftStore) draftStore.current = state; }, [draftStore, state]);
  const updateState = (change: (current: AllocatorState) => AllocatorState) => {
    setStored((current) => change(reconcileState(current, sourceKey, lines, maxScore)));
  };
  const total = state.groups.reduce((sum, group) => sum + (Number.isFinite(group.score) ? group.score ?? 0 : 0), 0);
  const remaining = maxScore - total;
  const validMaxScore = Number.isFinite(maxScore) && maxScore > 0;
  const groupScoresValid = state.groups.every((group) => group.score !== null && Number.isFinite(group.score) && group.score > 0);
  const balanced = validMaxScore && Math.abs(remaining) < 0.0001;
  const editingDisabled = disabled || !validMaxScore;
  const lineGroup = (index: number) => state.groups.find((group) => group.lineIds.includes(lines[index].id));
  const assigned = (index: number) => Boolean(lineGroup(index));
  const groupRange = (group: StepGroup): [number, number] => [lines.findIndex((line) => line.id === group.lineIds[0]), lines.findIndex((line) => line.id === group.lineIds[group.lineIds.length - 1])];

  function selectLine(index: number) {
    updateState((current) => {
      const previous = current.selection;
      let selection: [number, number] | null = previous;
      if (!previous) selection = [index, index];
      else if (index < previous[0]) selection = [index, previous[1]];
      else if (index > previous[1]) selection = [previous[0], index];
      else if (previous[0] === previous[1]) selection = null;
      else if (index === previous[0]) selection = [previous[0] + 1, previous[1]];
      else if (index === previous[1]) selection = [previous[0], previous[1] - 1];
      else return { ...current, error: "一个采分区间必须连续，请从首行或末行调整选择。" };

      // 两端虽然都未使用，中间也可能跨过已分配区间，不能重复计入另一采分点。
      if (selection && current.groups.some((group) => lines.slice(selection[0], selection[1] + 1).some((line) => group.lineIds.includes(line.id)))) {
        return { ...current, error: "所选范围包含已分配的步骤，请选择不重叠的连续区间。" };
      }
      return { ...current, selection, error: "" };
    });
  }

  function endSegment(index: number) {
    const start = lines.findIndex((_, lineIndex) => !assigned(lineIndex));
    // 从上往下配分时不能越过已经分配的区间；保留首末勾选供教师单独处理后面的空段。
    if (start < 0 || start > index || lines.slice(start, index + 1).some((_, offset) => assigned(start + offset))) {
      updateState((current) => ({ ...current, error: "请先完成前面的未分配区间，或使用首末勾选单独选择后面的连续步骤。" }));
      return;
    }
    updateState((current) => ({ ...current, selection: [start, index], error: "" }));
    scoreInput.current?.focus();
  }

  function addGroup() {
    updateState((current) => {
      if (!current.selection) return { ...current, error: "请先勾选要分配分值的连续步骤。" };
      const score = current.selectionScore;
      if (score === null || !Number.isFinite(score) || score <= 0) return { ...current, error: "请输入大于 0 的本段分值。" };
      const used = current.groups.reduce((sum, group) => sum + (group.score ?? 0), 0);
      if (used + score - maxScore > 0.0001) return { ...current, error: "采分点合计不能超过本题满分，请调整分值。" };
      const group: StepGroup = { id: newRubricPointId(), lineIds: lines.slice(current.selection[0], current.selection[1] + 1).map((line) => line.id), score };
      return { ...current, groups: [...current.groups, group].sort((left, right) => groupRange(left)[0] - groupRange(right)[0]), selection: null, selectionScore: null, error: "" };
    });
  }

  function applyGroups() {
    if (editingDisabled || !state.groups.length || !groupScoresValid || !balanced) return;
    // 保存完整步骤原文，后续阅卷不依赖本组件的临时行号；这里只生成待教师保存的草稿。
    onApply(state.groups.map((group) => ({
      id: group.id,
      description: `完成以下解答步骤：\n${group.lineIds.map((id) => lines.find((line) => line.id === id)?.content ?? "").join("\n")}`,
      score: group.score as number,
      required: false
    })));
  }

  if (!lines.length) return <Space direction="vertical" style={{ width: "100%" }}>
    {state.invalidatedNotice && <Alert type="warning" showIcon message={state.invalidatedNotice} />}
    <Alert type="info" showIcon message="请先补充参考解答" description="暂无可分段的解答内容。请先核对并保存参考解答或文字标准答案，再按步骤分配分值。" />
  </Space>;

  return <section className="paper-rubric-step-allocator" aria-label="按解答步骤分配分值">
    <p className="paper-rubric-step-help">从上往下点击“在此结束采分段”，填写本段分值；也可勾选首末行，自行选择连续步骤。</p>
    {!validMaxScore && <Alert type="warning" showIcon message="请先保存本题分值，再分配步骤分值" />}
    {state.invalidatedNotice && <Alert type="warning" showIcon message={state.invalidatedNotice} closable={!disabled} onClose={() => updateState((current) => ({ ...current, invalidatedNotice: "" }))} />}
    <ol className="paper-rubric-solution-lines">{lines.map((line, index) => {
      const group = lineGroup(index);
      const used = Boolean(group);
      const groupIndex = group ? state.groups.indexOf(group) : -1;
      const groupEnd = group?.lineIds[group.lineIds.length - 1] === line.id;
      const checked = Boolean(state.selection && index >= state.selection[0] && index <= state.selection[1]);
      return <li key={line.id} className={`${checked ? "is-selected" : ""} ${used ? `is-assigned is-group-${groupIndex % 2}` : ""} ${groupEnd ? "is-group-end" : ""}`.trim()}>
        <span className="paper-rubric-line-number">{line.position}</span>
        <MathMarkdown className="paper-rubric-line-content">{line.content}</MathMarkdown>
        <div className="paper-rubric-line-action">
          {groupEnd && group && <strong className="paper-rubric-assigned-score">采分点 {groupIndex + 1} · {group.score === null ? "待配分" : `${scoreText(group.score)} 分`}</strong>}
          <div className="paper-rubric-line-selection">
            {!used && <Button size="small" disabled={editingDisabled} aria-label={`在第 ${line.position} 行结束采分段`} onClick={() => endSegment(index)}>在此结束采分段</Button>}
            <Checkbox checked={checked} disabled={editingDisabled || used} aria-label={`选择第 ${line.position} 行`} onChange={() => selectLine(index)} />
          </div>
          {renderLineActions?.(line, index)}
        </div>
      </li>;
    })}</ol>
    <div className="paper-rubric-selection-controls">
      <span aria-live="polite">{state.selection ? `已选${rangeText(state.selection[0], state.selection[1], lines)}` : "请选择解答步骤"}</span>
      <Space wrap>
        <InputNumber ref={scoreInput} className="paper-rubric-score-input" aria-label="本段分值" min={0} max={maxScore} precision={2} step={0.5} value={state.selectionScore} disabled={editingDisabled} placeholder="本段分值"
          onChange={(score) => updateState((current) => ({ ...current, selectionScore: score, error: "" }))} suffix="分" />
        <Button disabled={editingDisabled || !state.selection} onClick={addGroup}>添加为采分点</Button>
        {(state.selection || state.selectionScore !== null) && <Button type="text" disabled={editingDisabled} onClick={() => updateState((current) => ({ ...current, selection: null, selectionScore: null, error: "" }))}>清除选择</Button>}
      </Space>
    </div>
    {state.error && <Alert type="error" showIcon message={state.error} />}
    {state.groups.length > 0 && <div className="paper-rubric-step-groups">{state.groups.map((group, index) => <div className="paper-rubric-step-group" key={group.id}>
      <span><strong>采分点 {index + 1}</strong> · {rangeText(...groupRange(group), lines)}</span>
      <Space wrap>
        <InputNumber className="paper-rubric-score-input" aria-label={`采分点 ${index + 1} 分值`} min={0} max={maxScore} precision={2} step={0.5} value={group.score} disabled={editingDisabled} suffix="分"
          onChange={(score) => updateState((current) => ({ ...current, groups: current.groups.map((item) => item.id === group.id ? { ...item, score } : item), error: "" }))} />
        <Button danger size="small" disabled={editingDisabled} aria-label={`删除分段采分点 ${index + 1}`}
          onClick={() => updateState((current) => ({ ...current, groups: current.groups.filter((item) => item.id !== group.id), error: "" }))}>删除</Button>
      </Space>
    </div>)}</div>}
    <div className="paper-rubric-step-footer">
      <div aria-live="polite"><strong>已分配 {scoreText(total)} / {scoreText(maxScore)} 分</strong><span className="paper-rubric-step-remaining">剩余 {scoreText(remaining)} 分</span>
        {state.groups.length > 0 && (!balanced || !groupScoresValid) && <p>每段分值须大于 0，且合计等于本题满分后才能采用。</p>}
      </div>
      <Button type="primary" disabled={editingDisabled || !state.groups.length || !groupScoresValid || !balanced} onClick={applyGroups}>采用分段结果</Button>
    </div>
    <p className="paper-rubric-step-help">采用后可继续编辑得分条件，核对无误后再保存并锁定评分细则。</p>
  </section>;
}
