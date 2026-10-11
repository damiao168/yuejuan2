import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import type { ComponentProps } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import App, { QuestionAppealForm } from "./App";
import { appealDraftKey, readAppealDraft, studentDraftOwner, writeAppealDraft } from "./appealDrafts";
import { StudentSessionContext } from "./studentSession";
import { deferredResponse, installApi, jsonResponse, makeAppeal, makeQuestion, makeResult, resultRoute, studentUser } from "./test/studentAppBatch8Fixtures";

const owner = studentDraftOwner(studentUser);
const draftKey = appealDraftKey(owner, "exam-1", "release-7", "question-1");
const explanation = "请核对第二步已经写出的完整计算过程。";
const confirmation = "本题已提交过复核申请，不能重复提交。处理结果会显示在页面下方。";

function form(props: Partial<ComponentProps<typeof QuestionAppealForm>> = {}) {
  return <StudentSessionContext value={{ draftOwner: owner, submitting: false, setSubmitting: vi.fn(), reauthenticate: vi.fn() }}>
    <QuestionAppealForm examID="exam-1" releaseID="release-7" releaseVersion={7} question={{ question_id: "question-1", question_no: "1" }} allowedReasonCodes={["recognition_error", "rubric_disagreement"]} onSubmitted={vi.fn()} {...props} />
  </StudentSessionContext>;
}

beforeEach(() => {
  window.history.replaceState(null, "", "/#/exams/exam-1");
  window.sessionStorage.clear();
  vi.spyOn(window, "scrollTo").mockImplementation(() => undefined);
});
afterEach(() => { vi.unstubAllGlobals(); vi.restoreAllMocks(); });

describe("学生复核表单展示职责", () => {
  it("没有可用原因时不显示表单，已确认的既有申请仍优先显示", async () => {
    installApi(() => jsonResponse({ appeals: [makeAppeal()] }));
    const view = render(form({ allowedReasonCodes: ["unknown"] }));
    expect(view.container.querySelector("form")).toBeNull();
    await screen.findByText(confirmation);
    expect(screen.queryByRole("textbox", { name: "具体说明" })).toBeNull();
  });

  it("申请记录加载失败时保留输入和重试入口，重试成功后恢复提交", async () => {
    let reads = 0;
    installApi(() => ++reads === 1 ? jsonResponse({ error: { code: "temporary_failure" } }, 503) : jsonResponse({ appeals: [] }));
    render(form());
    fireEvent.change(screen.getByRole("textbox", { name: "具体说明" }), { target: { value: explanation } });
    const retry = await screen.findByRole("button", { name: "重试申请记录" });
    expect(screen.getByRole<HTMLButtonElement>("button", { name: "确认提交（仅一次）" }).disabled).toBe(true);
    fireEvent.click(retry);
    await waitFor(() => expect(screen.getByRole<HTMLButtonElement>("button", { name: "确认提交（仅一次）" }).disabled).toBe(false));
    expect(screen.getByRole<HTMLTextAreaElement>("textbox", { name: "具体说明" }).value).toBe(explanation);
    expect(reads).toBe(2);
  });

  it("输入区沿用原发布草稿和区域，成功提交仍清理原稿并只通知一次", async () => {
    const selectedRegion = { coordinate_space: "canonical_image_normalized", x: 0.1, y: 0.2, width: 0.3, height: 0.4 } as const;
    writeAppealDraft(draftKey, { reasonCode: "obsolete", reason: ` ${explanation} `, selectedRegion });
    const calls = installApi((call) => call.init.method === "POST" ? jsonResponse({ appeal: makeAppeal() }) : undefined);
    const onSubmitted = vi.fn();
    render(form({ onSubmitted }));
    expect(screen.getByRole<HTMLSelectElement>("combobox", { name: "复核原因" }).value).toBe("recognition_error");
    expect(screen.getByText("已圈选争议区域")).toBeTruthy();
    const submit = screen.getByRole<HTMLButtonElement>("button", { name: "确认提交（仅一次）" });
    await waitFor(() => expect(submit.disabled).toBe(false));
    fireEvent.click(submit);
    await screen.findByText(confirmation);
    const posts = calls.filter((call) => call.init.method === "POST");
    expect(posts).toHaveLength(1);
    expect(JSON.parse(String(posts[0].init.body))).toEqual({ source_release_id: "release-7", question_id: "question-1", reason_code: "recognition_error", reason: explanation, selected_region: selectedRegion });
    expect(onSubmitted).toHaveBeenCalledTimes(1);
    expect(readAppealDraft(draftKey)).toBeUndefined();
  });

  it("正在核对版本时展示等待原因，直接提交事件也不发送申请", async () => {
    const calls = installApi();
    const view = render(form({ evidenceChecking: true }));
    fireEvent.change(screen.getByRole("textbox", { name: "具体说明" }), { target: { value: explanation } });
    await waitFor(() => expect(screen.queryByText("正在核对已有复核申请…")).toBeNull());
    expect(screen.getByText("正在核对答卷所属成绩版本，完成前暂不能提交。")).toBeTruthy();
    expect(screen.getByRole<HTMLButtonElement>("button", { name: "确认提交（仅一次）" }).disabled).toBe(true);
    fireEvent.submit(view.container.querySelector("form")!);
    expect(calls.some((call) => call.init.method === "POST")).toBe(false);
  });

  it("换版后保留已发申请的等待及失败回执，原稿不迁移也不提供重发", async () => {
    // 同一父表单在 POST 等待时改为回执展示，展示组件切换不能清除提交事实或原版本草稿。
    const receipt = deferredResponse();
    const calls = installApi((call) => call.init.method === "POST" ? receipt.promise : undefined);
    const view = render(form());
    fireEvent.change(screen.getByRole("textbox", { name: "具体说明" }), { target: { value: explanation } });
    const submit = screen.getByRole<HTMLButtonElement>("button", { name: "确认提交（仅一次）" });
    await waitFor(() => expect(submit.disabled).toBe(false));
    fireEvent.click(submit);
    view.rerender(form({ evidenceUnavailable: true }));
    const region = screen.getByRole("region", { name: "第 1 题复核申请回执" });
    expect(within(region).getByText("正在提交第 7 版的复核申请，请等待回执。")).toBeTruthy();
    expect(screen.queryByRole("textbox", { name: "具体说明" })).toBeNull();
    await act(async () => receipt.resolve(jsonResponse({ error: { code: "temporary_failure" } }, 503)));
    await within(region).findByRole("alert");
    expect(within(region).getByText(/草稿已保存在当前标签页会话/)).toBeTruthy();
    expect(screen.queryByRole("button", { name: "确认提交（仅一次）" })).toBeNull();
    expect(readAppealDraft(draftKey)?.reason).toBe(explanation);
    expect(calls.filter((call) => call.init.method === "POST")).toHaveLength(1);
  });

  it("未发申请的证据失效保留只读草稿，不声称已发申请", async () => {
    installApi();
    const view = render(form({ evidenceUnavailable: true }));
    await act(async () => { await Promise.resolve(); });
    expect(view.container.querySelector("form")).toBeNull();
    expect(screen.getByRole("heading", { name: "原版本复核草稿" })).toBeTruthy();
    expect(screen.queryByRole("heading", { name: "复核申请回执" })).toBeNull();
  });

  it("切换学科时关闭原题详情，重新打开的题目仍使用当前发布版本", async () => {
    const result = makeResult([makeQuestion(), makeQuestion({ question_id: "question-2", question_no: "2", subject: "chinese" })]);
    const calls = installApi((call) => resultRoute(call, result));
    render(<App />);
    fireEvent.click(await screen.findByRole("button", { name: /^第 1 题，/ }));
    await screen.findByRole("textbox", { name: "具体说明" });
    fireEvent.click(screen.getByRole("button", { name: "语文" }));
    expect(screen.queryByRole("heading", { name: "第 1 题 · 作答与评分依据" })).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: /^第 2 题，/ }));
    await screen.findByRole("heading", { name: "第 2 题 · 作答与评分依据" });
    const reads = calls.filter((call) => call.url.pathname.endsWith("/questions/question-2"));
    expect(reads).toHaveLength(1);
    expect(reads[0].url.searchParams.get("expected_release_id")).toBe("release-7");
  });
});
