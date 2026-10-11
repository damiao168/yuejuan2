import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import App from "./App";
import { appealDraftKey, readAppealDraft, studentDraftOwner } from "./appealDrafts";
import type { useStudentSession } from "./studentSession";
import { deferredResponse, installApi, jsonResponse, makeAppeal, makeQuestion, makeResult, resultRoute, studentUser } from "./test/studentAppBatch8Fixtures";

const sessionProbe = vi.hoisted(() => ({ current: undefined as ReturnType<typeof useStudentSession> | undefined }));
vi.mock("./studentSession", async (importOriginal) => {
  const original = await importOriginal<typeof import("./studentSession")>();
  return { ...original, useStudentSession: () => {
    const session = original.useStudentSession();
    sessionProbe.current = session;
    return session;
  } };
});

const expired = () => jsonResponse({ error: { code: "unauthenticated" } }, 401);
const failed = () => jsonResponse({ error: { code: "temporary_failure" } }, 503);
const explanation = "请核对第二步已经写出的完整计算过程。";
const confirmation = "本题已提交过复核申请，不能重复提交。处理结果会显示在页面下方。";
const draftKey = appealDraftKey(studentDraftOwner(studentUser), "exam-1", "release-7", "question-1");
const isAnnotation = (path: string) => path.endsWith("/questions/question-1/annotations");
const isAppeal = (path: string) => path.endsWith("/question-appeals");

beforeEach(() => {
  window.location.hash = "/exams/exam-1";
  window.sessionStorage.clear();
  sessionProbe.current = undefined;
  vi.spyOn(window, "scrollTo").mockImplementation(() => undefined);
});
afterEach(() => { vi.unstubAllGlobals(); vi.restoreAllMocks(); });

async function prepareAppeal() {
  fireEvent.click(await screen.findByRole("button", { name: /^第 1 题，/ }));
  fireEvent.change(await screen.findByRole("textbox", { name: "具体说明" }), { target: { value: explanation } });
  const submit = screen.getByRole<HTMLButtonElement>("button", { name: "确认提交（仅一次）" });
  await waitFor(() => expect(submit.disabled).toBe(false));
  return submit;
}

async function loginAgain() {
  await screen.findByRole("heading", { name: "查看已发布成绩" });
  fireEvent.change(screen.getByRole("textbox", { name: "学校代码" }), { target: { value: "school-a" } });
  fireEvent.change(screen.getByRole("textbox", { name: "账号" }), { target: { value: "student-001" } });
  fireEvent.change(screen.getByLabelText("密码"), { target: { value: "testing" } });
  fireEvent.click(screen.getByRole("button", { name: "登录" }));
  fireEvent.click(await screen.findByRole("button", { name: /^第 1 题，/ }));
}

describe("复核回执与重新登录的顺序保护", () => {
  it("先重试批注再提交，迟到401不能卸载表单，成功回执保留确认且重登后不能重复提交", async () => {
    const annotation = deferredResponse();
    const receipt = deferredResponse();
    let annotationReads = 0;
    let accepted = false;
    const calls = installApi((call) => {
      if (call.url.pathname === "/api/v1/auth/login") return jsonResponse({ user: studentUser });
      if (isAppeal(call.url.pathname) && call.init.method === "POST") return receipt.promise;
      if (isAppeal(call.url.pathname)) return jsonResponse({ appeals: accepted ? [makeAppeal()] : [] });
      if (isAnnotation(call.url.pathname)) return ++annotationReads === 1 ? failed() : annotation.promise;
      return resultRoute(call, makeResult());
    });
    render(<App />);
    const submit = await prepareAppeal();
    // 批注重试先发出，POST 尚未取得回执时才收到401；此时的登录入口不能中断回执展示。
    fireEvent.click(screen.getByRole("button", { name: "重试批注" }));
    fireEvent.click(submit);
    await act(async () => annotation.resolve(expired()));
    const reauthenticate = await screen.findByRole<HTMLButtonElement>("button", { name: "重新登录" });
    expect(reauthenticate.disabled).toBe(true);
    fireEvent.click(reauthenticate);
    expect(screen.queryByRole("heading", { name: "查看已发布成绩" })).toBeNull();
    expect(screen.getByRole("textbox", { name: "具体说明" })).toBeTruthy();
    expect(readAppealDraft(draftKey)?.reason).toBe(explanation);
    accepted = true;
    await act(async () => receipt.resolve(jsonResponse({ appeal: makeAppeal() })));
    await screen.findByText(confirmation);
    expect(readAppealDraft(draftKey)).toBeUndefined();
    expect(reauthenticate.disabled).toBe(false);
    fireEvent.click(reauthenticate);
    await loginAgain();
    await screen.findByText(confirmation);
    expect(screen.queryByRole("button", { name: "确认提交（仅一次）" })).toBeNull();
    expect(calls.filter((call) => isAppeal(call.url.pathname) && call.init.method === "POST")).toHaveLength(1);
  });

  it("迟到批注401期间拒绝重登，POST失败结束后可重登并恢复同一版本草稿", async () => {
    const annotation = deferredResponse();
    const receipt = deferredResponse();
    let annotationReads = 0;
    const calls = installApi((call) => {
      if (call.url.pathname === "/api/v1/auth/login") return jsonResponse({ user: studentUser });
      if (isAppeal(call.url.pathname) && call.init.method === "POST") return receipt.promise;
      if (isAnnotation(call.url.pathname)) return ++annotationReads === 1 ? failed() : annotationReads === 2 ? annotation.promise : jsonResponse({ annotations: [] });
      return resultRoute(call, makeResult());
    });
    render(<App />);
    const submit = await prepareAppeal();
    fireEvent.click(screen.getByRole("button", { name: "重试批注" }));
    fireEvent.click(submit);
    await act(async () => annotation.resolve(expired()));
    const reauthenticate = await screen.findByRole<HTMLButtonElement>("button", { name: "重新登录" });
    expect(reauthenticate.disabled).toBe(true);
    await act(async () => receipt.resolve(failed()));
    await waitFor(() => expect(reauthenticate.disabled).toBe(false));
    expect(screen.getByRole<HTMLTextAreaElement>("textbox", { name: "具体说明" }).value).toBe(explanation);
    fireEvent.click(reauthenticate);
    await loginAgain();
    await waitFor(() => expect(screen.getByRole<HTMLTextAreaElement>("textbox", { name: "具体说明" }).value).toBe(explanation));
    expect(readAppealDraft(draftKey)?.reason).toBe(explanation);
    expect(calls.filter((call) => isAppeal(call.url.pathname) && call.init.method === "POST")).toHaveLength(1);
  });

  it("同一轮事件中直接调用旧会话重登回调也必须等待POST回执", async () => {
    const receipt = deferredResponse();
    const calls = installApi((call) => isAppeal(call.url.pathname) && call.init.method === "POST" ? receipt.promise : resultRoute(call, makeResult()));
    render(<App />);
    const submit = await prepareAppeal();
    const session = sessionProbe.current;
    expect(session).toBeDefined();
    expect(session?.submitting).toBe(false);
    // 保留提交前取得的回调，并在同一个批处理事件里调用；不能依赖按钮 disabled 或闭包中的旧 state。
    act(() => {
      fireEvent.submit(submit.closest("form") as HTMLFormElement);
      expect(session?.isSubmitting?.()).toBe(true);
      session?.reauthenticate();
    });
    expect(screen.queryByRole("heading", { name: "查看已发布成绩" })).toBeNull();
    expect(screen.getByRole("button", { name: "正在提交…" })).toBeTruthy();
    await act(async () => receipt.resolve(jsonResponse({ appeal: makeAppeal() })));
    await screen.findByText(confirmation);
    expect(calls.filter((call) => isAppeal(call.url.pathname) && call.init.method === "POST")).toHaveLength(1);
    act(() => session?.reauthenticate());
    await screen.findByRole("heading", { name: "查看已发布成绩" });
  });

  it("申请记录迟到401的重登入口在本题提交中禁用，成功回执后恢复", async () => {
    const record = deferredResponse();
    const receipt = deferredResponse();
    let recordReads = 0;
    installApi((call) => {
      if (isAppeal(call.url.pathname) && call.init.method === "POST") return receipt.promise;
      if (isAppeal(call.url.pathname)) return ++recordReads === 1 ? record.promise : jsonResponse({ appeals: [] });
      return resultRoute(call, makeResult());
    });
    render(<App />);
    fireEvent.click(await prepareAppeal());
    await act(async () => record.resolve(expired()));
    const status = document.querySelector(".appeal-status");
    expect(status).toBeTruthy();
    const reauthenticate = within(status as HTMLElement).getByRole<HTMLButtonElement>("button", { name: "重新登录" });
    expect(reauthenticate.disabled).toBe(true);
    fireEvent.click(reauthenticate);
    expect(screen.queryByRole("heading", { name: "查看已发布成绩" })).toBeNull();
    await act(async () => receipt.resolve(jsonResponse({ appeal: makeAppeal() })));
    await screen.findByText(confirmation);
    expect(screen.queryByText("正在提交复核申请，请等待回执后再切换题目或离开。")).toBeNull();
  });

  it("申请列表核对当前成绩迟到401时，保留列表但提交中禁用重登，失败后恢复", async () => {
    const current = deferredResponse();
    const receipt = deferredResponse();
    let recordReads = 0;
    let resultReads = 0;
    installApi((call) => {
      if (isAppeal(call.url.pathname) && call.init.method === "POST") return receipt.promise;
      if (isAppeal(call.url.pathname)) return jsonResponse({ appeals: ++recordReads === 1 ? [makeAppeal({ id: "appeal-2", question_id: "question-2", question_no: "2" })] : [] });
      if (call.url.pathname.endsWith("/result") && ++resultReads === 2) return current.promise;
      return resultRoute(call, makeResult());
    });
    render(<App />);
    fireEvent.click(await prepareAppeal());
    await act(async () => current.resolve(expired()));
    const status = document.querySelector(".appeal-status");
    expect(status?.textContent).toContain("第 2 题");
    const reauthenticate = within(status as HTMLElement).getByRole<HTMLButtonElement>("button", { name: "重新登录" });
    expect(reauthenticate.disabled).toBe(true);
    await act(async () => receipt.resolve(failed()));
    await waitFor(() => expect(reauthenticate.disabled).toBe(false));
    fireEvent.click(reauthenticate);
    await screen.findByRole("heading", { name: "查看已发布成绩" });
    expect(readAppealDraft(draftKey)?.reason).toBe(explanation);
  });

  it("整页批注迟到401的既有重登保护与逐题提交状态保持一致", async () => {
    const paperAnnotation = deferredResponse();
    const receipt = deferredResponse();
    let annotationReads = 0;
    const result = makeResult([makeQuestion({ page_no: 1 })]);
    installApi((call) => {
      if (isAppeal(call.url.pathname) && call.init.method === "POST") return receipt.promise;
      if (isAnnotation(call.url.pathname)) return ++annotationReads === 1 ? paperAnnotation.promise : jsonResponse({ annotations: [] });
      return resultRoute(call, result);
    });
    render(<App />);
    fireEvent.click(await prepareAppeal());
    await act(async () => paperAnnotation.resolve(expired()));
    const paper = document.querySelector(".paper-viewer");
    const reauthenticate = within(paper as HTMLElement).getByRole<HTMLButtonElement>("button", { name: "重新登录" });
    expect(reauthenticate.disabled).toBe(true);
    fireEvent.click(reauthenticate);
    expect(screen.queryByRole("heading", { name: "查看已发布成绩" })).toBeNull();
    await act(async () => receipt.resolve(jsonResponse({ appeal: makeAppeal() })));
    await screen.findByText(confirmation);
    await waitFor(() => expect(reauthenticate.disabled).toBe(false));
  });

  it("POST本身401后解除保护，表单重登恢复未提交草稿", async () => {
    const receipt = deferredResponse();
    installApi((call) => {
      if (call.url.pathname === "/api/v1/auth/login") return jsonResponse({ user: studentUser });
      if (isAppeal(call.url.pathname) && call.init.method === "POST") return receipt.promise;
      return resultRoute(call, makeResult());
    });
    render(<App />);
    fireEvent.click(await prepareAppeal());
    await act(async () => receipt.resolve(expired()));
    const reauthenticate = await screen.findByRole<HTMLButtonElement>("button", { name: "重新登录" });
    expect(reauthenticate.disabled).toBe(false);
    fireEvent.click(reauthenticate);
    await loginAgain();
    await waitFor(() => expect(screen.getByRole<HTMLTextAreaElement>("textbox", { name: "具体说明" }).value).toBe(explanation));
  });
});
