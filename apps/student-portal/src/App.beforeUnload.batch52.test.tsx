import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import App from "./App";
import { appealDraftKey, removeAppealDraft, studentDraftOwner, writeAppealDraft, type AppealDraft } from "./appealDrafts";
import { deferredResponse, installApi, jsonResponse, makeAppeal, makeResult, resultRoute, studentUser } from "./test/studentAppBatch8Fixtures";

const owner = studentDraftOwner(studentUser);
const draftKey = appealDraftKey(owner, "exam-1", "release-7", "question-1");
const reason = "请核对第二步已经写出的完整计算过程。";
const selectedRegion = { coordinate_space: "canonical_image_normalized", x: .1, y: .2, width: .3, height: .4 } as const;

beforeEach(() => {
  window.history.replaceState(null, "", "/#/");
  window.sessionStorage.clear();
  vi.spyOn(window, "scrollTo").mockImplementation(() => undefined);
});
afterEach(() => { vi.unstubAllGlobals(); vi.restoreAllMocks(); });

// jsdom 中只断言取消标志和属性返回值；实际确认框仍受浏览器用户激活等条件约束。
function dispatchUnload() {
  const event = new Event("beforeunload", { cancelable: true });
  window.dispatchEvent(event);
  return event;
}

function propertyResult() {
  return window.onbeforeunload?.call(window, new Event("beforeunload", { cancelable: true }) as BeforeUnloadEvent);
}

async function mountHome() {
  installApi();
  const view = render(<App />);
  await screen.findByRole("button", { name: "回到我的考试" });
  return view;
}

describe("学生端卸载时的回执和草稿边界", () => {
  it("没有草稿或已写入 sessionStorage 的草稿都不取消卸载", async () => {
    await mountHome();
    expect(dispatchUnload().defaultPrevented).toBe(false);
    expect(propertyResult()).toBeUndefined();
    expect(writeAppealDraft(draftKey, { reasonCode: "recognition_error", reason, selectedRegion })).toBe(true);
    expect(dispatchUnload().defaultPrevented).toBe(false);
    expect(propertyResult()).toBeUndefined();
  });

  it.each([
    { name: "非空理由", draft: { reasonCode: "recognition_error", reason }, blocked: true },
    { name: "仅圈选", draft: { reasonCode: "recognition_error", reason: "", selectedRegion }, blocked: true },
    { name: "只有空白理由", draft: { reasonCode: "recognition_error", reason: "  " }, blocked: false },
  ] satisfies { name: string; draft: AppealDraft; blocked: boolean }[])("会话存储失败后的$name 按内容决定是否拦截", async ({ draft, blocked }) => {
    await mountHome();
    vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => { throw new Error("storage unavailable"); });
    expect(writeAppealDraft(draftKey, draft)).toBe(false);
    expect(dispatchUnload().defaultPrevented).toBe(blocked);
    expect(propertyResult()).toBe(blocked ? "unsaved" : undefined);
    removeAppealDraft(draftKey);
    expect(dispatchUnload().defaultPrevented).toBe(false);
  });

  it("其他身份的内存草稿不拦截当前学生", async () => {
    await mountHome();
    vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => { throw new Error("storage unavailable"); });
    const otherOwner = studentDraftOwner({ ...studentUser, id: "student-user-2" });
    const otherKey = appealDraftKey(otherOwner, "exam-1", "release-7", "question-1");
    expect(writeAppealDraft(otherKey, { reasonCode: "recognition_error", reason, selectedRegion })).toBe(false);
    expect(dispatchUnload().defaultPrevented).toBe(false);
    expect(propertyResult()).toBeUndefined();
  });

  it.each(["success", "failure"] as const)("POST 等待期间同步拦截，%s 回执结束后已存储草稿不再拦截", async (outcome) => {
    window.history.replaceState(null, "", "/#/exams/exam-1");
    const receipt = deferredResponse();
    const calls = installApi((call) => {
      if (call.url.pathname.endsWith("/question-appeals")) {
        return call.init.method === "POST" ? receipt.promise : jsonResponse({ appeals: [] });
      }
      return resultRoute(call, makeResult());
    });
    render(<App />);
    fireEvent.click(await screen.findByRole("button", { name: /^第 1 题，/ }));
    fireEvent.change(await screen.findByRole("textbox", { name: "具体说明" }), { target: { value: reason } });
    const submit = screen.getByRole<HTMLButtonElement>("button", { name: "确认提交（仅一次）" });
    await waitFor(() => expect(submit.disabled).toBe(false));
    expect(dispatchUnload().defaultPrevented).toBe(false);
    fireEvent.click(submit);
    expect(calls.filter((call) => call.init.method === "POST")).toHaveLength(1);
    expect(dispatchUnload().defaultPrevented).toBe(true);
    expect(propertyResult()).toBe("unsaved");
    await act(async () => receipt.resolve(outcome === "success"
      ? jsonResponse({ appeal: makeAppeal({ reason }) })
      : jsonResponse({ error: { code: "temporary_failure" } }, 503)));
    await waitFor(() => expect(screen.queryByText("正在提交复核申请，请等待回执后再切换题目或离开。")).toBeNull());
    expect(dispatchUnload().defaultPrevented).toBe(false);
    expect(propertyResult()).toBeUndefined();
  });

  it("卸载 App 后清理监听并恢复原属性处理器", async () => {
    const previous = window.onbeforeunload;
    const view = await mountHome();
    vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => { throw new Error("storage unavailable"); });
    writeAppealDraft(draftKey, { reasonCode: "recognition_error", reason });
    expect(dispatchUnload().defaultPrevented).toBe(true);
    view.unmount();
    expect(window.onbeforeunload).toBe(previous);
    expect(dispatchUnload().defaultPrevented).toBe(false);
  });
});
