import { useEffect, useRef, useState } from "react";
import { Alert, Button, Drawer, Space } from "antd";
import { getUserErrorMessage } from "../../api/client";
import { downloadGovernanceMaterial, evaluationReportExample, observationExample } from "./governanceMaterials";
import { parseGovernanceMaterial, type GovernanceMaterial } from "./materialIntake";

export function GovernanceMaterialIntake({ tenantID, disabled, onReady, onDirtyChange }: Readonly<{ tenantID: string; disabled: boolean; onReady: (material: GovernanceMaterial) => void; onDirtyChange: (dirty: boolean) => void }>) {
  const [open, setOpen] = useState(false), [loading, setLoading] = useState(false);
  const [material, setMaterial] = useState<GovernanceMaterial>(), [filename, setFilename] = useState(""), [error, setError] = useState("");
  const generation = useRef(0), scope = useRef(tenantID); scope.current = tenantID;
  useEffect(() => { onDirtyChange(open && Boolean(material)); return () => onDirtyChange(false); }, [open, material, onDirtyChange]);
  // biome-ignore lint/correctness/useExhaustiveDependencies: 材料只属于读取时的学校，切校必须清空并废弃旧文件解析结果。
  useEffect(() => { generation.current++; setOpen(false); setMaterial(undefined); setError(""); setLoading(false); return () => { generation.current++; }; }, [tenantID]);
  const read = async (file: File) => {
    const request = ++generation.current, owner = tenantID; setLoading(true); setError(""); setMaterial(undefined);
    try {
      if (file.size > 2 * 1024 * 1024) throw new Error("材料请控制在 2 MB 以内");
      const next = parseGovernanceMaterial(await file.text());
      if (request !== generation.current || scope.current !== owner) return;
      setMaterial(next); setFilename(file.name);
    } catch (reason) { if (request === generation.current) setError(getUserErrorMessage(reason, "材料读取失败")); }
    finally { if (request === generation.current) setLoading(false); }
  };
  const metadata = material?.kind === "candidate_summary" ? material.material.run : material?.material.evaluation;
  return <>
    <Button disabled={disabled} onClick={() => setOpen(true)}>导入评测材料</Button>
    {open ? <Drawer title="导入评测材料" width={650} open onClose={() => { generation.current++; setOpen(false); setLoading(false); }}>
      <p>选择已完成的离线报告，预览后继续登记。</p>
      <label>评测材料文件<input type="file" accept=".json,application/json" disabled={loading} onChange={event => { const file = event.target.files?.[0]; event.target.value = ""; if (file) void read(file); }} /></label>
      {error ? <Alert type="error" showIcon message={error} /> : null}
      {material && metadata ? <>
        <h3>{material.kind === "candidate_summary" ? "候选模型汇总" : "逐卷评分证据"}：{metadata.display_name}</h3>
        <p>{material.kind === "candidate_summary" ? `${material.material.candidateCount} 个候选；继续后核对报告，再分别保存候选结果。` : `${material.material.observations.length} 条记录；登记报告后直接核对并导入，最后冻结。`}</p>
        <details><summary>材料来源与版本</summary><p>文件：{filename}</p><p>数据集：{metadata.dataset_reference}</p><p>SHA-256：{metadata.dataset_sha256}</p>{material.kind === "scoring_records" ? <p>模型 {material.material.evaluation.model_reference} · 提示词 {material.material.evaluation.prompt_version} · 细则 {material.material.evaluation.rubric_version}</p> : material.material.candidates.map((candidate, index) => <p key={index}>候选 {index + 1}：提示词 {candidate.prompt_version} · 细则 {candidate.rubric_version}</p>)}</details>
        <Button type="primary" disabled={disabled} onClick={() => { onReady(material); setMaterial(undefined); setOpen(false); }}>继续登记</Button>
      </> : null}
      <details style={{ marginTop: 18 }}><summary>材料模板</summary><Space wrap><Button onClick={() => downloadGovernanceMaterial("候选模型汇总模板.json", evaluationReportExample)}>候选汇总模板</Button><Button onClick={() => downloadGovernanceMaterial("逐卷评分证据模板.json", { evaluation: { key: "math-report-v1", display_name: "数学评分验证报告", model_reference: "实际模型版本", prompt_version: "实际提示词版本", rubric_version: "实际细则版本", dataset_reference: "authorized-dataset-v1", dataset_sha256: "请替换为实际数据集64位SHA256" }, observations: observationExample })}>逐卷评分模板</Button></Space></details>
    </Drawer> : null}
  </>;
}
