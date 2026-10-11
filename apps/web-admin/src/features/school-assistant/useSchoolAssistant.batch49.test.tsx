// @vitest-environment jsdom
import { act, cleanup, renderHook } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { SchoolAssistantCapabilities, SchoolAssistantEvent, SchoolAssistantQuery } from "../../api/schoolAssistant";
import { useSchoolAssistant } from "./useSchoolAssistant";

const api = vi.hoisted(() => ({ capabilities: vi.fn(), stream: vi.fn() }));
vi.mock("../../api/schoolAssistant", () => ({
  getSchoolAssistantCapabilities: api.capabilities,
  streamSchoolAssistantQuery: api.stream
}));

function capabilities(available = true): SchoolAssistantCapabilities {
  return { mode: "school_business", model_required: false, intents: [
    { intent: "exam_progress", label: "考试进度", available, required_permission: "exam:manage" },
    { intent: "publish_diagnosis", label: "发布诊断", available, required_permission: "exam:manage" }
  ] };
}

describe("学校助手 Context 所用状态与命令", () => {
  beforeEach(() => {
    api.capabilities.mockReset().mockResolvedValue(capabilities());
    api.stream.mockReset().mockResolvedValue(undefined);
  });
  afterEach(() => { cleanup(); });

  it("相同账户重新渲染保留对象和命令，刷新仍重新读取权限并取消旧读取", async () => {
    const view = renderHook(({ accountKey }) => useSchoolAssistant(accountKey), { initialProps: { accountKey: "owner" } });
    await act(async () => undefined);
    expect(view.result.current.capabilitiesLoading).toBe(false);
    const initial = view.result.current;
    const firstSignal = api.capabilities.mock.calls[0][0] as AbortSignal;
    view.rerender({ accountKey: "owner" });
    expect(view.result.current).toBe(initial);
    expect(view.result.current.refreshCapabilities).toBe(initial.refreshCapabilities);
    expect(view.result.current.retry).toBe(initial.retry);
    await act(async () => view.result.current.refreshCapabilities());
    expect(api.capabilities).toHaveBeenCalledTimes(2);
    expect(firstSignal.aborted).toBe(true);
    expect(view.result.current.capabilitiesLoading).toBe(false);
  });

  it("重试采用刷新后的权限，切换考试清空旧请求，发布诊断仍携带学生端渠道", async () => {
    const view = renderHook(() => useSchoolAssistant("owner"));
    await act(async () => undefined);
    await act(async () => view.result.current.setContext({ exam_id: "exam-a", exam_name: "考试 A" }));
    await act(async () => view.result.current.run("exam_progress", "查看进度"));
    expect(api.stream).toHaveBeenCalledTimes(1);
    const firstRequest = api.stream.mock.calls[0][0] as SchoolAssistantQuery;
    expect(firstRequest.context_hint).toEqual({ exam_id: "exam-a" });
    expect(view.result.current.query.status).toBe("error");
    const firstRetry = view.result.current.retry;
    api.capabilities.mockResolvedValueOnce(capabilities(false));
    await act(async () => view.result.current.refreshCapabilities());
    expect(view.result.current.retry).not.toBe(firstRetry);
    await act(async () => view.result.current.retry());
    expect(api.stream).toHaveBeenCalledTimes(1);
    api.capabilities.mockResolvedValueOnce(capabilities());
    await act(async () => view.result.current.refreshCapabilities());
    await act(async () => view.result.current.retry());
    expect(api.stream).toHaveBeenCalledTimes(2);
    expect(api.stream.mock.calls[1][0]).toMatchObject({ question: "查看进度", context_hint: { exam_id: "exam-a" } });
    expect(api.stream.mock.calls[1][0].request_id).not.toBe(firstRequest.request_id);
    await act(async () => view.result.current.setContext({ exam_id: "exam-b" }));
    await act(async () => view.result.current.retry());
    expect(api.stream).toHaveBeenCalledTimes(2);
    await act(async () => view.result.current.run("publish_diagnosis", "为什么不能发布"));
    expect(api.stream.mock.calls[2][0]).toMatchObject({ context_hint: { exam_id: "exam-b", release_channel: "student_portal" } });
  });

  it("账户切换取消查询并拒绝旧事件，重试不会重放旧账户请求", async () => {
    let emit!: (event: SchoolAssistantEvent) => void;
    let finish!: () => void;
    let request!: SchoolAssistantQuery;
    let signal!: AbortSignal;
    api.stream.mockImplementation((nextRequest: SchoolAssistantQuery, nextSignal: AbortSignal, onEvent: (event: SchoolAssistantEvent) => void) => {
      request = nextRequest; signal = nextSignal; emit = onEvent;
      return new Promise<void>((resolve) => { finish = resolve; });
    });
    const view = renderHook(({ accountKey }) => useSchoolAssistant(accountKey), { initialProps: { accountKey: "owner" } });
    await act(async () => undefined);
    let pending!: Promise<void>;
    await act(async () => { pending = view.result.current.run("exam_progress", "旧账户查询"); });
    expect(view.result.current.query.status).toBe("running");
    // 模拟网络层忽略 abort 后仍送回事件；旧账户事件不能影响新账户的空状态。
    view.rerender({ accountKey: "next-owner" });
    await act(async () => undefined);
    expect(signal.aborted).toBe(true);
    await act(async () => emit({ type: "source_started", request_id: request.request_id, seq: 1, source_id: "old", label: "旧账户来源" }));
    expect(view.result.current.query).toEqual({ status: "idle", sources: [], progress: "", error: "" });
    await act(async () => view.result.current.retry());
    expect(api.stream).toHaveBeenCalledOnce();
    await act(async () => { finish(); await pending; });
    expect(view.result.current.query.status).toBe("idle");
  });
});
