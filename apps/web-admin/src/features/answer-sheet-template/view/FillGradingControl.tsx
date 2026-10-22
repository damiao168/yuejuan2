import { useState } from "react";
import { Button, InputNumber, Select } from "antd";
import type { AnalysisBox } from "../../../api/answerSheetTemplateAnalysis";
import type { LayoutRegion } from "../../../api/configuration";
import { validObjectiveBox } from "../editor/objectiveGridGeometry";
import type { TemplatePageViewProps } from "./templateViewTypes";
import { ObjectiveGridCanvas } from "./ObjectiveGridCanvas";

const coordinateLabels = { x: "左", y: "上", width: "宽", height: "高" };

export function FillGradingControl({ view, selectedCandidate }: TemplatePageViewProps & { selectedCandidate?: LayoutRegion }) {
  const group = view.editor.fillGradingGroup;
  const [editing, setEditing] = useState(false);
  const [members, setMembers] = useState(group?.questionIds ?? []);
  const [bbox, setBBox] = useState<AnalysisBox | undefined>(group?.bbox);
  const [error, setError] = useState("");
  if (!group) return null;
  const selectMembers = (ids: string[]) => {
    setMembers(ids); setError("");
  };
  return <div className="template-candidate-actions" role="group" aria-label="填空题批阅范围">
    <span>{group.questionLabels.length ? `第 ${group.questionLabels.join("、")} 题` : "填空题批阅"}</span>
    <Select aria-label="填空题批阅方式" value={group.mode} disabled={view.editor.readonly}
      onChange={value => view.editor.setFillGradingMode(value as "shared" | "individual")}
      options={[{ value: "shared", label: "按区域一起批阅", disabled: !group.canShare }, { value: "individual", label: "逐题批阅" }]} />
    <span>{group.mode === "shared" ? "显示完整作答区，各题分数和评分细则分别保留" : "按真实单题框分别批阅"}</span>
    {group.missingQuestionLabels?.length ? <span>第 {group.missingQuestionLabels.join("、")} 题尚无单题框；切换逐题后须补框。</span> : null}
    <Button size="small" disabled={view.editor.readonly} onClick={() => {
      setMembers(group.questionIds); setBBox(group.bbox); setError(""); setEditing(value => !value);
    }}>{group.mode === "shared" ? "调整整区范围" : "设置整区批阅"}</Button>
    {editing ? <div style={{ width: "100%", minWidth: 0 }}>
      <p>选择本区域包含的正式填空题，再框出完整作答区。未识别出单题框时也可设置整区。</p>
      <Select<string[]> mode="multiple" aria-label="共框批阅的填空题" value={members} options={group.options}
        onChange={selectMembers} disabled={view.editor.readonly} />
      <Button size="small" disabled={view.editor.readonly} onClick={() => selectMembers(group.options.map(option => option.value))}>选择全部填空题</Button>
      <Button size="small" disabled={view.editor.readonly || !selectedCandidate} onClick={() => {
        if (selectedCandidate) { setBBox({ x: selectedCandidate.x, y: selectedCandidate.y, width: selectedCandidate.width, height: selectedCandidate.height }); setError(""); }
      }}>使用所选待核对框</Button>
      {view.previewController.preview.imageUrl ? <ObjectiveGridCanvas imageURL={view.previewController.preview.imageUrl} block={bbox}
        groups={[]} labels={{}} orientation="horizontal" reverse={false} disabled={view.editor.readonly} subjectLabel="填空题"
        onBlock={box => { setBBox(box); setError(""); }} /> : null}
      {bbox ? <details><summary>精确调整整区坐标</summary><div className="template-coordinate-fields">{(["x", "y", "width", "height"] as const).map(key => <label key={key}>
        <span>共框{coordinateLabels[key]}（%）</span>
        <InputNumber aria-label={`共框${coordinateLabels[key]}`} value={Number((bbox[key] * 100).toFixed(3))} min={0} max={100} step={.1}
          disabled={view.editor.readonly} onChange={value => { setBBox(current => current && ({ ...current, [key]: Number(value ?? 0) / 100 })); setError(""); }} />
      </label>)}</div></details> : null}
      <Button type="primary" size="small" disabled={view.editor.readonly || members.length < 2 || !validObjectiveBox(bbox)} onClick={() => {
        if (bbox && view.editor.setFillGradingRegion(members, bbox)) setEditing(false);
        else setError("请选择至少两道本页填空题并核对完整外框；不能覆盖其他题目、个人信息或待核对的选择题。");
      }}>确认整区及成员</Button>
      {error ? <span role="alert">{error}</span> : null}
    </div> : null}
  </div>;
}
