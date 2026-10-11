import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import App from "./App";
import { appealDraftKey, readAppealDraft, studentDraftOwner } from "./appealDrafts";
import { deferredResponse, installApi, jsonResponse, makeAppeal, makeQuestion, makeResult, resultRoute, studentUser } from "./test/studentAppBatch8Fixtures";

beforeEach(() => { window.location.hash = "/exams/exam-1"; window.sessionStorage.clear(); vi.spyOn(window, "scrollTo").mockImplementation(() => undefined); });
afterEach(() => { vi.unstubAllGlobals(); vi.restoreAllMocks(); });
const changed = () => jsonResponse({ error: { code: "student_release_changed" } }, 409);
const oldDraftKey = () => appealDraftKey(studentDraftOwner(studentUser), "exam-1", "release-7", "question-1");
const explanation = "请核对第二步已写出的完整计算过程。";

describe("学生证据发布版本与草稿保存状态", () => {
  it("发布切换时阻止混用证据，保留旧稿；明确重新核对后新版表单不自动迁移旧稿", async () => {
    let current = "release-7";
    const oldResult = makeResult([makeQuestion({ feedback: "第七版反馈" })]);
    const newResult = makeResult([makeQuestion({ feedback: "第八版反馈", score: 18 })], { release_id: "release-8", release_version: 8 });
    const calls = installApi((call) => {
      if (call.url.pathname.endsWith("/result")) return jsonResponse({ result: current === "release-7" ? oldResult : newResult });
      if (call.url.pathname.endsWith("/questions/question-1") || call.url.pathname.endsWith("/annotations")) {
        if (call.url.searchParams.get("expected_release_id") !== current) return changed();
        return call.url.pathname.endsWith("/annotations") ? jsonResponse({ annotations: [] }) : jsonResponse({ question: (current === "release-7" ? oldResult : newResult).questions![0] });
      }
      if (call.init.method === "POST") return jsonResponse({ appeal: makeAppeal({ source_release_id: current }) });
      return resultRoute(call, oldResult);
    });
    render(<App />);
    fireEvent.click(await screen.findByRole("button", { name: /^第 1 题，/ }));
    fireEvent.change(await screen.findByRole("textbox", { name: "具体说明" }), { target: { value: explanation } });
    expect(document.querySelector(".answer-image-stage img")?.getAttribute("src")).toContain("expected_release_id=release-7");
    fireEvent.click(screen.getByRole("button", { name: "返回题目列表" }));
    current = "release-8";
    fireEvent.click(screen.getByRole("button", { name: /^第 1 题，/ }));
    const verify = await screen.findByRole("button", { name: "查看当前成绩并重新核对" });
    expect(screen.queryByText("第八版反馈")).toBeNull();
    expect(screen.queryByRole("textbox", { name: "具体说明" })).toBeNull();
    expect(readAppealDraft(oldDraftKey())?.reason).toBe(explanation);
    fireEvent.click(verify);
    fireEvent.click(await screen.findByRole("button", { name: /^第 1 题，18/ }));
    await screen.findByText("第八版反馈");
    expect(screen.getByRole<HTMLTextAreaElement>("textbox", { name: "具体说明" }).value).toBe("");
    fireEvent.change(screen.getByRole("textbox", { name: "具体说明" }), { target: { value: "已经重新核对第八版公开的评分反馈。" } });
    await waitFor(() => expect(screen.getByRole<HTMLButtonElement>("button", { name: "确认提交（仅一次）" }).disabled).toBe(false));
    fireEvent.click(screen.getByRole("button", { name: "确认提交（仅一次）" }));
    await screen.findByText("本题已提交过复核申请，不能重复提交。处理结果会显示在页面下方。");
    const submitted = calls.find((call) => call.init.method === "POST")!;
    expect(JSON.parse(String(submitted.init.body)).source_release_id).toBe("release-8");
    expect(readAppealDraft(oldDraftKey())?.reason).toBe(explanation);
  });

  it("题目读取成功但批注遇到发布切换时，不展示成功证据或申诉表单", async () => {
    installApi((call) => call.url.pathname.endsWith("/annotations") ? changed() : resultRoute(call, makeResult()));
    render(<App />);
    fireEvent.click(await screen.findByRole("button", { name: /^第 1 题，/ }));
    await screen.findByRole("button", { name: "查看当前成绩并重新核对" });
    expect(screen.queryByRole("textbox", { name: "具体说明" })).toBeNull();
    expect(screen.queryByText("第一题公开评分依据")).toBeNull();
  });

  it("重试批注才发现发布切换时，保留原稿并关闭原版本提交入口", async () => {
    let attempts = 0;
    installApi((call) => call.url.pathname.endsWith("/annotations") ? ++attempts === 1 ? jsonResponse({ error: { code: "failed" } }, 503) : changed() : resultRoute(call, makeResult()));
    render(<App />);
    fireEvent.click(await screen.findByRole("button", { name: /^第 1 题，/ }));
    fireEvent.change(await screen.findByRole("textbox", { name: "具体说明" }), { target: { value: explanation } });
    fireEvent.click(screen.getByRole("button", { name: "重试批注" }));
    await screen.findByRole("button", { name: "查看当前成绩并重新核对" });
    expect(screen.queryByRole("textbox", { name: "具体说明" })).toBeNull();
    expect(readAppealDraft(oldDraftKey())?.reason).toBe(explanation);
  });

  it.each(["我的原始答卷", "圈选争议区域"])("题目和批注成功后，%s图片失败的复核409也会保留旧稿并关闭申诉", async (image) => {
    let current = "release-7";
    const calls = installApi((call) => {
      if (call.url.pathname.endsWith("/questions/question-1") && call.url.searchParams.get("expected_release_id") !== current) return changed();
      return resultRoute(call, makeResult());
    });
    render(<App />);
    fireEvent.click(await screen.findByRole("button", { name: /^第 1 题，/ }));
    fireEvent.change(await screen.findByRole("textbox", { name: "具体说明" }), { target: { value: explanation } });
    current = "release-8";
    const selector = image === "我的原始答卷" ? ".answer-image-stage img" : ".appeal-region-image-wrap img";
    fireEvent.error(document.querySelector(selector)!);
    await screen.findByRole("button", { name: "查看当前成绩并重新核对" });
    expect(screen.queryByRole("textbox", { name: "具体说明" })).toBeNull();
    expect(readAppealDraft(oldDraftKey())?.reason).toBe(explanation);
    const reads = calls.filter((call) => call.url.pathname.endsWith("/questions/question-1"));
    expect(reads).toHaveLength(2);
    expect(reads[1].url.searchParams.get("expected_release_id")).toBe("release-7");
    expect(calls.some((call) => call.init.method === "POST")).toBe(false);
  });

  it.each(["bad-image", "network-failure"])("图片失败但复核为%s时，保留原版本文字申诉", async (failure) => {
    let reads = 0;
    const calls = installApi((call) => {
      if (call.url.pathname.endsWith("/questions/question-1") && ++reads > 1 && failure === "network-failure") throw new TypeError("network unavailable");
      if (call.init.method === "POST") return jsonResponse({ appeal: makeAppeal() });
      return resultRoute(call, makeResult());
    });
    render(<App />);
    fireEvent.click(await screen.findByRole("button", { name: /^第 1 题，/ }));
    fireEvent.change(await screen.findByRole("textbox", { name: "具体说明" }), { target: { value: explanation } });
    fireEvent.error(document.querySelector(".answer-image-stage img")!);
    await waitFor(() => expect(reads).toBe(2));
    await screen.findByText("原卷图暂时不可用");
    expect(screen.queryByRole("button", { name: "查看当前成绩并重新核对" })).toBeNull();
    expect(screen.getByRole<HTMLTextAreaElement>("textbox", { name: "具体说明" }).value).toBe(explanation);
    await waitFor(() => expect(screen.getByRole<HTMLButtonElement>("button", { name: "确认提交（仅一次）" }).disabled).toBe(false));
    fireEvent.click(screen.getByRole("button", { name: "确认提交（仅一次）" }));
    await screen.findByText("本题已提交过复核申请，不能重复提交。处理结果会显示在页面下方。");
    const submitted = calls.find((call) => call.init.method === "POST")!;
    expect(JSON.parse(String(submitted.init.body)).source_release_id).toBe("release-7");
  });

  it.each(["changed", "same-version"])("图片复核%s等待期间拒绝同轮强制提交，完成后才决定是否恢复", async (outcome) => {
    const pending = deferredResponse();
    let reads = 0;
    const calls = installApi((call) => {
      if (call.url.pathname.endsWith("/questions/question-1") && ++reads > 1) return pending.promise;
      if (call.init.method === "POST") return jsonResponse({ appeal: makeAppeal() });
      return resultRoute(call, makeResult());
    });
    render(<App />);
    fireEvent.click(await screen.findByRole("button", { name: /^第 1 题，/ }));
    fireEvent.change(await screen.findByRole("textbox", { name: "具体说明" }), { target: { value: explanation } });
    await waitFor(() => expect(screen.getByRole<HTMLButtonElement>("button", { name: "确认提交（仅一次）" }).disabled).toBe(false));
    const form = document.querySelector(".question-appeal-form")!;
    // 图片失败和程序直接提交同轮触发，覆盖按钮 disabled 尚未重绘的窗口。
    act(() => { fireEvent.error(document.querySelector(".answer-image-stage img")!); fireEvent.submit(form); });
    expect(calls.some((call) => call.init.method === "POST")).toBe(false);
    expect(screen.getByRole<HTMLButtonElement>("button", { name: "确认提交（仅一次）" }).disabled).toBe(true);
    await screen.findByText("正在核对答卷所属成绩版本，完成前暂不能提交。");
    await act(async () => pending.resolve(outcome === "changed" ? changed() : jsonResponse({ question: makeQuestion() })));
    if (outcome === "changed") {
      await screen.findByRole("button", { name: "查看当前成绩并重新核对" });
      expect(screen.queryByRole("textbox", { name: "具体说明" })).toBeNull();
      expect(readAppealDraft(oldDraftKey())?.reason).toBe(explanation);
    } else {
      await waitFor(() => expect(screen.getByRole<HTMLButtonElement>("button", { name: "确认提交（仅一次）" }).disabled).toBe(false));
      expect(screen.getByRole<HTMLTextAreaElement>("textbox", { name: "具体说明" }).value).toBe(explanation);
    }
  });

  it("旧题图片的迟到409与收尾不能关闭新题或解除新题的复核保护", async () => {
    const oldPending = deferredResponse();
    const newPending = deferredResponse();
    const reads = new Map<string, number>();
    const result = makeResult([makeQuestion(), makeQuestion({ question_id: "question-2", question_no: "2" })]);
    const calls = installApi((call) => {
      for (const questionID of ["question-1", "question-2"]) {
        if (call.url.pathname.endsWith(`/questions/${questionID}`)) {
          const count = (reads.get(questionID) ?? 0) + 1;
          reads.set(questionID, count);
          if (count > 1) return questionID === "question-1" ? oldPending.promise : newPending.promise;
        }
      }
      return resultRoute(call, result);
    });
    render(<App />);
    fireEvent.click(await screen.findByRole("button", { name: /^第 1 题，/ }));
    await screen.findByRole("textbox", { name: "具体说明" });
    fireEvent.error(document.querySelector(".answer-image-stage img")!);
    fireEvent.click(screen.getByRole("button", { name: "下一失分题" }));
    fireEvent.change(await screen.findByRole("textbox", { name: "具体说明" }), { target: { value: explanation } });
    await waitFor(() => expect(screen.getByRole<HTMLButtonElement>("button", { name: "确认提交（仅一次）" }).disabled).toBe(false));
    fireEvent.error(document.querySelector(".answer-image-stage img")!);
    await act(async () => oldPending.resolve(changed()));
    expect(screen.queryByRole("button", { name: "查看当前成绩并重新核对" })).toBeNull();
    expect(screen.getByRole("heading", { name: "第 2 题 · 作答与评分依据" })).toBeTruthy();
    expect(screen.getByRole<HTMLButtonElement>("button", { name: "确认提交（仅一次）" }).disabled).toBe(true);
    fireEvent.submit(document.querySelector(".question-appeal-form")!);
    expect(calls.some((call) => call.init.method === "POST")).toBe(false);
    await act(async () => newPending.resolve(jsonResponse({ question: result.questions![1] })));
    await waitFor(() => expect(screen.getByRole<HTMLButtonElement>("button", { name: "确认提交（仅一次）" }).disabled).toBe(false));
  });

  it("会话存储拒绝写入时，切题和重新挂载后仍提示仅内存保存", async () => {
    const result = makeResult([makeQuestion(), makeQuestion({ question_id: "question-2", question_no: "2" })]);
    installApi((call) => resultRoute(call, result));
    vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => { throw new DOMException("quota", "QuotaExceededError"); });
    const view = render(<App />);
    fireEvent.click(await screen.findByRole("button", { name: /^第 1 题，/ }));
    fireEvent.change(await screen.findByRole("textbox", { name: "具体说明" }), { target: { value: explanation } });
    const warning = "浏览器未允许保存草稿，当前输入仅在本页保留。刷新或关闭前请先复制说明。";
    await screen.findByText(warning);
    fireEvent.click(screen.getByRole("button", { name: "下一失分题" }));
    await screen.findByRole("heading", { name: "第 2 题 · 作答与评分依据" });
    fireEvent.click(screen.getByRole("button", { name: "上一失分题" }));
    await screen.findByText(warning);
    expect(screen.getByRole<HTMLTextAreaElement>("textbox", { name: "具体说明" }).value).toBe(explanation);
    view.unmount(); render(<App />);
    fireEvent.click(await screen.findByRole("button", { name: /^第 1 题，/ }));
    await screen.findByText(warning);
    expect(screen.getByRole<HTMLTextAreaElement>("textbox", { name: "具体说明" }).value).toBe(explanation);
  });
});
