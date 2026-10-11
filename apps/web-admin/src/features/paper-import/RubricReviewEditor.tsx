import { Button, Input, InputNumber, Select, Space, Switch } from "antd";
import { Plus, Trash2 } from "lucide-react";
import type { RubricPayload, RubricPoint } from "../../api/papers";
import { createScoringExample, StructuredListEditor } from "../../components/StructuredTeacherFields";

function JsonArrayEditor({ label, value, draftText, onChange, onDraftChange }: Readonly<{
  label: string; value: unknown[]; draftText?: string;
  onChange: (value: unknown[]) => void;
  onDraftChange: (text: string, error?: string) => void;
}>) {
  const text = draftText ?? JSON.stringify(value, null, 2);
  return <label className="paper-import-rubric-json-field">
    <span>{label}</span>
    <StructuredListEditor
      value={text}
      itemLabel={label}
      newItem={label === "扣分规则" ? {description:"",score:-1} : createScoringExample()}
      onChange={(nextText) => {
        const parsed: unknown = JSON.parse(nextText);
        if (!Array.isArray(parsed)) return;
        onDraftChange(nextText); onChange(parsed);
      }}
    />
  </label>;
}

export function RubricReviewEditor({ value, questionScore, jsonTexts, onJsonTextChange, onChange }: Readonly<{
  value?: RubricPayload; questionScore: number;
  jsonTexts?: { deductions?: string; examples?: string };
  onJsonTextChange?: (field: "deductions" | "examples", text: string, error?: string) => void;
  onChange: (value?: RubricPayload) => void;
}>) {
  const rubric = value ?? { status: "draft", max_score: questionScore, points: [], deductions: [], examples: [] };
  const total = rubric.points.reduce((sum, point) => sum + Number(point.score || 0), 0);
  const mismatch = Math.abs(total - questionScore) > 0.0001 || Math.abs(rubric.max_score - questionScore) > 0.0001;
  const updatePoint = (index: number, patch: Partial<RubricPoint>) => onChange({ ...rubric, points: rubric.points.map((point, i) => i === index ? { ...point, ...patch } : point) });
  return <div className="paper-import-rubric-editor">
    <Space wrap size="small">
      <span>满分</span><InputNumber min={0} value={rubric.max_score} onChange={(score) => onChange({ ...rubric, max_score: Number(score ?? 0) })} />
      <Select value={rubric.status} options={[{ label: "草稿", value: "draft" }, { label: "锁定", value: "locked" }]} onChange={(status) => onChange({ ...rubric, status })} />
      <strong className={mismatch ? "text-danger" : "text-success"}>采分点合计 {total} / 题目 {questionScore}</strong>
    </Space>
    {rubric.points.map((point, index) => <div className="paper-import-rubric-point" key={point.id || index}>
      <Input placeholder="采分点描述" value={point.description} onChange={(event) => updatePoint(index, { description: event.target.value })} />
      <InputNumber min={0} aria-label="采分点分值" value={point.score} onChange={(score) => updatePoint(index, { score: Number(score ?? 0) })} />
      <span>必需</span><Switch checked={point.required} onChange={(required) => updatePoint(index, { required })} />
      <Button danger type="text" icon={<Trash2 size={14} />} aria-label="删除采分点" onClick={() => onChange({ ...rubric, points: rubric.points.filter((_, i) => i !== index) })} />
    </div>)}
    <Button size="small" icon={<Plus size={14} />} onClick={() => onChange({ ...rubric, points: [...rubric.points, { id: `human-review-p${rubric.points.length + 1}`, description: "", score: 0, required: true }] })}>添加采分点</Button>
    <div className="paper-import-rubric-json-grid">
      <JsonArrayEditor label="扣分规则" value={rubric.deductions} draftText={jsonTexts?.deductions} onDraftChange={(text, error) => onJsonTextChange?.("deductions", text, error)} onChange={(deductions) => onChange({ ...rubric, deductions })} />
      <JsonArrayEditor label="评分示例" value={rubric.examples} draftText={jsonTexts?.examples} onDraftChange={(text, error) => onJsonTextChange?.("examples", text, error)} onChange={(examples) => onChange({ ...rubric, examples })} />
    </div>
    {mismatch ? <div className="text-danger">评分细则满分和采分点合计必须都等于题目分值，当前不能确认。</div> : null}
  </div>;
}
