import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import App from "./App";
import { appealDraftKey, clearStudentAppealDrafts, readAppealDraft, studentDraftOwner } from "./appealDrafts";
import { deferredResponse, installApi, jsonResponse, makeAppeal, makeQuestion, makeResult, resultRoute, studentUser } from "./test/studentAppBatch8Fixtures";

const prefix = "  第一段：请核对原卷第二步写出的完整计算过程。\n";
const suffix = "\n第二段：上述内容属于原题原版，不能自动带到新版。  ";
const explanation = prefix + "证".repeat(500 - prefix.length - suffix.length) + suffix;
const recovery = "查看当前成绩并重新核对";
const receiptName = "第 1 题复核申请回执";
const originalReasonName = "第 1 题 · 第 7 版原说明（只读）";
const draftKey = appealDraftKey(studentDraftOwner(studentUser), "exam-1", "release-7", "question-1");
const changed = () => jsonResponse({ error: { code: "student_release_changed" } }, 409);
const unavailable = () => jsonResponse({ error: { code: "temporary_failure" } }, 503);
const isAppeal = (path: string) => path.endsWith("/question-appeals");

beforeEach(() => {
  window.history.replaceState(null, "", "/#/exams/exam-1");
  clearStudentAppealDrafts();
  window.sessionStorage.clear();
  vi.spyOn(window, "scrollTo").mockImplementation(() => undefined);
});

afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
  clearStudentAppealDrafts();
});

type Failure = "service" | "network" | "session";

function installReceiptScenario(failure: Failure = "service") {
  const annotation = deferredResponse();
  const receipt = deferredResponse();
  let annotationReads = 0;
  let recordsUnavailable = false;
  let result = makeResult();
  const calls = installApi((call) => {
    if (isAppeal(call.url.pathname) && call.init.method === "POST") {
      return receipt.promise.then((response) => {
        if (failure === "network") throw new TypeError("Failed to fetch");
        return response;
      });
    }
    if (isAppeal(call.url.pathname)) return recordsUnavailable ? unavailable() : jsonResponse({ appeals: [] });
    if (call.url.pathname.endsWith("/questions/question-1/annotations")) {
      return ++annotationReads === 1 ? unavailable() : annotationReads === 2 ? annotation.promise : jsonResponse({ annotations: [] });
    }
    return resultRoute(call, result);
  });
  return {
    annotation,
    receipt,
    calls,
    failReceipt: () => receipt.resolve(failure === "session" ? jsonResponse({ error: { code: "unauthenticated" } }, 401) : unavailable()),
    setRecordsUnavailable: () => { recordsUnavailable = true; },
    publishNext: () => { result = makeResult([makeQuestion({ score: 18, feedback: "第八版当前评分依据" })], { release_id: "release-8", release_version: 8 }); }
  };
}

async function startWithAnnotationRetry(storageAllowed: boolean) {
  fireEvent.click(await screen.findByRole("button", { name: /^第 1 题，/ }));
  const input = await screen.findByRole<HTMLTextAreaElement>("textbox", { name: "具体说明" });
  if (!storageAllowed) {
    vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => {
      throw new DOMException("Storage blocked", "SecurityError");
    });
  }
  fireEvent.change(input, { target: { value: explanation } });
  fireEvent.load(screen.getByAltText("本题答题区域"));
  fireEvent.click(screen.getByRole("button", { name: "选择整个答题区域" }));
  const submit = screen.getByRole<HTMLButtonElement>("button", { name: "确认提交（仅一次）" });
  await waitFor(() => expect(submit.disabled).toBe(false));
  // 批注重试先发出，POST提交后才返回换版；旧证据关闭以后仍需取回原说明。
  fireEvent.click(screen.getByRole("button", { name: "重试批注" }));
  fireEvent.click(submit);
}

function assertOriginalReasonCanBeSelected() {
  const region = screen.getByRole("region", { name: receiptName });
  const input = within(region).getByRole<HTMLTextAreaElement>("textbox", { name: originalReasonName });
  expect(input.value).toBe(explanation);
  expect(input.value).toHaveLength(500);
  expect(input.readOnly).toBe(true);
  expect(input.disabled).toBe(false);
  fireEvent.click(within(region).getByRole("button", { name: "选中原说明" }));
  expect(document.activeElement).toBe(input);
  expect(input.selectionStart).toBe(0);
  expect(input.selectionEnd).toBe(500);
  expect(within(region).queryByRole("button", { name: "确认提交（仅一次）" })).toBeNull();
  expect(screen.queryByRole("textbox", { name: "具体说明" })).toBeNull();
  expect(screen.queryByAltText("本题答题区域")).toBeNull();
  return region;
}

function posts(scenario: ReturnType<typeof installReceiptScenario>) {
  return scenario.calls.filter((call) => isAppeal(call.url.pathname) && call.init.method === "POST");
}

describe("原版复核回执的说明取回", () => {
  it.each([
    { failure: "service", stored: false, label: "存储被拒绝且服务503" },
    { failure: "network", stored: false, label: "存储被拒绝且网络断开" },
    { failure: "session", stored: false, label: "存储被拒绝且登录401" },
    { failure: "service", stored: true, label: "会话存储正常但服务503" }
  ] as const)("$label：失败后完整说明可取回，新版不继承原草稿", async ({ failure, stored }) => {
    const scenario = installReceiptScenario(failure);
    render(<App />);
    await startWithAnnotationRetry(stored);
    const originalDraft = readAppealDraft(draftKey);
    await act(async () => scenario.annotation.resolve(changed()));
    await act(async () => scenario.failReceipt());

    const region = assertOriginalReasonCanBeSelected();
    expect(within(region).getByRole("alert").textContent).toContain(failure === "session" ? "登录状态已失效" : "暂时无法连接服务");
    expect(readAppealDraft(draftKey)).toEqual(originalDraft);
    expect(originalDraft?.selectedRegion?.width).toBe(1);
    if (!stored) expect(within(region).getByText(/浏览器未允许保存草稿/)).toBeTruthy();
    else expect(within(region).getByText(/草稿已保存在当前标签页会话/)).toBeTruthy();
    expect(posts(scenario)).toHaveLength(1);
    expect(JSON.parse(posts(scenario)[0].init.body as string)).toMatchObject({ source_release_id: "release-7", question_id: "question-1", reason: explanation.trim(), selected_region: originalDraft?.selectedRegion });

    if (failure === "session") {
      expect(within(region).getByRole<HTMLButtonElement>("button", { name: "重新登录" }).disabled).toBe(false);
      return;
    }
    scenario.publishNext();
    fireEvent.click(screen.getByRole("button", { name: recovery }));
    fireEvent.click(await screen.findByRole("button", { name: /^第 1 题，18/ }));
    await screen.findByText("第八版当前评分依据");
    expect(screen.getByRole<HTMLTextAreaElement>("textbox", { name: "具体说明" }).value).toBe("");
    expect(screen.queryByRole("region", { name: receiptName })).toBeNull();
    expect(readAppealDraft(draftKey)).toEqual(originalDraft);
    expect(readAppealDraft(appealDraftKey(studentDraftOwner(studentUser), "exam-1", "release-8", "question-1"))).toBeUndefined();
    expect(posts(scenario)).toHaveLength(1);
  });

  it("换版后POST仍等待时可以选中原说明，不恢复旧证据和旧版重发入口", async () => {
    const scenario = installReceiptScenario();
    render(<App />);
    await startWithAnnotationRetry(false);
    await act(async () => scenario.annotation.resolve(changed()));
    const region = assertOriginalReasonCanBeSelected();
    expect(within(region).getByText("正在提交第 7 版的复核申请，请等待回执。")).toBeTruthy();
    expect(screen.getByRole<HTMLButtonElement>("button", { name: recovery }).disabled).toBe(true);
    expect(posts(scenario)).toHaveLength(1);
    await act(async () => scenario.failReceipt());
    expect(within(region).getByRole("alert").textContent).toContain("暂时无法连接服务");
  });

  it("原版POST确认成功后清除草稿和取回区，后续记录查询503不能恢复失败导出", async () => {
    const scenario = installReceiptScenario();
    render(<App />);
    await startWithAnnotationRetry(false);
    await act(async () => scenario.annotation.resolve(changed()));
    assertOriginalReasonCanBeSelected();
    scenario.setRecordsUnavailable();
    await act(async () => scenario.receipt.resolve(jsonResponse({ appeal: makeAppeal({ reason: explanation.trim() }) })));
    await screen.findByText("本题已提交过复核申请，不能重复提交。处理结果会显示在页面下方。");
    await screen.findByText(/申请记录暂时无法加载/);
    expect(readAppealDraft(draftKey)).toBeUndefined();
    expect(screen.queryByRole("textbox", { name: originalReasonName })).toBeNull();
    expect(screen.queryByRole("button", { name: "选中原说明" })).toBeNull();
    expect(screen.queryByText(/浏览器未允许保存草稿/)).toBeNull();
    expect(posts(scenario)).toHaveLength(1);
  });
});
