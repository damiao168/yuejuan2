// @vitest-environment jsdom
import { act, type ReactNode } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { apiClient } from "../api/client";
import { setBusinessCommandScope } from "../api/businessCommand";
import { PendingBusinessCommands } from "./PendingBusinessCommands";

vi.mock("antd", async () => {
  const { createElement } = await import("react");
  return {
    Alert: ({ message, description }: { message: ReactNode; description: ReactNode }) => createElement("aside", null, message, description),
    Space: ({ children }: { children: ReactNode }) => createElement("div", null, children),
    Button: ({ children, disabled, onClick }: { children: ReactNode; disabled?: boolean; onClick: () => void }) => createElement("button", { disabled, onClick }, children),
  };
});

function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((onResolve) => { resolve = onResolve; });
  return { promise, resolve };
}
const key = (actor: string) => `business-command:tenant:${actor}:score.publish:exam`;

describe("pending command UI session ownership", () => {
  let host: HTMLDivElement;
  let root: Root;
  beforeEach(() => {
    Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
    window.localStorage.clear();
    setBusinessCommandScope("tenant", "A");
    host = document.createElement("div");
    document.body.append(host);
    root = createRoot(host);
  });
  afterEach(async () => { await act(async () => root.unmount()); host.remove(); vi.restoreAllMocks(); setBusinessCommandScope("", ""); });

  it("does not resend A's takeover receipt or alter B's recovery state after switching accounts", async () => {
    window.localStorage.setItem(key("A"), JSON.stringify({ id: "A-command" }));
    const response = deferred<unknown>();
    const request = vi.spyOn(apiClient, "request").mockImplementation(() => response.promise as Promise<never>);
    await act(async () => root.render(<PendingBusinessCommands key="A" tenant="tenant" actor="A" />));
    await act(async () => host.querySelector("button")!.click());
    window.localStorage.setItem(key("B"), JSON.stringify({ id: "B-command" }));
    await act(async () => { setBusinessCommandScope("tenant", "B"); root.render(<PendingBusinessCommands key="B" tenant="tenant" actor="B" />); });
    await act(async () => response.resolve({ command_id: "A-command", status: "takeover_ready", payload: { reason: "frozen-A" } }));
    expect(request).toHaveBeenCalledTimes(1);
    expect(window.localStorage.getItem(key("A"))).toBe(JSON.stringify({ id: "A-command" }));
    expect(window.localStorage.getItem(key("B"))).toBe(JSON.stringify({ id: "B-command" }));
    expect(host.querySelector("button")?.disabled).toBe(false);
    expect(host.textContent).not.toContain("失败");
  });

  it("keeps the new generation busy when an earlier recovery completes after lock and unlock", async () => {
    window.localStorage.setItem(key("A"), JSON.stringify({ id: "original" }));
    const oldResponse = deferred<unknown>();
    const currentResponse = deferred<unknown>();
    const request = vi.spyOn(apiClient, "request")
      .mockImplementationOnce(() => oldResponse.promise as Promise<never>)
      .mockImplementationOnce(() => currentResponse.promise as Promise<never>);
    await act(async () => root.render(<PendingBusinessCommands tenant="tenant" actor="A" />));
    await act(async () => host.querySelector("button")!.click());
    await act(async () => { setBusinessCommandScope("", ""); setBusinessCommandScope("tenant", "A"); });
    await act(async () => host.querySelector("button")!.click());
    expect(request).toHaveBeenCalledTimes(2);
    await act(async () => oldResponse.resolve({ command_id: "original", status: "succeeded", result: {} }));
    expect(host.querySelector("button")?.disabled).toBe(true);
    expect(window.localStorage.getItem(key("A"))).toBe(JSON.stringify({ id: "original" }));
    await act(async () => currentResponse.resolve({ command_id: "original", status: "succeeded", result: {} }));
    expect(window.localStorage.getItem(key("A"))).toBeNull();
    expect(host.textContent).toContain("操作结果已确认");
    expect(host.textContent).toContain("已完成");
    expect(host.querySelector("button")?.textContent).toBe("关闭已确认结果");
  });

  it("only inspects first and sends the frozen original content after an explicit continue", async () => {
    window.localStorage.setItem(key("A"), JSON.stringify({ id: "original" }));
    const payload = { reason: "原发布说明", expected_revision: 3 };
    const request = vi.spyOn(apiClient, "request").mockImplementation(async (path, init) => {
      if (path === "/api/v1/score-commands/original") return { command_id: "original", operation: "score.publish", target_id: "exam", status: "takeover_ready", payload } as never;
      if (path === "/api/v1/exams/exam") return { exam: { name: "期中考试" } } as never;
      if (init?.method === "POST") return {} as never;
      throw new Error(`unexpected ${path}`);
    });
    await act(async () => root.render(<PendingBusinessCommands tenant="tenant" actor="A" />));
    await act(async () => host.querySelector("button")!.click());
    expect(host.textContent).toContain("期中考试"); expect(host.textContent).toContain("原发布说明");
    expect(request.mock.calls.filter(([, init]) => init?.method === "POST")).toHaveLength(0);
    const continueButton = [...host.querySelectorAll<HTMLButtonElement>("button")].find(button => button.textContent === "按以上原内容继续提交")!;
    await act(async () => { continueButton.click(); continueButton.click(); });
    const writes = request.mock.calls.filter(([, init]) => init?.method === "POST");
    expect(writes).toHaveLength(1); expect(writes[0]).toEqual(["/api/v1/exams/exam/publish", { method: "POST", headers: { "Idempotency-Key": "original" }, body: JSON.stringify(payload) }]);
    expect(host.textContent).toContain("已完成"); expect(localStorage.getItem(key("A"))).toBeNull();
  });

  it.each([
    { operation: "review.submit", object: { task: { exam_name: "高一数学期中考试", question_no: "12", anonymous_code: "匿名023" } }, expected: "高一数学期中考试 · 第 12 题 · 答卷 匿名023", destination: "/grading?task=task-id" },
    { operation: "review.arbitrate", object: { arbitration_task: { question_no: "18", anonymous_code: "匿名045" } }, expected: "第 18 题 · 答卷 匿名045", destination: "/arbitration?task=task-id" }
  ])("$operation uses the actual object response and retains navigation after removing the pending key", async ({ operation, object, expected, destination }) => {
    const pendingKey = `business-command:tenant:A:${operation}:task-id`;
    localStorage.setItem(pendingKey, JSON.stringify({ id: "completed-command" }));
    const request = vi.spyOn(apiClient, "request").mockImplementation(async path => {
      if (path.endsWith("commands/completed-command")) return { command_id: "completed-command", status: "succeeded", result: { human_grade: { score: 0 } } } as never;
      return object as never;
    });
    const navigate = vi.fn();
    await act(async () => root.render(<PendingBusinessCommands tenant="tenant" actor="A" onNavigate={navigate} />));
    await act(async () => host.querySelector("button")!.click());
    expect(localStorage.getItem(pendingKey)).toBeNull();
    expect(host.textContent).toContain(expected);
    expect(host.textContent).not.toContain("对象 task-id");
    const returnButton = [...host.querySelectorAll<HTMLButtonElement>("button")].find(item => item.textContent === "返回已完成操作的业务页面")!;
    await act(async () => returnButton.click());
    expect(navigate).toHaveBeenCalledWith(destination);
    expect(request.mock.calls.every(([, init]) => init?.method !== "POST")).toBe(true);
  });

  it("存储属性读取被浏览器禁用时仍能挂载及刷新事件，不误报已完成命令", async () => {
    vi.spyOn(window, "localStorage", "get").mockImplementation(() => { throw new DOMException("storage blocked", "SecurityError"); });
    await act(async () => root.render(<PendingBusinessCommands tenant="tenant" actor="A" />));
    await act(async () => { window.dispatchEvent(new Event("storage")); window.dispatchEvent(new Event("business-command-changed")); });
    expect(host.textContent).toBe("");
  });
});
