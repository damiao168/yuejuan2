import { useState } from "react";

export function useCopyConversationReply(toast: {
  success: (message: string) => unknown;
  error: (message: string) => unknown;
}) {
  const [copiedMessage, setCopiedMessage] = useState("");

  async function copyReply(content: string, key: string) {
    try {
      await navigator.clipboard.writeText(content);
      setCopiedMessage(key);
      toast.success("回复已复制");
      // 前一条回复的反馈到期时，只清除它自己的复制提示。
      window.setTimeout(() => setCopiedMessage((current) => (current === key ? "" : current)), 1600);
    } catch {
      toast.error("复制失败，请手动选择内容");
    }
  }

  return { copiedMessage, copyReply };
}
