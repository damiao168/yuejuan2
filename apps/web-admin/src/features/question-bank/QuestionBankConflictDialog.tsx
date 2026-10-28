import type { QuestionBankVersion } from "@edugrade/sdk";
import { App, Button, Modal, Space } from "antd";
import { QuestionBankConflictComparison } from "./QuestionBankConflictComparison";
import { useEffect, useState } from "react";
import { draftMergeGroups } from "./questionBankDraftMerge";

type Props = Readonly<{
  original: QuestionBankVersion | null;
  latest: QuestionBankVersion | null;
  localContent: Record<string, unknown>;
  scoringDraft: unknown;
  dirty: boolean;
  onClose: () => void;
  onDownload: () => void;
  onAccept: () => void;
  onMerge?: (localGroups: ReadonlySet<string>) => void;
}>;

// 弹窗只选择采纳来源；生成草稿仍委托页面统一复位修订、内容和评分，不在此保存业务数据。
export function QuestionBankConflictDialog({ original, latest, localContent, scoringDraft, dirty, onClose, onDownload, onAccept, onMerge }: Props) {
  const { modal } = App.useApp();
  const [localGroups, setLocalGroups] = useState<Set<string>>(new Set());
  useEffect(() => { if (latest) setLocalGroups(new Set()); }, [latest]);
  return <Modal title="比较本地修改与最新版本" open={Boolean(latest)} width={900} onCancel={onClose} footer={<Space wrap>
    <Button onClick={onDownload}>下载本地修改备份</Button><Button onClick={onClose}>保留本地修改</Button>
    {onMerge && latest?.workflow_status === "draft" ? <Button type="primary" onClick={() => onMerge(localGroups)}>生成合并草稿</Button> : null}
    <Button danger type="primary" onClick={() => {
      if (!dirty) { onAccept(); return; }
      modal.confirm({ title: "采用最新版本并放弃本地修改？", content: "题目内容和评分标准的未保存修改都会被替换。请先下载备份，或返回保留本地修改。", okText: "放弃本地修改并采用", okButtonProps: { danger: true }, cancelText: "保留本地修改", onOk: onAccept });
    }}>采用最新版本</Button>
  </Space>}>
    <p>采用最新版本会替换本页未保存内容。可先下载本地修改备份。</p>
    <details><summary>备份包含什么？</summary><p>题目、答案、评分编辑和附件信息；附件信息不等同于附件文件。</p></details>
    {latest && <QuestionBankConflictComparison original={original} latest={latest} localContent={localContent} scoringDraft={scoringDraft} />}
    {onMerge && latest?.workflow_status === "draft" ? <fieldset><legend>选择要保留的本地修改</legend><p>未勾选的项目使用最新版本。生成后仍需核对并保存本题草稿。</p>{draftMergeGroups.map(group => <label key={group.key} style={{ display: "block" }}><input type="checkbox" checked={localGroups.has(group.key)} onChange={event => setLocalGroups(current => {
      const next = new Set(current); if (event.target.checked) next.add(group.key); else next.delete(group.key); return next;
    })} />{group.label}</label>)}</fieldset> : null}
  </Modal>;
}
