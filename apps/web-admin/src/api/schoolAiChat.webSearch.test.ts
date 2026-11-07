import { beforeEach, describe, expect, it, vi } from "vitest";
import { publicSearchMessages, streamSchoolConversation, type SchoolChatMessage } from "./schoolAiChat";

const api = vi.hoisted(() => ({ stream: vi.fn().mockResolvedValue(undefined) }));
vi.mock("./client", async (original) => ({ ...await original<typeof import("./client")>(), apiClient: { requestEventStream: api.stream } }));

describe("school conversation web-search wire boundary", () => {
  beforeEach(() => vi.clearAllMocks());

  it("excludes ordinary history, cached sources and reasoning, retaining only a separate server-side context hint", async () => {
    const source = { id: "web-1", kind: "web" as const, status: "available" as const, label: "来源", read_at: "2026-11-01T00:00:00Z", consistency: "source_local" as const, scope_label: "公开网页", url: "https://example.org" };
    await streamSchoolConversation([
      { role: "assistant", content: "历史回答", web_sources: [source] },
      { role: "user", content: "公开问题", web_search: true },
    ], { exam_id: "exam", exam_name: "本地名称" }, new AbortController().signal, vi.fn(), "model", true);
    const body = JSON.parse(api.stream.mock.calls[0]![1].body);
    expect(body).toEqual({ messages: [{ role: "user", content: "公开问题" }], context: { exam_id: "exam" }, model_config_id: "model", web_search: true });
  });

  it("carries a public weather follow-up without exposing metadata to the server", async () => {
    await streamSchoolConversation([
      { role: "user", content: "北京今天天气怎么样", web_search: true },
      { role: "assistant", content: "查到了北京天气资料。", reasoning_content: "不能发送的推理", web_search: true },
      { role: "user", content: "告诉我结果", web_search: true },
    ], { exam_id: "private-exam" }, new AbortController().signal, vi.fn(), undefined, true);
    expect(JSON.parse(api.stream.mock.calls[0]![1].body)).toEqual({ messages: [
      { role: "user", content: "北京今天天气怎么样" }, { role: "assistant", content: "查到了北京天气资料。" }, { role: "user", content: "告诉我结果" },
    ], context: { exam_id: "private-exam" }, web_search: true });
  });

  it("cuts off school, ordinary and attachment turns while retaining later public turns", () => {
    const privateTurns: SchoolChatMessage[] = [
      { role: "user", content: "校内学生成绩" }, { role: "assistant", content: "私有成绩内容" },
      { role: "user", content: "北京天气", web_search: true }, { role: "assistant", content: "公开天气", web_search: true },
      { role: "user", content: "告诉我结果", web_search: true },
    ];
    expect(publicSearchMessages(privateTurns).map(message => message.content)).toEqual(["北京天气", "公开天气", "告诉我结果"]);
    privateTurns[3] = { ...privateTurns[3], attachments: [{ name: "private.txt", content: "private", media_type: "text/plain", size: 7 }] };
    expect(publicSearchMessages(privateTurns)).toEqual([{ role: "user", content: "告诉我结果" }]);
    privateTurns[3] = { role: "assistant", content: "没有明确公开标记的历史" };
    expect(publicSearchMessages(privateTurns)).toHaveLength(1);
  });

  it("bounds the public context while preserving complete question-answer pairs", () => {
    const messages: SchoolChatMessage[] = Array.from({ length: 9 }, (_, index) => [
      { role: "user" as const, content: `问题${index}`, web_search: true }, { role: "assistant" as const, content: `回答${index}`, web_search: true },
    ]).flat();
    messages.push({ role: "user", content: "继续", web_search: true });
    expect(publicSearchMessages(messages)).toHaveLength(13);
    messages[17].content = "长".repeat(20_000);
    const bounded = publicSearchMessages(messages);
    expect(bounded.reduce((size, message) => size + Array.from(message.content).length, 0)).toBeLessThanOrEqual(12_002);
    expect(Array.from(bounded[bounded.length - 2].content)).toHaveLength(4_000);
  });
});
