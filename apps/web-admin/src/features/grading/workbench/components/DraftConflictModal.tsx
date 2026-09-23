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

const fieldLabels: Record<DraftConflictField, string> = {
  score: "最终得分",
  rubricSelections: "评分细则",
  comments: "教师评语",
  studentFeedback: "学生可见反馈",
  privateNote: "内部备注",
  viewer: "图像查看位置"
};

function present(value: unknown): string {
  if (value === null || value === undefined || value === "") return "（空）";
  if (typeof value === "object") return JSON.stringify(value, null, 2);
  return String(value);
}

export interface DraftConflictModalProps {
  conflict: DraftConflictResolution | null;
  loading?: boolean;
  onCancel: () => void;
  onApply: (snapshot: DraftFallbackSnapshot) => void;
}

export function DraftConflictModal({ conflict, loading = false, onCancel, onApply }: DraftConflictModalProps) {
  const [choices, setChoices] = useState<DraftConflictChoices>(() => initialDraftConflictChoices());

  useEffect(() => {
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
        {differingFields.map((field) => (
          <section className="draft-conflict-field" aria-label={`${fieldLabels[field]}冲突`} key={field}>
            <strong>{fieldLabels[field]}</strong>
            <div className="draft-conflict-values">
              <div><span>本机修改</span><pre>{present(draftConflictValue(conflict!.local, field))}</pre></div>
              <div><span>服务端版本</span><pre>{present(draftConflictValue(conflict!.server, field))}</pre></div>
            </div>
            <Radio.Group
              aria-label={`${fieldLabels[field]}保留版本`}
              value={choices[field]}
              onChange={(event) => setChoices((current) => ({ ...current, [field]: event.target.value as "local" | "server" }))}
            >
              <Radio value="local">保留本机</Radio>
              <Radio value="server">采用服务端</Radio>
            </Radio.Group>
          </section>
        ))}
        {conflict && differingFields.length === 0 ? <p>本机与服务端可保存字段已经一致，可以直接应用并恢复自动保存。</p> : null}
      </div>
    </Modal>
  );
}
