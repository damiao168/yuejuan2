import { describe, expect, it } from "vitest";
import { interruptedMessage, requestMessages } from "./schoolAiChatMessages";
import { parseConversationThreads, serializeConversationThreads } from "../features/school-assistant/conversationStorage";

describe("interrupted AI chat replies", () => {
  it("sends image bytes in the current conversation but stores only the attachment name", () => {
    const attachment = { name: "截图.png", media_type: "image/png", size: 4, content: "data:image/png;base64,iVBORw==" };
    const messages = [{ role: "user" as const, content: "", attachments: [attachment] }, { role: "assistant" as const, content: "这是图片" }];
    expect(requestMessages(messages)[0].attachments).toEqual([attachment]);
    const stored = serializeConversationThreads([{ id: "images", title: "图片", updatedAt: 1, messages }]);
    expect(stored).not.toContain("base64");
    const restored = parseConversationThreads(JSON.parse(stored));
    expect(restored[0].messages[0].attachments?.[0].content).toBe("");
    const restoredRequest = requestMessages([...restored[0].messages, { role: "user", content: "继续" }]);
    expect(restoredRequest[0].content).toContain("文件内容未在浏览器中持久保存");
    expect(restoredRequest[0].attachments).toBeUndefined();
  });
  it("keeps reasoning even when no answer text was generated", () => {
    const stopped = interruptedMessage({ content: "", reasoning: "先分析题目" });
    expect(stopped).toEqual({ role: "assistant", content: "", reasoning_content: "先分析题目" });
    expect(requestMessages([
      { role: "user", content: "如何解题？" },
      stopped!,
      { role: "user", content: "请继续" }
    ])).toEqual([
      { role: "user", content: "如何解题？" },
      { role: "user", content: "请继续" }
    ]);
  });

  it("keeps the partial answer and reasoning together", () => {
    expect(interruptedMessage({ content: "第一步", reasoning: "先分析题目" })).toEqual({
      role: "assistant", content: "第一步", reasoning_content: "先分析题目"
    });
    expect(interruptedMessage({ content: "", reasoning: "" })).toBeNull();
  });
});
