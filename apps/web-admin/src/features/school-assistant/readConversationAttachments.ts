import type { SchoolChatAttachment } from "../../api/schoolAiChat";
import { readChatAttachment } from "./conversationAttachments";

export async function readConversationAttachments(
  files: File[],
  signal: AbortSignal,
  isLive: () => boolean,
  onError: (file: File, error: unknown) => void,
) {
  const accepted: SchoolChatAttachment[] = [];
  // 按选择顺序读取。单份失败仍处理后续文件，取消后不再开始下一份。
  for (const file of files) {
    if (signal.aborted || !isLive()) break;
    try {
      accepted.push(await readChatAttachment(file, signal));
    } catch (error) {
      if (!signal.aborted && isLive()) onError(file, error);
    }
  }
  return accepted;
}

export function pastedConversationImages(clipboard: DataTransfer) {
  const images = Array.from(clipboard.items ?? [])
    .filter((item) => item.kind === "file" && item.type.startsWith("image/"))
    .map((item) => item.getAsFile())
    .filter((file): file is File => file !== null);
  return images.length
    ? images
    : Array.from(clipboard.files ?? []).filter((file) => file.type.startsWith("image/"));
}
