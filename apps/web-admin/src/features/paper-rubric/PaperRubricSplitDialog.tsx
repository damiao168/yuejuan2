import { useState } from "react";
import { Alert, Input, Modal, Select } from "antd";
import { MathMarkdown } from "../../components/MathMarkdown";
import { isSolutionSplitOffsetSafe, solutionSplitOffsets, type RubricSolutionLine, type RubricSolutionSource } from "./rubricSolutionLines";
import { solutionLineRawText, splitSolutionLine } from "./rubricSegmentation";

export function PaperRubricSplitDialog({ line, sources, disabled, onClose, onSplit }: Readonly<{
  line: RubricSolutionLine; sources: RubricSolutionSource[]; disabled: boolean; onClose: () => void; onSplit: (parts: RubricSolutionLine[]) => void;
}>) {
  const candidates = solutionSplitOffsets(line, sources);
  const [offset, setOffset] = useState<number | null>(() => candidates[0]?.offset ?? null);
  const raw = solutionLineRawText(line, sources);
  let parts: RubricSolutionLine[] = [];
  if (offset !== null && isSolutionSplitOffsetSafe(line, sources, offset)) {
    try { parts = splitSolutionLine(line, offset, sources); } catch { /* 只有空白的半段不能成为配分单元。 */ }
  }
  const safe = parts.length === 2;

  return <Modal open title={`拆分解答单元 ${line.position}`} width="min(820px, calc(100vw - 24px))" onCancel={onClose}
    okText="确认拆分" cancelText="取消" okButtonProps={{ disabled: disabled || !safe }} onOk={() => { if (!disabled && safe) onSplit(parts); }}>
    <div className="paper-rubric-split-dialog">
      <p>选择建议分界，或在原文中点击、移动光标设置分界。拆分只调整范围，原解答内容保持完整。</p>
      <Select aria-label="选择拆分位置" placeholder="选择建议分界" value={offset} options={candidates.map((item) => ({ value: item.offset, label: item.label }))}
        disabled={disabled} onChange={setOffset} />
      <label>原解答（点击设置光标）<Input.TextArea aria-label="原解答拆分光标" readOnly value={raw} autoSize={{ minRows: 4, maxRows: 10 }}
        disabled={disabled} onSelect={(event) => setOffset(event.currentTarget.selectionStart)} /></label>
      {!safe && <Alert type="info" showIcon message="请在文字、完整公式之间或推导行的分界处拆分" description="分式、矩阵和分段函数内部不能直接拆开；复杂公式可使用上方建议分界。" />}
      {safe && <div className="paper-rubric-split-preview">{parts.map((part, index) => <section key={part.id}><strong>{index === 0 ? "拆分后 · 前一部分" : "拆分后 · 后一部分"}</strong><MathMarkdown>{part.content}</MathMarkdown></section>)}</div>}
    </div>
  </Modal>;
}
