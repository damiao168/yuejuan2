import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import App, { AppealWindow, QuestionAppealForm } from "./App";
import { installApi, jsonResponse, makeAppeal, makeResult, resultRoute } from "./test/studentAppBatch8Fixtures";

beforeEach(() => { window.history.replaceState(null, "", "/#/exams/exam-1"); vi.spyOn(window, "scrollTo").mockImplementation(() => undefined); });
afterEach(() => { vi.unstubAllGlobals(); vi.restoreAllMocks(); });

describe("学生复核申请真实可用性", () => {
  it("服务端原因白名单为空时展示六种原因，选择并提交仍绑定原题原发布", async () => {
    const result = makeResult(undefined, { appeal_window: { open: true, allowed_reason_codes: [] } });
    const calls = installApi((call) => call.init.method === "POST" ? jsonResponse({ appeal: makeAppeal() }) : resultRoute(call, result));
    render(<App />);
    fireEvent.click(await screen.findByRole("button", { name: /^第 1 题，/ }));
    const reasons = await screen.findByRole<HTMLSelectElement>("combobox", { name: "复核原因" });
    expect([...reasons.options].map((option) => option.value)).toEqual(["recognition_error", "missing_step_credit", "rubric_disagreement", "calculation_error", "annotation_issue", "other"]);
    fireEvent.change(reasons, { target: { value: "missing_step_credit" } });
    fireEvent.change(screen.getByRole("textbox", { name: "具体说明" }), { target: { value: "第二步完整列出，申请核对过程分。" } });
    const submit = screen.getByRole<HTMLButtonElement>("button", { name: "确认提交（仅一次）" });
    await waitFor(() => expect(submit.disabled).toBe(false)); fireEvent.click(submit);
    await screen.findByText("学校已收到本题复核申请。");
    expect(screen.getByRole("link", { name: "查看我的申请" })).toBeTruthy();
    const post = calls.find((call) => call.init.method === "POST")!;
    expect(JSON.parse(String(post.init.body))).toMatchObject({ source_release_id: "release-7", question_id: "question-1", reason_code: "missing_step_credit" });
  });

  it("原因全部未知时给出联系说明，不静默隐藏入口或发送空原因", async () => {
    const calls = installApi();
    const view = render(<QuestionAppealForm examID="exam-1" releaseID="release-7" releaseVersion={7} question={{ question_id: "question-1", question_no: "1" }} allowedReasonCodes={["unsupported"]} onSubmitted={vi.fn()} />);
    expect(await screen.findByText("暂时无法申请复核")).toBeTruthy();
    expect(screen.getByText(/请联系学校考试负责人核对设置/)).toBeTruthy();
    expect(view.container.querySelector("form")).toBeNull(); expect(calls.some((call) => call.init.method === "POST")).toBe(false);
  });

  it("历史矛盾发布仅公开总分时说明实际缺少申请对象，不指向不存在的题目表单", () => {
    render(<AppealWindow result={makeResult([], { questions: undefined, appeal_window: { open: true } })} />);
    expect(screen.getByText(/请联系学校考试负责人公开逐题得分后/)).toBeTruthy();
    expect(screen.queryByText(/请在需要复核的题目下提交/)).toBeNull();
  });
});
