import type { QuestionBankVersion } from "@edugrade/sdk";
import { App, Button, Modal, Space } from "antd";
import { QuestionBankConflictComparison } from "./QuestionBankConflictComparison";

type Props = Readonly<{
  original: QuestionBankVersion | null;
  latest: QuestionBankVersion | null;
  localContent: Record<string, unknown>;
  scoringDraft: unknown;
  dirty: boolean;
  onClose: () => void;
  onDownload: () => void;
  onAccept: () => void;
}>;

// 比较只展示快照；实际采用仍委托页面统一复位内容和评分草稿，不能在弹窗内另建编辑状态。
export function QuestionBankConflictDialog({ original, latest, localContent, scoringDraft, dirty, onClose, onDownload, onAccept }: Props) {
  const { modal } = App.useApp();
  return <Modal title="比较本地修改与最新版本" open={Boolean(latest)} width={900} onCancel={onClose} footer={<Space wrap>
    <Button onClick={onDownload}>下载本地修改备份</Button><Button onClick={onClose}>保留本地修改</Button>
    <Button danger type="primary" onClick={() => {
      if (!dirty) { onAccept(); return; }
      modal.confirm({ title: "采用最新版本并放弃本地修改？", content: "题目内容和评分标准的未保存修改都会被替换。请先下载备份，或返回保留本地修改。", okText: "放弃本地修改并采用", okButtonProps: { danger: true }, cancelText: "保留本地修改", onOk: onAccept });
    }}>采用最新版本</Button>
  </Space>}>
    <p>查看最新版本不会覆盖本地修改。采用前请核对差异，下载的备份包含当前题目内容、答案、评分编辑和附件信息。</p>
    {latest && <QuestionBankConflictComparison original={original} latest={latest} localContent={localContent} scoringDraft={scoringDraft} />}
  </Modal>;
}
