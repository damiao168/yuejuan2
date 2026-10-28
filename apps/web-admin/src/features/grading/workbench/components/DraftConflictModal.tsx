import { useEffect, useMemo, useState } from "react";
import { Button, Modal, Radio, Space } from "antd";
import {
  draftConflictFields,
  draftConflictValue,
  initialDraftConflictChoices,
  mergeDraftConflict,
  type DraftConflictChoices,
  type DraftConflictField,
  type DraftConflictResolution
} from "../draftConflict";
import type { DraftFallbackSnapshot } from "../gradingWorkbench.types";
import type { RubricPoint } from "../../../../api/papers";

const fieldLabels: Record<DraftConflictField, string> = {
  score: "最终得分",
  rubricSelections: "评分细则",
  comments: "教师评语",
  studentFeedback: "学生可见反馈",
  privateNote: "内部备注",
  viewer: "图像查看位置"
};

function present(value: unknown, field: DraftConflictField, rubricPoints: RubricPoint[]): string {
  if (value === null || value === undefined || value === "") return "（空）";
  if (field === "rubricSelections" && typeof value === "object") {
    const selections = Object.entries(value);
    return selections.length ? selections.map(([id, score], index) => `${rubricPoints.find(point => point.id === id)?.description || `评分点 ${index + 1}`}：${score} 分`).join("\n") : "未选择评分点";
  }
  if (field === "viewer" && typeof value === "object") {
    const viewer = value as DraftFallbackSnapshot["viewer"];
    return `缩放 ${Math.round(viewer.scale * 100)}% · 旋转 ${viewer.rotation}°\n位置：横向 ${Math.round(viewer.offset.x)}，纵向 ${Math.round(viewer.offset.y)}${viewer.fit ? " · 自动适应" : ""}`;
  }
  if (typeof value === "object") return JSON.stringify(value, null, 2);
  switch (typeof value) {
    case "string": return value;
    case "number":
    case "boolean":
    case "bigint":
    case "symbol":
    case "function": return String(value);
    default: return "（空）";
  }
}

export interface DraftConflictModalProps {
  conflict: DraftConflictResolution | null;
  loading?: boolean;
  onCancel: () => void;
  onApply: (snapshot: DraftFallbackSnapshot) => void;
  rubricPoints?: RubricPoint[];
}

export function DraftConflictModal({ conflict, loading = false, onCancel, onApply, rubricPoints = [] }: Readonly<DraftConflictModalProps>) {
  const [choices, setChoices] = useState<DraftConflictChoices>(() => initialDraftConflictChoices());

  useEffect(() => {
    // 新一轮冲突从默认本机选择开始，不能沿用上一轮对其他草稿作出的字段选择。
    if (conflict) setChoices(initialDraftConflictChoices());
  }, [conflict]);

  const differingFields = useMemo(() => !conflict ? [] : draftConflictFields.filter((field) =>
    JSON.stringify(draftConflictValue(conflict.local, field)) !== JSON.stringify(draftConflictValue(conflict.server, field))
  ), [conflict]);

  return (
    <Modal
      open={Boolean(conflict)}
      title="逐项合并草稿冲突"
      okText="应用选择并重新保存"
      cancelText="稍后处理"
      confirmLoading={loading}
      width={760}
      onCancel={onCancel}
      onOk={() => conflict && onApply(mergeDraftConflict(conflict.local, conflict.server, choices))}
      destroyOnHidden
    >
      <p>服务端草稿已被其他会话更新。请为每个有差异的字段选择要保留的版本；默认保留本机修改。</p>
      <Space wrap>
        <Button size="small" onClick={() => setChoices(initialDraftConflictChoices("local"))}>全部保留本机修改</Button>
        <Button size="small" onClick={() => setChoices(initialDraftConflictChoices("server"))}>全部采用服务端</Button>
      </Space>
      <div className="draft-conflict-fields">
        {differingFields.map((field) => {
          const Container = field === "viewer" ? "details" : "section";
          return <Container className="draft-conflict-field" aria-label={`${fieldLabels[field]}冲突`} key={field}>
            {field === "viewer" ? <summary>阅读位置（默认保留本机）</summary> : <strong>{fieldLabels[field]}</strong>}
            <div className="draft-conflict-values">
              <div><span>本机修改</span><pre>{present(draftConflictValue(conflict!.local, field), field, rubricPoints)}</pre></div>
              <div><span>服务端版本</span><pre>{present(draftConflictValue(conflict!.server, field), field, rubricPoints)}</pre></div>
            </div>
            <Radio.Group
              aria-label={`${fieldLabels[field]}保留版本`}
              value={choices[field]}
              onChange={(event) => setChoices((current) => ({ ...current, [field]: event.target.value as "local" | "server" }))}
            >
              <Radio value="local">保留本机</Radio>
              <Radio value="server">采用服务端</Radio>
            </Radio.Group>
          </Container>;
        })}
        {conflict && differingFields.length === 0 ? <p>本机与服务端可保存字段已经一致，可以直接应用并恢复自动保存。</p> : null}
      </div>
    </Modal>
  );
}
