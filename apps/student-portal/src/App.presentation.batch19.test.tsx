import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import App, { AppealStatus, QuestionAppealForm, appealReleaseState } from "./App";
import { appealDraftKey, readAppealDraft, studentDraftOwner } from "./appealDrafts";
import { StudentSessionContext } from "./studentSession";
import { deferredResponse, installApi, jsonResponse, makeAppeal, makeQuestion, makeResult, resultRoute, studentUser } from "./test/studentAppBatch8Fixtures";

beforeEach(() => {
  window.history.replaceState(null, "", "/#/exams/exam-1");
  window.sessionStorage.clear();
  window.localStorage.clear();
});

afterEach(() => {
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

function expectNativeStatus(element: HTMLElement, parentClass?: string) {
  expect(element.tagName).toBe("OUTPUT");
  expect(element.getAttribute("aria-live")).toBe("polite");
  expect(element.parentElement?.tagName).toBe("P");
  expect(screen.getAllByRole("status")).toContain(element);
  if (parentClass) expect(element.parentElement?.classList.contains(parentClass)).toBe(true);
}

function renderOwnedAppealForm() {
  const owner = studentDraftOwner(studentUser);
  render(<StudentSessionContext value={{ draftOwner: owner, submitting: false, setSubmitting: vi.fn(), reauthenticate: vi.fn() }}>
    <QuestionAppealForm examID="exam-1" releaseID="release-7" releaseVersion={7} question={makeQuestion()} allowedReasonCodes={["recognition_error"]} onSubmitted={vi.fn()} />
  </StudentSessionContext>);
  return appealDraftKey(owner, "exam-1", "release-7", "question-1");
}

describe("student appeal presentation boundaries", () => {
  it("keeps submission and reauthentication notices as native status outputs", async () => {
    const pending = deferredResponse();
    installApi((call) => call.init.method === "POST" ? pending.promise : resultRoute(call, makeResult()));
    render(<App />);
    fireEvent.click(await screen.findByRole("button", { name: /^第 1 题，/ }));
    fireEvent.change(await screen.findByRole("textbox", { name: "具体说明" }), { target: { value: "请核对原卷第二步已经写出的计算过程。" } });
    await waitFor(() => expect(screen.getByRole<HTMLButtonElement>("button", { name: "确认提交（仅一次）" }).disabled).toBe(false));
    fireEvent.click(screen.getByRole("button", { name: "确认提交（仅一次）" }));
    // 先让提交保持未决，再返回 401，覆盖忙提示与重登提示的两次显示边界。
    expectNativeStatus(await screen.findByText("正在提交复核申请，请等待回执后再切换题目或离开。"), "submission-notice");
    expect(screen.getByRole<HTMLButtonElement>("button", { name: "退出登录" }).disabled).toBe(true);
    expect(screen.getByRole<HTMLButtonElement>("button", { name: "← 返回我的考试" }).disabled).toBe(true);
    await act(async () => pending.resolve(jsonResponse({ error: { code: "unauthenticated" } }, 401)));
    expect(screen.queryByText("正在提交复核申请，请等待回执后再切换题目或离开。")).toBeNull();
    fireEvent.click(await screen.findByRole("button", { name: "重新登录" }));
    expectNativeStatus(await screen.findByText("请重新登录。未提交的复核草稿会在同一账号登录后恢复。"));
    expect(screen.getByRole("heading", { name: "查看已发布成绩" })).toBeTruthy();
  });

  it("keeps the existing-appeal loading output and submission guard until the check resolves", async () => {
    const pending = deferredResponse();
    installApi((call) => call.url.pathname === "/api/v1/student/question-appeals" ? pending.promise : undefined);
    renderOwnedAppealForm();
    expectNativeStatus(screen.getByText("正在核对已有复核申请…"));
    fireEvent.change(screen.getByRole("textbox", { name: "具体说明" }), { target: { value: "请核对原卷第二步已经写出的计算过程。" } });
    expect(screen.getByRole<HTMLButtonElement>("button", { name: "确认提交（仅一次）" }).disabled).toBe(true);
    await act(async () => pending.resolve(jsonResponse({ appeals: [] })));
    expect(screen.queryByText("正在核对已有复核申请…")).toBeNull();
    expect(screen.getByRole<HTMLButtonElement>("button", { name: "确认提交（仅一次）" }).disabled).toBe(false);
  });

  it.each([
    { stored: true, expected: "草稿已保存在当前标签页会话，切题、返回或重新登录后可恢复；退出登录将清除。", className: "muted" },
    { stored: false, expected: "浏览器未允许保存草稿，当前输入仅在本页保留。刷新或关闭前请先复制说明。", className: "detail-error" },
  ])("keeps the draft status, input and region-only condition when stored=$stored", async ({ stored, expected, className }) => {
    installApi();
    const storageKey = renderOwnedAppealForm();
    await waitFor(() => expect(screen.queryByText("正在核对已有复核申请…")).toBeNull());
    expect(screen.queryByText(expected)).toBeNull();
    if (!stored) vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => { throw new Error("storage unavailable"); });
    const reason = screen.getByRole<HTMLTextAreaElement>("textbox", { name: "具体说明" });
    const value = "请核对原卷第二步已经写出的计算过程。";
    fireEvent.change(reason, { target: { value } });
    expectNativeStatus(screen.getByText(expected), className);
    expect(reason.value).toBe(value);
    expect(readAppealDraft(storageKey)?.reason).toBe(value);
    fireEvent.change(reason, { target: { value: "" } });
    expect(screen.queryByText(expected)).toBeNull();
    fireEvent.load(screen.getByAltText("本题答题区域"));
    fireEvent.click(screen.getByRole("button", { name: "选择整个答题区域" }));
    expectNativeStatus(screen.getByText(expected), className);
    expect(reason.value).toBe("");
    expect(screen.getByRole<HTMLButtonElement>("button", { name: "确认提交（仅一次）" }).disabled).toBe(true);
  });

  it.each([
    { decision: "reject", status: "resolved", expected: "处理结论：本次申请未被支持。" },
    { decision: "refer_regrade", status: "rejected", expected: "处理结论：本次申请未被支持。" },
    { decision: "refer_regrade", status: "submitted", expected: "处理结论：已支持复核，转入重评。" },
    { decision: undefined, status: "upheld_pending_regrade", expected: "处理结论：已支持复核，转入重评。" },
    { decision: undefined, status: "resolved", expected: "处理结论：已支持复核，转入重评。" },
    { decision: undefined, status: "submitted", expected: "处理结论：等待学校复核。" },
  ] as const)("keeps the rendered conclusion for decision=$decision and status=$status", async ({ decision, status, expected }) => {
    const appeal = makeAppeal({ decision, status });
    installApi((call) => call.url.pathname === "/api/v1/student/question-appeals" ? jsonResponse({ appeals: [appeal] }) : resultRoute(call, makeResult()));
    render(<AppealStatus examID="exam-1" revision={0} />);
    expect(await screen.findByText(expected)).toBeTruthy();
  });

  it.each([
    { name: "zero score", questions: [makeQuestion({ score: 0, max_score: 20 })], suffix: "，本题当前得分 0 / 20 分" },
    { name: "fractional score", questions: [makeQuestion({ score: 0.5, max_score: 2.75 })], suffix: "，本题当前得分 0.5 / 2.8 分" },
    { name: "no matching question", questions: [makeQuestion({ question_id: "other" })], suffix: "" },
  ])("keeps the full matching-release text with $name", ({ questions, suffix }) => {
    const appeal = makeAppeal({ status: "resolved", new_release_id: "release-9", new_release_version: 9 });
    const current = makeResult(questions, { release_id: "release-9", release_version: 9 });
    expect(appealReleaseState(appeal, current)).toBe(`本次处理关联的第 9 版已是当前生效成绩${suffix}。`);
  });
});
