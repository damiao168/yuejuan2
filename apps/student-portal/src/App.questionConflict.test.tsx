import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import App from "./App";
import { appealDraftKey, readAppealDraft, studentDraftOwner } from "./appealDrafts";
import { deferredResponse, installApi, jsonResponse, makeAppeal, makeQuestion, makeResult, resultRoute, studentUser } from "./test/studentAppBatch8Fixtures";

const changed = () => jsonResponse({ error: { code: "student_release_changed" } }, 409);
const failed = () => jsonResponse({ error: { code: "temporary_failure" } }, 503);
const recovery = "查看当前成绩并重新核对";
const explanation = "请核对第二步已经写出的完整计算过程。";
const isQuestion = (path: string) => path.endsWith("/questions/question-1");
const isAnnotation = (path: string) => path.endsWith("/questions/question-1/annotations");

beforeEach(() => {
  window.location.hash = "/exams/exam-1";
  window.sessionStorage.clear();
  vi.spyOn(window, "scrollTo").mockImplementation(() => undefined);
});
afterEach(() => { vi.unstubAllGlobals(); vi.restoreAllMocks(); });

describe("逐题证据即时冲突恢复", () => {
  it.each(["question", "annotation"])("%s先确认换版时立即停止加载，另一份迟到成功不能恢复旧表单", async (source) => {
    const pending = deferredResponse();
    const result = makeResult();
    const calls = installApi((call) => {
      if (isQuestion(call.url.pathname)) return source === "question" ? changed() : pending.promise;
      if (isAnnotation(call.url.pathname)) return source === "annotation" ? changed() : pending.promise;
      return resultRoute(call, result);
    });
    render(<App />);
    fireEvent.click(await screen.findByRole("button", { name: /^第 1 题，/ }));
    // 另一份请求仍挂起；恢复入口必须独立于 allSettled 的收尾出现。
    await screen.findByRole("button", { name: recovery });
    expect(screen.queryByText("正在加载本题…")).toBeNull();
    expect(screen.queryByRole("textbox", { name: "具体说明" })).toBeNull();
    expect(calls.filter((call) => call.url.pathname.endsWith("/result"))).toHaveLength(1);
    await act(async () => pending.resolve(source === "question" ? jsonResponse({ annotations: [] }) : jsonResponse({ question: makeQuestion() })));
    expect(screen.getByRole("button", { name: recovery })).toBeTruthy();
    expect(screen.queryByText("第一题公开评分依据")).toBeNull();
    expect(screen.queryByRole("textbox", { name: "具体说明" })).toBeNull();
    expect(calls.some((call) => call.init.method === "POST")).toBe(false);
  });

  it.each(["question", "annotation"])("%s先确认换版后，另一份普通失败不替换已知冲突", async (source) => {
    const pending = deferredResponse();
    installApi((call) => {
      if (isQuestion(call.url.pathname)) return source === "question" ? changed() : pending.promise;
      if (isAnnotation(call.url.pathname)) return source === "annotation" ? changed() : pending.promise;
      return resultRoute(call, makeResult());
    });
    render(<App />);
    fireEvent.click(await screen.findByRole("button", { name: /^第 1 题，/ }));
    await screen.findByRole("button", { name: recovery });
    await act(async () => pending.resolve(failed()));
    expect(screen.getByRole("button", { name: recovery })).toBeTruthy();
    expect(screen.queryByRole("button", { name: "重试本题" })).toBeNull();
    expect(screen.queryByRole("textbox", { name: "具体说明" })).toBeNull();
  });

  it("切题后旧题的迟到冲突与收尾不能关闭新题", async () => {
    const oldQuestion = deferredResponse();
    const oldAnnotations = deferredResponse();
    const result = makeResult([makeQuestion(), makeQuestion({ question_id: "question-2", question_no: "2", feedback: "第二题当前评分依据" })]);
    installApi((call) => {
      if (isQuestion(call.url.pathname)) return oldQuestion.promise;
      if (isAnnotation(call.url.pathname)) return oldAnnotations.promise;
      return resultRoute(call, result);
    });
    render(<App />);
    fireEvent.click(await screen.findByRole("button", { name: /^第 1 题，/ }));
    fireEvent.click(screen.getByRole("button", { name: /^第 2 题，/ }));
    await screen.findByText("第二题当前评分依据");
    await act(async () => oldQuestion.resolve(changed()));
    expect(screen.queryByRole("button", { name: recovery })).toBeNull();
    expect(screen.getByRole("textbox", { name: "具体说明" })).toBeTruthy();
    await act(async () => oldAnnotations.resolve(jsonResponse({ annotations: [] })));
    expect(screen.getByText("第二题当前评分依据")).toBeTruthy();
    expect(screen.queryByRole("button", { name: recovery })).toBeNull();
  });

  it("关闭题目后旧请求确认换版不能重新打开冲突提示", async () => {
    const oldQuestion = deferredResponse();
    const oldAnnotations = deferredResponse();
    installApi((call) => {
      if (isQuestion(call.url.pathname)) return oldQuestion.promise;
      if (isAnnotation(call.url.pathname)) return oldAnnotations.promise;
      return resultRoute(call, makeResult());
    });
    render(<App />);
    fireEvent.click(await screen.findByRole("button", { name: /^第 1 题，/ }));
    fireEvent.click(screen.getByRole("button", { name: "返回题目列表" }));
    await act(async () => oldQuestion.resolve(changed()));
    await act(async () => oldAnnotations.resolve(jsonResponse({ annotations: [] })));
    expect(screen.queryByRole("button", { name: recovery })).toBeNull();
    expect(screen.queryByText("正在加载本题…")).toBeNull();
    expect(screen.queryByRole("heading", { name: "第 1 题 · 作答与评分依据" })).toBeNull();
  });

  it("离开成绩页面后旧题的迟到冲突不能污染考试列表", async () => {
    const oldQuestion = deferredResponse();
    const oldAnnotations = deferredResponse();
    installApi((call) => {
      if (isQuestion(call.url.pathname)) return oldQuestion.promise;
      if (isAnnotation(call.url.pathname)) return oldAnnotations.promise;
      return resultRoute(call, makeResult());
    });
    render(<App />);
    fireEvent.click(await screen.findByRole("button", { name: /^第 1 题，/ }));
    fireEvent.click(screen.getByRole("button", { name: "← 返回我的考试" }));
    await waitFor(() => expect(document.querySelector(".exam-list-page")).toBeTruthy());
    await act(async () => oldAnnotations.resolve(changed()));
    await act(async () => oldQuestion.resolve(jsonResponse({ question: makeQuestion() })));
    expect(document.querySelector(".exam-list-page")).toBeTruthy();
    expect(screen.queryByRole("button", { name: recovery })).toBeNull();
    expect(screen.queryByRole("textbox", { name: "具体说明" })).toBeNull();
  });

  it("即时冲突保留旧稿，明确核对后新版不继承草稿或旧请求结果", async () => {
    const lateAnnotations = deferredResponse();
    const oldResult = makeResult();
    const newResult = makeResult([makeQuestion({ feedback: "第八版评分依据", score: 18 })], { release_id: "release-8", release_version: 8 });
    let phase: "old" | "conflict" | "new" = "old";
    const calls = installApi((call) => {
      if (call.url.pathname.endsWith("/result")) return jsonResponse({ result: phase === "new" ? newResult : oldResult });
      if (phase === "conflict" && isQuestion(call.url.pathname)) return changed();
      if (phase === "conflict" && isAnnotation(call.url.pathname)) return lateAnnotations.promise;
      return resultRoute(call, phase === "new" ? newResult : oldResult);
    });
    render(<App />);
    fireEvent.click(await screen.findByRole("button", { name: /^第 1 题，/ }));
    fireEvent.change(await screen.findByRole("textbox", { name: "具体说明" }), { target: { value: explanation } });
    const key = appealDraftKey(studentDraftOwner(studentUser), "exam-1", "release-7", "question-1");
    fireEvent.click(screen.getByRole("button", { name: "返回题目列表" }));
    phase = "conflict";
    fireEvent.click(screen.getByRole("button", { name: /^第 1 题，/ }));
    const reload = await screen.findByRole("button", { name: recovery });
    expect(readAppealDraft(key)?.reason).toBe(explanation);
    expect(calls.filter((call) => call.url.pathname.endsWith("/result"))).toHaveLength(1);
    phase = "new";
    fireEvent.click(reload);
    fireEvent.click(await screen.findByRole("button", { name: /^第 1 题，18/ }));
    await screen.findByText("第八版评分依据");
    await act(async () => lateAnnotations.resolve(changed()));
    expect(screen.queryByRole("button", { name: recovery })).toBeNull();
    expect(screen.getByRole<HTMLTextAreaElement>("textbox", { name: "具体说明" }).value).toBe("");
    expect(readAppealDraft(key)?.reason).toBe(explanation);
    expect(calls.filter((call) => call.url.pathname.endsWith("/result"))).toHaveLength(2);
    const questionReads = calls.filter((call) => isQuestion(call.url.pathname));
    expect(questionReads[questionReads.length - 1]?.url.searchParams.get("expected_release_id")).toBe("release-8");
  });

  it("普通题目读取失败保留重试本题入口，不误报换版", async () => {
    let reads = 0;
    installApi((call) => isQuestion(call.url.pathname) && ++reads === 1 ? failed() : resultRoute(call, makeResult()));
    render(<App />);
    fireEvent.click(await screen.findByRole("button", { name: /^第 1 题，/ }));
    const retry = await screen.findByRole("button", { name: "重试本题" });
    expect(screen.queryByRole("button", { name: recovery })).toBeNull();
    expect(screen.queryByRole("textbox", { name: "具体说明" })).toBeNull();
    fireEvent.click(retry);
    await screen.findByRole("textbox", { name: "具体说明" });
    expect(reads).toBe(2);
  });

  it("普通批注失败仍允许查看同版题目和保留草稿，单独重试批注", async () => {
    let reads = 0;
    const calls = installApi((call) => isAnnotation(call.url.pathname) && ++reads === 1 ? failed() : resultRoute(call, makeResult()));
    render(<App />);
    fireEvent.click(await screen.findByRole("button", { name: /^第 1 题，/ }));
    const reason = await screen.findByRole<HTMLTextAreaElement>("textbox", { name: "具体说明" });
    fireEvent.change(reason, { target: { value: explanation } });
    fireEvent.click(screen.getByRole("button", { name: "重试批注" }));
    await waitFor(() => expect(screen.queryByRole("button", { name: "重试批注" })).toBeNull());
    expect(reason.value).toBe(explanation);
    expect(screen.getByText("第一题公开评分依据")).toBeTruthy();
    expect(screen.queryByRole("button", { name: recovery })).toBeNull();
    expect(calls.filter((call) => isQuestion(call.url.pathname))).toHaveLength(1);
  });

  it("批注重试确认换版时，提交中的当前成绩重载仍被禁止", async () => {
    const annotation = deferredResponse();
    const receipt = deferredResponse();
    let annotationReads = 0;
    const calls = installApi((call) => {
      if (call.init.method === "POST") return receipt.promise;
      if (isAnnotation(call.url.pathname)) return ++annotationReads === 1 ? failed() : annotation.promise;
      return resultRoute(call, makeResult());
    });
    render(<App />);
    fireEvent.click(await screen.findByRole("button", { name: /^第 1 题，/ }));
    fireEvent.change(await screen.findByRole("textbox", { name: "具体说明" }), { target: { value: explanation } });
    await waitFor(() => expect(screen.getByRole<HTMLButtonElement>("button", { name: "确认提交（仅一次）" }).disabled).toBe(false));
    fireEvent.click(screen.getByRole("button", { name: "重试批注" }));
    fireEvent.click(screen.getByRole("button", { name: "确认提交（仅一次）" }));
    await act(async () => annotation.resolve(changed()));
    const reload = await screen.findByRole<HTMLButtonElement>("button", { name: recovery });
    expect(reload.disabled).toBe(true);
    fireEvent.click(reload);
    expect(calls.filter((call) => call.url.pathname.endsWith("/result"))).toHaveLength(1);
    await act(async () => receipt.resolve(jsonResponse({ appeal: makeAppeal() })));
    await waitFor(() => expect(reload.disabled).toBe(false));
    expect(calls.filter((call) => call.init.method === "POST")).toHaveLength(1);
  });
});
