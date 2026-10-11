// @vitest-environment jsdom
import { render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { AppealStatus, AppealWindow } from "./App";
import type { StudentResult } from "./api";
import { installApi, jsonResponse, makeAppeal, makeResult, resultRoute } from "./test/studentAppBatch8Fixtures";

afterEach(() => { vi.unstubAllGlobals(); vi.restoreAllMocks(); });

const closesAt = "2030-10-02T00:00:00Z";
const closeLabel = new Intl.DateTimeFormat("zh-CN", { year: "numeric", month: "numeric", day: "numeric", hour: "2-digit", minute: "2-digit", hour12: false }).format(new Date(closesAt));

describe("学生复核展示的日期和历史分边界", () => {
  it.each<{ name: string; window: StudentResult["appeal_window"]; expected: string }>([
    { name: "即将开始但日期为空", window: { open: false, state: "upcoming", opens_at: "", closes_at: "" }, expected: "复核将在 学校指定时间 开始。" },
    { name: "已结束且有截止时间", window: { open: false, state: "closed", opens_at: "invalid", closes_at: closesAt }, expected: `本次成绩的复核窗口已于 ${closeLabel}结束。` },
    { name: "已结束但没有截止时间", window: { open: false, state: "closed" }, expected: "本次成绩的复核窗口已结束。" },
    // 无效日期放在未使用的状态分支，保护惰性格式化，不能因无关字段抛出异常。
    { name: "未开放时忽略无关日期", window: { open: false, state: "disabled", opens_at: "invalid", closes_at: "invalid" }, expected: "学校当前未开放本次成绩的复核窗口。" },
  ])("$name", ({ window, expected }) => {
    const view = render(<AppealWindow result={makeResult(undefined, { appeal_window: window })} onStart={vi.fn()} />);
    expect(view.container.querySelector(".appeal-notice p")?.textContent).toBe(expected);
    expect(screen.queryByRole("button", { name: "申请成绩复核" })).toBeNull();
  });

  it.each([
    { name: "缺历史得分", reviewedScore: undefined, reviewedMaxScore: 20, sourceScore: 14, expected: "历史复核分暂不可读取，请核对学校回复" },
    { name: "缺历史满分", reviewedScore: 0, reviewedMaxScore: undefined, sourceScore: 14, expected: "历史复核分暂不可读取，请核对学校回复" },
    { name: "小数相等", reviewedScore: 0.5, reviewedMaxScore: 2.75, sourceScore: 0.5, expected: "0.5 / 2.8（分数未变更）" },
  ])("$name不由当前成绩补齐", async ({ reviewedScore, reviewedMaxScore, sourceScore, expected }) => {
    const appeal = makeAppeal({ status: "resolved", reviewed_score: reviewedScore, reviewed_max_score: reviewedMaxScore, source_score: sourceScore });
    installApi((call) => call.url.pathname.endsWith("question-appeals") ? jsonResponse({ appeals: [appeal] }) : resultRoute(call, makeResult()));
    render(<AppealStatus examID="exam-1" revision={0} />);
    const historical = await screen.findByText(/^原得分：/);
    expect(historical.textContent).toContain(`；复核后得分：${expected}。`);
    expect(historical.textContent).not.toContain("；复核后得分：14 / 20");
    expect(await screen.findByText("本题当前成绩：14")).toBeTruthy();
  });
});
