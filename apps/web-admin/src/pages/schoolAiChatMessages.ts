import type { SchoolChatMessage } from "../api/schoolAiChat";

export function requestMessages(messages: SchoolChatMessage[]): SchoolChatMessage[] {
  return messages.flatMap((message) => {
    const attachments = message.attachments?.filter((attachment) => attachment.content);
    const unavailableNames = message.attachments?.filter((attachment) => !attachment.content).map((attachment) => attachment.name) ?? [];
    const content = message.content || (unavailableNames.length > 0 ? `此前发送过附件：${unavailableNames.join("、")}（文件内容未在浏览器中持久保存）。` : "");
    if (message.role === "assistant" && !content) return [];
    return [{ role: message.role, content, ...(attachments?.length ? { attachments } : {}) }];
  });
}

export function interruptedMessage(partial: { content: string; reasoning: string }): SchoolChatMessage | null {
  if (!partial.content && !partial.reasoning) return null;
  return { role: "assistant", content: partial.content, reasoning_content: partial.reasoning };
}
