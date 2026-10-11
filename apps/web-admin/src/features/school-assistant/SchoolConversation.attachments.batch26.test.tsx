// @vitest-environment jsdom
import { act, type ComponentProps, type ReactNode } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { SessionUser } from "../../auth/session";
import type { SchoolChatAttachment, SchoolChatMessage, SchoolConversationEvent } from "../../api/schoolAiChat";
import { SchoolConversation } from "./SchoolConversation";

const mocks = vi.hoisted(() => ({ model: vi.fn(), stream: vi.fn(), attachment: vi.fn(), warning: vi.fn(), error: vi.fn() }));
vi.mock("../../api/schoolAiChat", () => ({ getSchoolChatModel: mocks.model, streamSchoolConversation: mocks.stream }));
vi.mock("./conversationAttachments", () => ({ readChatAttachment: mocks.attachment }));
vi.mock("../../api/client", () => ({ getUserErrorMessage: (error: Error, fallback: string) => error.message || fallback }));
// 附件测试只核对读取与发送参数，消息展示和流传输使用薄替身。
vi.mock("./SchoolConversationDisplay", () => ({
  ConversationHistory: ({ onNew }: Readonly<{ onNew: () => void }>) => <button onClick={onNew}>开启新对话</button>,
  ConversationFlow: () => null
}));
vi.mock("antd", async () => {
  const { forwardRef } = await import("react");
  function Button({ children, icon, htmlType = "button", type: _type, size: _size, shape: _shape, loading: _loading, ...props }: Readonly<ComponentProps<"button"> & { icon?: ReactNode; htmlType?: "button" | "submit"; type?: string; size?: string; shape?: string; loading?: boolean }>) {
    return <button {...props} type={htmlType}>{icon}{children}</button>;
  }
  const TextArea = forwardRef<HTMLTextAreaElement, ComponentProps<"textarea"> & { autoSize?: unknown }>(function TextArea({ autoSize: _autoSize, ...props }, ref) {
    return <textarea {...props} ref={ref} />;
  });
  return {
    App: { useApp: () => ({ message: { warning: mocks.warning, error: mocks.error }, modal: { confirm: vi.fn() } }) },
    Button, Input: { TextArea },
    Tooltip: ({ children }: Readonly<{ children: ReactNode }>) => children,
    Drawer: ({ open, children }: Readonly<{ open: boolean; children: ReactNode }>) => open ? <div>{children}</div> : null
  };
});

const user: SessionUser = {
  id: "admin-26", username: "admin-26", displayName: "管理员", name: "管理员", role: "school_admin", roles: ["school_admin"],
  tenant: "tenant-26", school: "学校", currentExam: "", permissions: ["dashboard:read"], publicComputer: true,
  organizationScope: { resolved: true, tenantWide: false, schoolIds: ["school-26"], gradeIds: [], classIds: [] }
};

function file(name: string) { return new File([`正文 ${name}`], name, { type: "text/plain" }); }
function attachment(input: File): SchoolChatAttachment {
  return { name: input.name, media_type: input.type, size: input.size, content: `正文 ${input.name}` };
}
function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (error: Error) => void;
  const promise = new Promise<T>((done, fail) => { resolve = done; reject = fail; });
  return { promise, resolve, reject };
}

describe("学校对话附件批次边界", () => {
  let host: HTMLDivElement;
  let root: Root;
  beforeEach(() => {
    vi.resetAllMocks();
    Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
    mocks.model.mockResolvedValue({ model: { available: true, display_name: "测试模型", model_name: "synthetic", message: "" } });
    mocks.attachment.mockImplementation(async (input: File) => attachment(input));
    mocks.stream.mockImplementation(async (_messages: SchoolChatMessage[], _context: unknown, _signal: AbortSignal, callback: (event: SchoolConversationEvent) => void) => {
      callback({ type: "done", request_id: "request-26", seq: 1, completion: {
        message: { role: "assistant", content: "收到附件" }, model_name: "synthetic", display_name: "测试模型", provider_key: "provider",
        usage: { input_tokens: 1, cached_input_tokens: 0, output_tokens: 1, reasoning_tokens: 0, total_tokens: 2 }
      } });
    });
    host = document.createElement("div"); document.body.append(host); root = createRoot(host);
  });
  afterEach(async () => { await act(async () => root.unmount()); host.remove(); });

  async function render() { await act(async () => root.render(<SchoolConversation user={user} />)); }
  function input() { return host.querySelector<HTMLInputElement>('input[type="file"]')!; }
  function dispatchFiles(files: File[]) {
    const target = input();
    Object.defineProperty(target, "files", { configurable: true, value: files });
    target.dispatchEvent(new Event("change", { bubbles: true }));
  }
  async function select(files: File[]) { await act(async () => dispatchFiles(files)); }
  function names() { return [...host.querySelectorAll(".school-chat-attachments b")].map((item) => item.textContent); }
  async function click(name: string) {
    const button = [...host.querySelectorAll<HTMLButtonElement>("button")].find((item) => item.getAttribute("aria-label") === name || item.textContent === name);
    if (!button) throw new Error(`Missing button: ${name}`);
    await act(async () => button.click());
  }

  it("超出总名额只读取前三份，保持选择顺序并提示限制", async () => {
    const files = [file("第一份.txt"), file("第二份.txt"), file("第三份.txt"), file("第四份.txt")];
    await render(); await select(files);
    expect(mocks.attachment.mock.calls.map(([selected]) => selected)).toEqual(files.slice(0, 3));
    expect(names()).toEqual(files.slice(0, 3).map((selected) => selected.name));
    expect(mocks.warning).toHaveBeenCalledTimes(1);
    expect(mocks.warning).toHaveBeenCalledWith("每次最多上传 3 个文件");
    expect(input().value).toBe("");
  });

  it("已有草稿消耗剩余名额，满额后的选择不会再次读取", async () => {
    const first = [file("已有一.txt"), file("已有二.txt")];
    const next = [file("补充一.txt"), file("补充二.txt")];
    await render(); await select(first); await select(next);
    expect(names()).toEqual([...first, next[0]].map((selected) => selected.name));
    expect(mocks.attachment).toHaveBeenCalledTimes(3);
    // 满额后直接触发 input change，验证回调仍拒绝读取，不依赖上传按钮禁用。
    await select([file("满额后的文件.txt")]);
    expect(mocks.attachment).toHaveBeenCalledTimes(3);
    expect(mocks.warning).toHaveBeenCalledTimes(2);
    expect(names()).toHaveLength(3);
  });

  it("React 状态提交前的第二次选择被 ref 拦住，完成后允许新批次", async () => {
    const first = file("首批.txt");
    const pending = deferred<SchoolChatAttachment>();
    mocks.attachment.mockReturnValueOnce(pending.promise);
    await render();
    await act(async () => { dispatchFiles([first]); dispatchFiles([file("重复选择.txt")]); });
    expect(mocks.attachment).toHaveBeenCalledTimes(1);
    await act(async () => pending.resolve(attachment(first)));
    await select([file("下一批.txt")]);
    expect(mocks.attachment).toHaveBeenCalledTimes(2);
    expect(names()).toEqual(["首批.txt", "下一批.txt"]);
    expect(mocks.error).not.toHaveBeenCalled();
  });

  it("逐份 await，中间文件失败后继续读取并按顺序发送成功附件", async () => {
    const first = file("首份.txt");
    const bad = file("坏文件.txt");
    const last = file("末份.txt");
    const pending = deferred<SchoolChatAttachment>();
    const firstResult = attachment(first);
    const lastResult = attachment(last);
    mocks.attachment.mockReturnValueOnce(pending.promise).mockRejectedValueOnce(new Error("格式无效")).mockResolvedValueOnce(lastResult);
    await render(); await select([first, bad, last]);
    expect(mocks.attachment).toHaveBeenCalledTimes(1);
    expect(names()).toEqual([]);
    await act(async () => pending.resolve(firstResult));
    expect(mocks.attachment.mock.calls.map(([selected]) => selected)).toEqual([first, bad, last]);
    const signal = mocks.attachment.mock.calls[0][1];
    expect(mocks.attachment.mock.calls.every((call) => call[1] === signal)).toBe(true);
    expect(mocks.error).toHaveBeenCalledTimes(1);
    expect(mocks.error).toHaveBeenCalledWith("坏文件.txt：格式无效");
    expect(names()).toEqual([first.name, last.name]);
    await click("发送消息");
    const sent = mocks.stream.mock.calls[0][0] as SchoolChatMessage[];
    expect(sent[0].attachments).toEqual([firstResult, lastResult]);
    expect(sent[0].attachments?.[0]).toBe(firstResult);
    expect(sent[0].attachments?.[1]).toBe(lastResult);
  });

  it.each(["成功", "失败"] as const)("取消后首份读取迟到%s，不开始后续文件或回填旧批次", async (outcome) => {
    const old = file("旧附件.txt");
    const pending = deferred<SchoolChatAttachment>();
    mocks.attachment.mockReturnValueOnce(pending.promise);
    await render(); await select([old, file("不得开始.txt")]);
    const signal = mocks.attachment.mock.calls[0][1] as AbortSignal;
    await click("开启新对话");
    expect(signal.aborted).toBe(true);
    await act(async () => {
      if (outcome === "成功") pending.resolve(attachment(old));
      else pending.reject(new Error("旧解析失败"));
    });
    expect(mocks.attachment).toHaveBeenCalledTimes(1);
    expect(mocks.error).not.toHaveBeenCalled();
    expect(names()).toEqual([]);
    await select([file("当前附件.txt")]);
    expect(names()).toEqual(["当前附件.txt"]);
    expect(mocks.attachment.mock.calls[1][1]).not.toBe(signal);
  });
});
