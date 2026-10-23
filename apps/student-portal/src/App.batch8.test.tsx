import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import App from "./App";
import type { StudentQuestion } from "./api";
import {
  dataResponse,
  deferredResponse,
  installApi,
  jsonResponse,
  makeAppeal,
  makeQuestion,
  makeResult,
  resultRoute,
  studentUser,
  type ApiHandler
} from "./test/studentAppBatch8Fixtures";

beforeEach(() => {
  window.history.replaceState(null, "", "/#/");
  window.localStorage.clear();
});

afterEach(() => {
  vi.useRealTimers();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

async function renderResult(questions: StudentQuestion[] = [makeQuestion()], handler?: ApiHandler) {
  const result = makeResult(questions);
  window.history.replaceState(null, "", "/#/exams/exam-1");
  const calls = installApi(async (call) => await handler?.(call) ?? resultRoute(call, result));
  render(<App />);
  await screen.findByLabelText("总分");
  return { calls, result };
}

async function openFirstQuestion() {
  fireEvent.click(screen.getByRole("button", { name: /^第 1 题，/ }));
  await screen.findByRole("heading", { name: "申请复核" });
}

function cohort(median: number): NonNullable<StudentQuestion["cohort"]> {
  return {
    sample_size: 25,
    mean_score_rate: 0.6,
    full_score_rate: 0.3,
    zero_score_rate: 0.1,
    class_mean_score: 5,
    school_mean_score: 6,
    median_score: median
  };
}

describe("Batch8 equivalent student portal behavior", () => {
  it.each([
    [0, "夜深了"], [5, "夜深了"], [6, "早上好"], [10, "早上好"],
    [11, "中午好"], [13, "中午好"], [14, "下午好"], [17, "下午好"],
    [18, "晚上好"], [23, "晚上好"]
  ] as const)("preserves local-hour greeting at %i", async (hour, greeting) => {
    vi.useFakeTimers({ toFake: ["Date"] });
    vi.setSystemTime(new Date(2026, 9, 4, hour, 0));
    installApi();
    render(<App />);
    expect(await screen.findByRole("heading", { name: `${studentUser.display_name}，${greeting}` })).toBeTruthy();
  });

  it.each([
    ["/exams/exam-1", "exam-1"],
    ["/exams/exam-1?view=detail", "exam-1"],
    ["/exams/%E6%95%B0%E5%AD%A6%20%2F%20A", "数学 / A"],
    ["/exams/percent%252Fid", "percent%2Fid"]
  ] as const)("decodes exactly one exam route segment %s", async (hash, examID) => {
    window.history.replaceState(null, "", `/#${hash}`);
    const path = `/api/v1/student/exams/${encodeURIComponent(examID)}/result`;
    const calls = installApi((call) => call.url.pathname === path ? dataResponse({ result: makeResult([], { exam_id: examID }) }) : undefined);
    render(<App />);
    await screen.findByLabelText("总分");
    expect(calls.filter((call) => call.url.pathname === path)).toHaveLength(1);
    expect(calls.every((call) => call.init.credentials === "include")).toBe(true);
  });

  it.each(["", "/", "/exams/", "/exams/exam-1/extra", "/exams/%E0%A4%A"])("returns malformed or non-exam route %s to home", async (hash) => {
    window.history.replaceState(null, "", `/#${hash}`);
    const calls = installApi();
    render(<App />);
    await screen.findByText("学校尚未发布你的成绩");
    expect(calls.some((call) => call.url.pathname.endsWith("/result"))).toBe(false);
  });

  it("re-reads hash changes and preserves the existing exam mount boundary", async () => {
    installApi((call) => resultRoute(call, makeResult()));
    render(<App />);
    await screen.findByText("学校尚未发布你的成绩");
    act(() => {
      window.history.replaceState(null, "", "/#/exams/exam-1");
      window.dispatchEvent(new HashChangeEvent("hashchange"));
    });
    await screen.findByLabelText("总分");
    act(() => {
      window.history.replaceState(null, "", "/#/exams/%E0%A4%A");
      window.dispatchEvent(new HashChangeEvent("hashchange"));
    });
    await screen.findByText("学校尚未发布你的成绩");
    expect(screen.queryByLabelText("总分")).toBeNull();
  });

  it("sorts local subject groups numerically without changing the supplied questions", async () => {
    const questions = [
      makeQuestion({ question_id: "math-10", question_no: "10" }),
      makeQuestion({ question_id: "english-2", question_no: "2", subject: "english" }),
      makeQuestion({ question_id: "math-2", question_no: "2" }),
      makeQuestion({ question_id: "math-1", question_no: "1" })
    ];
    const references = [...questions];
    const original = questions.map((question) => ({ ...question }));
    await renderResult(questions);
    const tabs = screen.getByRole("navigation", { name: "科目" });
    expect(within(tabs).getAllByRole("button").map((button) => button.textContent)).toEqual(["数学", "英语"]);
    expect(screen.getAllByRole("button", { name: /^第 \d+ 题，/ }).map((button) => button.getAttribute("aria-label"))).toEqual([
      "第 1 题，14 / 20 分", "第 2 题，14 / 20 分", "第 10 题，14 / 20 分"
    ]);
    fireEvent.click(within(tabs).getByRole("button", { name: "英语" }));
    expect(screen.getAllByRole("button", { name: /^第 \d+ 题，/ })).toHaveLength(1);
    expect(questions).toEqual(original);
    questions.forEach((question, index) => expect(question).toBe(references[index]));
  });

  it.each([
    [undefined, 5, "unknown", "暂无对比数据"],
    [Number.NaN, 5, "unknown", "暂无对比数据"],
    [Number.POSITIVE_INFINITY, 5, "unknown", "暂无对比数据"],
    [Number.NEGATIVE_INFINITY, 5, "unknown", "暂无对比数据"],
    [0, 0, "mastered", "达到群体中位分"],
    [-1, 0, "mastered", "达到群体中位分"],
    [5, 5, "mastered", "达到群体中位分"],
    [5, 4.5, "review", "低于群体中位分"]
  ] as const)("preserves median boundary %s with score %s", async (median, score, className, label) => {
    const question = makeQuestion({ score, cohort: median === undefined ? undefined : cohort(median) });
    await renderResult([question]);
    const comparison = screen.getByText(label);
    expect(comparison.classList.contains(className)).toBe(true);
    expect(screen.queryByText("已掌握")).toBeNull();
  });

  it("keeps row identity and keyboard focus on a normal parent rerender", async () => {
    await renderResult();
    const rowButton = screen.getByRole("button", { name: /^第 1 题，/ });
    rowButton.focus();
    fireEvent.click(screen.getByRole("button", { name: "大字阅读" }));
    expect(screen.getByRole("button", { name: /^第 1 题，/ })).toBe(rowButton);
    expect(document.activeElement).toBe(rowButton);
    expect(rowButton.getAttribute("aria-expanded")).toBe("false");
  });

  it("keeps the appeal draft, focus, region and selected row on a parent rerender", async () => {
    const { calls } = await renderResult();
    await openFirstQuestion();
    const reason = screen.getByRole<HTMLTextAreaElement>("textbox", { name: "具体说明" });
    fireEvent.change(reason, { target: { value: "请核对原卷第二步中的识别内容。" } });
    fireEvent.change(screen.getByRole("combobox", { name: "复核原因" }), { target: { value: "rubric_disagreement" } });
    fireEvent.load(screen.getByAltText("本题答题区域"));
    fireEvent.click(screen.getByRole("button", { name: "选择整个答题区域" }));
    reason.focus();
    const questionRequests = calls.filter((call) => call.url.pathname.endsWith("/questions/question-1")).length;
    fireEvent.click(screen.getByRole("button", { name: "大字阅读" }));
    expect(screen.getByRole("textbox", { name: "具体说明" })).toBe(reason);
    expect(document.activeElement).toBe(reason);
    expect(reason.value).toBe("请核对原卷第二步中的识别内容。");
    expect(screen.getByRole<HTMLSelectElement>("combobox", { name: "复核原因" }).value).toBe("rubric_disagreement");
    expect(screen.getByText("已圈选争议区域")).toBeTruthy();
    expect(screen.getByRole("button", { name: /^第 1 题，/ }).getAttribute("aria-expanded")).toBe("true");
    expect(calls.filter((call) => call.url.pathname.endsWith("/questions/question-1"))).toHaveLength(questionRequests);
  });

  it("ignores late question responses after changing subject and after unmount", async () => {
    const oldResponse = deferredResponse();
    const first = makeQuestion();
    const second = makeQuestion({ question_id: "english-2", question_no: "2", subject: "english" });
    await renderResult([first, second], (call) => call.url.pathname.endsWith("/questions/question-1") ? oldResponse.promise : undefined);
    fireEvent.click(screen.getByRole("button", { name: /^第 1 题，/ }));
    fireEvent.click(screen.getByRole("button", { name: /^英语$/ }));
    await act(async () => oldResponse.resolve(dataResponse({ question: { ...first, feedback: "过期的第一题评分依据" } })));
    expect(screen.queryByText("过期的第一题评分依据")).toBeNull();
    expect(screen.queryByRole("heading", { name: "申请复核" })).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: /^数学$/ }));
    const unmountedResponse = deferredResponse();
    installApi((call) => call.url.pathname.endsWith("/questions/question-1") ? unmountedResponse.promise : resultRoute(call, makeResult([first, second])));
    fireEvent.click(screen.getByRole("button", { name: /^第 1 题，/ }));
    act(() => {
      window.history.replaceState(null, "", "/#/");
      window.dispatchEvent(new HashChangeEvent("hashchange"));
    });
    await screen.findByText("学校尚未发布你的成绩");
    await act(async () => unmountedResponse.resolve(dataResponse({ question: first })));
    expect(screen.queryByRole("heading", { name: "申请复核" })).toBeNull();
  });

  it("retains appeal input and selected region after a failed submission and retries the same release", async () => {
    let submissions = 0;
    const { calls } = await renderResult(undefined, (call) => {
      if (call.url.pathname === "/api/v1/student/exams/exam-1/question-appeals" && call.init.method === "POST") {
        submissions += 1;
        if (submissions === 1) return jsonResponse({ error: { code: "request_failed", message: "PRIVATE_UPSTREAM_DETAIL" } }, 500);
        return jsonResponse({ appeal: makeAppeal() });
      }
      return undefined;
    });
    await openFirstQuestion();
    const reason = screen.getByRole<HTMLTextAreaElement>("textbox", { name: "具体说明" });
    fireEvent.change(reason, { target: { value: "  请核对原卷第二步中的识别内容。  " } });
    fireEvent.load(screen.getByAltText("本题答题区域"));
    fireEvent.click(screen.getByRole("button", { name: "选择整个答题区域" }));
    const submit = screen.getByRole<HTMLButtonElement>("button", { name: "确认提交（仅一次）" });
    // 表单显示时已有申请查询可能仍未结束；等待按钮启用后再验证首次提交失败和重试。
    await waitFor(() => expect(submit.disabled).toBe(false));
    fireEvent.click(submit);
    await screen.findByText("暂时无法连接服务，请稍后重试。");
    expect(reason.value).toBe("  请核对原卷第二步中的识别内容。  ");
    expect(screen.getByText("已圈选争议区域")).toBeTruthy();
    expect(screen.queryByText("PRIVATE_UPSTREAM_DETAIL")).toBeNull();
    expect(submit.disabled).toBe(false);
    fireEvent.click(submit);
    await screen.findByText("本题已提交过复核申请，不能重复提交。处理结果会显示在页面下方。");
    const submitted = calls.filter((call) => call.init.method === "POST");
    expect(submitted).toHaveLength(2);
    for (const call of submitted) {
      expect(call.init.credentials).toBe("include");
      expect(JSON.parse(String(call.init.body))).toEqual({
        source_release_id: "release-7",
        question_id: "question-1",
        reason_code: "recognition_error",
        reason: "请核对原卷第二步中的识别内容。",
        selected_region: { coordinate_space: "canonical_image_normalized", x: 0, y: 0, width: 1, height: 1 }
      });
    }
    expect(screen.queryByRole("textbox", { name: "具体说明" })).toBeNull();
  });

  it("keeps disabled submission state on a parent rerender", async () => {
    const pending = deferredResponse();
    await renderResult(undefined, (call) => call.init.method === "POST" ? pending.promise : undefined);
    await openFirstQuestion();
    const reason = screen.getByRole("textbox", { name: "具体说明" });
    expect(screen.getByRole<HTMLButtonElement>("button", { name: "确认提交（仅一次）" }).disabled).toBe(true);
    fireEvent.change(reason, { target: { value: "请核对原卷第二步中的识别内容。" } });
    const submit = screen.getByRole<HTMLButtonElement>("button", { name: "确认提交（仅一次）" });
    // 表单标题先于已有申请查询结束出现；等待按钮启用后，点击才会进入待回执的提交状态。
    await waitFor(() => expect(submit.disabled).toBe(false));
    fireEvent.click(submit);
    await screen.findByRole("button", { name: "正在提交…" });
    fireEvent.click(screen.getByRole("button", { name: "大字阅读" }));
    expect(screen.getByRole("textbox", { name: "具体说明" })).toBe(reason);
    expect(screen.getByRole<HTMLButtonElement>("button", { name: "正在提交…" }).disabled).toBe(true);
    await act(async () => pending.resolve(jsonResponse({ appeal: makeAppeal() })));
    await screen.findByText("本题已提交过复核申请，不能重复提交。处理结果会显示在页面下方。");
  });

  it.each([
    { roles: ["teacher"], permissions: ["student:grade:read"] },
    { roles: ["student"], permissions: [] }
  ])("still requires both student role and grade permission %j", async (identity) => {
    const calls = installApi(undefined, { ...studentUser, ...identity });
    render(<App />);
    await screen.findByRole("heading", { name: identity.roles.includes("student") ? "学生账号尚未开通查分权限" : "此账号不是学生账号" });
    expect(calls).toHaveLength(1);
    expect(calls[0].url.pathname).toBe("/api/v1/auth/me");
    expect(screen.queryByLabelText("总分")).toBeNull();
  });
});
