import { describe, expect, it } from "vitest";
import { interruptedMessage, requestMessages } from "./schoolAiChatMessages";

describe("interrupted AI chat replies", () => {
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
