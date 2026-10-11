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
  const promise = new Promise<T>(onResolve => { resolve = onResolve; });
  return { promise, resolve };
}

function readBlobBytes(blob: Blob): Promise<number[]> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => resolve([...new Uint8Array(reader.result as ArrayBuffer)]);
    reader.onerror = () => reject(reader.error);
    reader.readAsArrayBuffer(blob);
  });
}

const reportKey = "business-command:tenant:A:report.export:exam";
const reportResult = { Content: "AH+A/w==", ContentType: "application/octet-stream", Filename: "原报表.csv", Watermark: "原水印" };

describe("pending command result and report boundaries", () => {
  let host: HTMLDivElement;
  let root: Root;
  let filenames: string[];
  const createURL = vi.fn<(blob: Blob) => string>(() => "blob:command-report");
  const revokeURL = vi.fn<(url: string) => void>();

  beforeEach(() => {
    Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
    localStorage.clear();
    setBusinessCommandScope("tenant", "A");
    createURL.mockClear();
    revokeURL.mockClear();
    filenames = [];
    const NativeURL = URL;
    vi.stubGlobal("URL", Object.assign(class extends NativeURL {}, { createObjectURL: createURL, revokeObjectURL: revokeURL }));
    vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(function (this: HTMLAnchorElement) { filenames.push(this.download); });
    host = document.createElement("div");
    document.body.append(host);
    root = createRoot(host);
  });

  afterEach(async () => {
    await act(async () => root.unmount());
    host.remove();
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
    setBusinessCommandScope("", "");
  });

  function button(text: string): HTMLButtonElement {
    const result = [...host.querySelectorAll<HTMLButtonElement>("button")].find(item => item.textContent === text);
    expect(result).toBeDefined();
    return result!;
  }

  it("shows score zero and the exact target when its name is unreadable without clearing a replacement command", async () => {
    const target = "exam:part/中文";
    const key = `business-command:tenant:A:score.publish:${target}`;
    localStorage.setItem(key, JSON.stringify({ id: "original" }));
    const response = deferred<unknown>();
    const request = vi.spyOn(apiClient, "request").mockImplementation(async path => {
      if (path === "/api/v1/score-commands/original") return response.promise as Promise<never>;
      if (path === `/api/v1/exams/${encodeURIComponent(target)}`) throw new Error("名称无读取权限");
      throw new Error(`unexpected ${path}`);
    });
    const navigate = vi.fn();
    await act(async () => root.render(<PendingBusinessCommands tenant="tenant" actor="A" onNavigate={navigate} />));
    await act(async () => button("查看操作结果").click());
    // 回执尚未返回时同一键已换成新命令；旧成功结果只能展示，不能删除新 ID。
    localStorage.setItem(key, JSON.stringify({ id: "replacement" }));
    await act(async () => response.resolve({ command_id: "original", operation: "score.publish", target_id: target, status: "succeeded", result: { human_grade: { score: 0 }, final_grade: { score: 9 } } }));
    expect(localStorage.getItem(key)).toBe(JSON.stringify({ id: "replacement" }));
    expect(host.textContent).toContain(`考试 ${target}`);
    const outputs = [...host.querySelectorAll("output")];
    expect(outputs.map(item => item.textContent)).toEqual(["已完成", `成绩发布已完成 · 对象 ${target} · 得分 0`]);
    expect(outputs.every(item => item.getAttribute("aria-live") === "polite" && !item.querySelector("button"))).toBe(true);
    expect(outputs[1].parentElement?.tagName).toBe("DIV");
    expect(host.querySelector('[role="alert"]')).toBeNull();
    expect(request.mock.calls.every(([, init]) => init?.method !== "POST")).toBe(true);
    await act(async () => button("返回对应业务页面").click());
    expect(navigate).toHaveBeenCalledWith(`/exams/${encodeURIComponent(target)}/scores`);
  });

  it("downloads completed receipt content as exact binary bytes with its original filename", async () => {
    localStorage.setItem(reportKey, JSON.stringify({ id: "report-original" }));
    const request = vi.spyOn(apiClient, "request").mockImplementation(async path => {
      if (path === "/api/v1/report-commands/report-original") return { command_id: "report-original", status: "succeeded", result: reportResult } as never;
      if (path === "/api/v1/exams/exam") return { exam: { name: "报表考试" } } as never;
      throw new Error(`unexpected ${path}`);
    });
    const timeout = vi.spyOn(window, "setTimeout");
    await act(async () => root.render(<PendingBusinessCommands tenant="tenant" actor="A" />));
    await act(async () => button("查看操作结果").click());
    expect(createURL).not.toHaveBeenCalled();
    expect(localStorage.getItem(reportKey)).toBeNull();
    await act(async () => button("下载已完成的报表").click());
    expect(createURL).toHaveBeenCalledTimes(1);
    const blob = createURL.mock.calls[0][0];
    expect(blob.type).toBe(reportResult.ContentType);
    expect(await readBlobBytes(blob)).toEqual([0, 127, 128, 255]);
    expect(filenames).toEqual([reportResult.Filename]);
    expect(request.mock.calls.every(([, init]) => init?.method !== "POST")).toBe(true);
    const schedule = timeout.mock.calls.findIndex(([, delay]) => delay === 1000);
    expect(schedule).toBeGreaterThanOrEqual(0);
    window.clearTimeout(timeout.mock.results[schedule].value as number);
    const release = timeout.mock.calls[schedule][0];
    expect(typeof release).toBe("function");
    if (typeof release === "function") release();
    expect(revokeURL).toHaveBeenCalledWith("blob:command-report");
  });

  it("recovers an already completed report after explicit continue using the same bytes and filename fallback", async () => {
    localStorage.setItem(reportKey, JSON.stringify({ id: "report-original" }));
    let receiptReads = 0;
    const request = vi.spyOn(apiClient, "request").mockImplementation(async path => {
      if (path === "/api/v1/report-commands/report-original") {
        receiptReads += 1;
        if (receiptReads === 1) return { command_id: "report-original", status: "takeover_ready", payload: { format: "csv" } } as never;
        // 用户核对期间原请求已完成；继续时读取冻结结果，不能再发送 POST。
        return { command_id: "report-original", status: "succeeded", result: { Content: reportResult.Content, ContentType: reportResult.ContentType, Watermark: reportResult.Watermark } } as never;
      }
      if (path === "/api/v1/exams/exam") return { exam: { name: "报表考试" } } as never;
      throw new Error(`unexpected ${path}`);
    });
    const requestBlob = vi.spyOn(apiClient, "requestBlob");
    const timeout = vi.spyOn(window, "setTimeout");
    await act(async () => root.render(<PendingBusinessCommands tenant="tenant" actor="A" />));
    await act(async () => button("查看操作结果").click());
    expect(createURL).not.toHaveBeenCalled();
    const continueButton = button("按以上原内容继续提交");
    await act(async () => { continueButton.click(); continueButton.click(); });
    expect(receiptReads).toBe(2);
    expect(requestBlob).not.toHaveBeenCalled();
    expect(request.mock.calls.every(([, init]) => init?.method !== "POST")).toBe(true);
    expect(createURL).toHaveBeenCalledTimes(1);
    const blob = createURL.mock.calls[0][0];
    expect(blob.type).toBe(reportResult.ContentType);
    expect(await readBlobBytes(blob)).toEqual([0, 127, 128, 255]);
    expect(filenames).toEqual(["report.csv"]);
    expect(localStorage.getItem(reportKey)).toBeNull();
    expect(host.querySelector("output")?.textContent).toBe("报表导出已完成 · 对象 报表考试");
    const schedule = timeout.mock.calls.findIndex(([, delay]) => delay === 1000);
    expect(schedule).toBeGreaterThanOrEqual(0);
    window.clearTimeout(timeout.mock.results[schedule].value as number);
  });
});
