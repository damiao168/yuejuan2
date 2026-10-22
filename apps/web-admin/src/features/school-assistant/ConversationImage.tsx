import { useState } from "react";
import { Modal } from "antd";
import type { SchoolChatAttachment } from "../../api/schoolAiChat";

type PreviewAttachment = Pick<SchoolChatAttachment, "name" | "media_type" | "content">;

/** 历史会剥离图片正文；只有仍保有本地图片地址的附件可以打开预览。 */
export function canPreviewConversationImage(attachment: PreviewAttachment) {
  return attachment.media_type.startsWith("image/")
    && (attachment.content.startsWith("data:image/") || attachment.content.startsWith("blob:"));
}

export function ConversationImage({ attachment, className = "" }: Readonly<{ attachment: PreviewAttachment; className?: string }>) {
  // 首次查看前不挂载弹窗；查看后保留挂载，让关闭过程完成并恢复焦点。
  const [open, setOpen] = useState<boolean>();
  if (!canPreviewConversationImage(attachment)) return null;
  return <>
    <button type="button" className={`school-conversation-image ${className}`.trim()}
      aria-label={`查看图片：${attachment.name}`} aria-haspopup="dialog" title={`查看图片：${attachment.name}`} onClick={() => setOpen(true)}>
      <img src={attachment.content} alt={attachment.name} />
    </button>
    {/* 预览通过门户打开，不改变对话流高度；关闭后由 Modal 把焦点返回原缩略图。 */}
    {open === undefined ? null : <Modal open={open} onCancel={() => setOpen(false)} title={attachment.name} footer={null}
      centered width="min(1000px, calc(100vw - 32px))" className="school-conversation-image-preview"
      destroyOnHidden keyboard focusTriggerAfterClose>
      <img src={attachment.content} alt={attachment.name} />
    </Modal>}
  </>;
}
