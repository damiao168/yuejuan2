// @vitest-environment jsdom
import { act, cleanup, renderHook } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { setBusinessCommandScope } from "../../../api/businessCommand";
import type { ScoreExport } from "../../../api/scores";
import { useScoreAction } from "./useScoreAction";
import { useScoreExport } from "./useScoreExport";

const messages = vi.hoisted(() => ({ success: vi.fn(), error: vi.fn(), warning: vi.fn() }));
vi.mock("antd", () => ({ App: { useApp: () => ({ message: messages }) } }));

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (reason: unknown) => void;
  const promise = new Promise<T>((done, fail) => { resolve = done; reject = fail; });
  return { promise, resolve, reject };
}

const exported: ScoreExport = { blob: new Blob(["synthetic scores"]), contentType: "text/csv", watermark: "synthetic-watermark" };

beforeEach(() => { vi.clearAllMocks(); setBusinessCommandScope("school", "admin"); });
afterEach(() => { cleanup(); setBusinessCommandScope("", ""); });

function mount() {
  const onExport = vi.fn();
  const loadScores = vi.fn().mockResolvedValue(undefined);
  const loadExamList = vi.fn().mockResolvedValue(undefined);
  const hook = renderHook(({ examId, scope, enabled }) => {
    const action = useScoreAction({ selectedExamId: examId, loadScores, loadExamList });
    const runExport = useScoreExport({ ownerKey: JSON.stringify([scope, examId, enabled]), enabled,
      selectedExamId: examId, runAction: action.runAction, onExport });
    return { ...action, runExport };
  }, { initialProps: { examId: "exam-a", scope: "school:admin", enabled: true } });
  return { ...hook, onExport, loadScores };
}

describe("成绩导出回包的页面与会话归属", () => {
  it.each(["export", "export-report", "export-official"])("%s当前请求只下载一次，保留水印并显示成功", async key => {
    const hook = mount();
    const request = vi.fn().mockResolvedValue(exported);
    await act(async () => { await hook.result.current.runExport(key, request, "导出成功", "fallback.csv"); });
    expect(request).toHaveBeenCalledWith("exam-a", expect.any(AbortSignal));
    expect(hook.onExport).toHaveBeenCalledExactlyOnceWith(exported, "fallback.csv");
    expect(messages.success).toHaveBeenCalledExactlyOnceWith("导出成功");
    expect(hook.loadScores).toHaveBeenCalledExactlyOnceWith("exam-a");
    expect(hook.result.current.actioning).toBeNull();
  });

  it.each(["考试变化", "作用域变化", "失去权限", "卸载", "同账号退出重登"])("%s后忽略迟到的成功，不下载或写水印，也不发成功提示", async boundary => {
    const hook = mount();
    const pending = deferred<ScoreExport>();
    const request = vi.fn().mockReturnValue(pending.promise);
    let operation!: Promise<void>;
    act(() => { operation = hook.result.current.runExport("export-official", request, "导出成功", "fallback.csv"); });
    const signal = request.mock.calls[0][1] as AbortSignal;
    if (boundary === "考试变化") hook.rerender({ examId: "exam-b", scope: "school:admin", enabled: true });
    if (boundary === "作用域变化") hook.rerender({ examId: "exam-a", scope: "school:another-admin", enabled: true });
    if (boundary === "失去权限") hook.rerender({ examId: "exam-a", scope: "school:admin", enabled: false });
    if (boundary === "卸载") hook.unmount();
    if (boundary === "同账号退出重登") act(() => { setBusinessCommandScope("", ""); setBusinessCommandScope("school", "admin"); });
    expect(signal.aborted).toBe(true);
    // 模拟已经完成下载或忽略abort的传输；不能只依赖fetch通常会抛AbortError。
    await act(async () => { pending.resolve(exported); await operation; });
    expect(hook.onExport).not.toHaveBeenCalled();
    expect(messages.success).not.toHaveBeenCalled();
    expect(messages.error).not.toHaveBeenCalled();
    expect(hook.loadScores).not.toHaveBeenCalled();
  });

  it("旧归属的失败不会在新考试弹出错误，当前归属的失败仍显示错误", async () => {
    const hook = mount();
    const pending = deferred<ScoreExport>();
    let operation!: Promise<void>;
    act(() => { operation = hook.result.current.runExport("export", () => pending.promise, "导出成功", "fallback.csv"); });
    hook.rerender({ examId: "exam-b", scope: "school:admin", enabled: true });
    await act(async () => { pending.reject(new Error("late failure")); await operation; });
    expect(messages.error).not.toHaveBeenCalled();
    await act(async () => { await hook.result.current.runExport("export-report", async () => { throw new Error("当前导出失败"); }, "导出成功", "fallback.csv"); });
    expect(messages.error).toHaveBeenCalledExactlyOnceWith("当前导出失败");
  });

  it("同一归属同步双击只请求一次，换考试后旧操作不能清掉新操作的忙碌状态", async () => {
    const hook = mount();
    const previous = deferred<ScoreExport>();
    const next = deferred<ScoreExport>();
    const request = vi.fn().mockReturnValue(previous.promise);
    let oldOperation!: Promise<void>;
    act(() => {
      oldOperation = hook.result.current.runExport("export", request, "旧导出", "old.csv");
      void hook.result.current.runExport("export", request, "旧导出", "old.csv");
    });
    expect(request).toHaveBeenCalledOnce();
    hook.rerender({ examId: "exam-b", scope: "school:admin", enabled: true });
    let newOperation!: Promise<void>;
    act(() => { newOperation = hook.result.current.runExport("export-official", () => next.promise, "新导出", "new.csv"); });
    await act(async () => { previous.resolve(exported); await oldOperation; });
    expect(hook.result.current.actioning).toBe("export-official");
    expect(hook.onExport).not.toHaveBeenCalled();
    await act(async () => { next.resolve(exported); await newOperation; });
    expect(hook.onExport).toHaveBeenCalledExactlyOnceWith(exported, "new.csv");
    expect(messages.success).toHaveBeenCalledExactlyOnceWith("新导出");
    expect(hook.result.current.actioning).toBeNull();
  });
});
