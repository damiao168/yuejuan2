import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { StrictMode, useLayoutEffect, useRef } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import App from "./App";
import { PaperViewer } from "./features/questions/PaperViewer";
import { deferredResponse, installApi, jsonResponse, makeAppeal, makeQuestion, makeResult, resultRoute } from "./test/studentAppBatch8Fixtures";

beforeEach(() => { window.location.hash = "/exams/exam-1"; window.sessionStorage.clear(); vi.spyOn(window, "scrollTo").mockImplementation(() => undefined); });
afterEach(() => { vi.unstubAllGlobals(); vi.restoreAllMocks(); });
const changed = () => jsonResponse({ error: { code: "student_release_changed" } }, 409);
const oldResult = () => makeResult([makeQuestion({ page_no: 1 }), makeQuestion({ question_id: "question-2", question_no: "2", page_no: 2 })], {
  paper_pages: [{ page_no: 1, question_id: "question-1" }, { page_no: 2, question_id: "question-2" }],
  high_score_paper: { available: true, pages: [{ page_no: 1, question_id: "question-1" }] }
});
const pageImage = () => document.querySelector(".paper-page img") as HTMLImageElement;

describe("整页答卷错误恢复", () => {
  it("一题批注确认版本冲突时不等待同页另一题的挂起请求", async () => {
    const pending = deferredResponse();
    const result = makeResult([makeQuestion({ page_no: 1 }), makeQuestion({ question_id: "question-2", question_no: "2", page_no: 1 })], { paper_pages: [{ page_no: 1, question_id: "question-1" }] });
    installApi((call) => {
      if (call.url.pathname.endsWith("/questions/question-1/annotations")) return changed();
      if (call.url.pathname.endsWith("/questions/question-2/annotations")) return pending.promise;
      return resultRoute(call, result);
    });
    render(<PaperViewer examID="exam-1" result={result} onViewCurrent={vi.fn()} />);
    expect(await screen.findByRole("button", { name: "查看当前成绩并重新核对" })).toBeTruthy();
    expect(pageImage()).toBeNull();
    await act(async () => pending.resolve(jsonResponse({ annotations: [] })));
    expect(screen.getByRole("alert").textContent).toContain("学校已更新当前成绩");
  });
  it("StrictMode首帧错误复核被作废后仍可重试，旧请求不能改变重挂载状态", async () => {
    const pending = deferredResponse();
    const result = oldResult();
    let reads = 0;
    installApi((call) => call.url.pathname.endsWith("/result") ? ++reads === 1 ? pending.promise : jsonResponse({ result: { ...result, release_id: "release-8" } }) : resultRoute(call, result));
    function FirstFrameError() {
      const triggered = useRef(false);
      // 在首个 layout 后、StrictMode 模拟卸载前触发缓存图片错误。
      useLayoutEffect(() => { if (!triggered.current) { triggered.current = true; pageImage().dispatchEvent(new Event("error", { bubbles: true })); } }, []);
      return <PaperViewer examID="exam-1" result={result} onViewCurrent={vi.fn()} />;
    }
    render(<StrictMode><FirstFrameError /></StrictMode>);
    await waitFor(() => expect(screen.getByRole<HTMLButtonElement>("button", { name: "重试答卷图像" }).disabled).toBe(false));
    await act(async () => pending.resolve(jsonResponse({ result: { ...result, release_id: "release-8" } })));
    expect(screen.queryByRole("button", { name: "查看当前成绩并重新核对" })).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "重试答卷图像" }));
    fireEvent.error(pageImage());
    await screen.findByRole("button", { name: "查看当前成绩并重新核对" });
  });
  it("整页批注409明确提示发布变化，直接重载当前成绩", async () => {
    let current = "release-7";
    const previous = oldResult();
    const latest = makeResult(previous.questions, { ...previous, release_id: "release-8", release_version: 8 });
    const calls = installApi((call) => {
      if (call.url.pathname.endsWith("/result")) return jsonResponse({ result: current === "release-7" ? previous : latest });
      if (call.url.pathname.endsWith("/annotations") && call.url.searchParams.get("expected_release_id") !== current) return changed();
      return resultRoute(call, previous);
    });
    render(<App />);
    await screen.findByRole("img", { name: "本人试卷第 1 页" });
    current = "release-8";
    fireEvent.click(screen.getByRole("button", { name: "下一页" }));
    const reload = await screen.findByRole("button", { name: "查看当前成绩并重新核对" });
    expect(screen.getByRole("alert").textContent).toContain("学校已更新当前成绩");
    expect(pageImage()).toBeNull();
    expect(screen.queryByRole("link", { name: "下载当前页" })).toBeNull();
    expect(screen.getByRole<HTMLButtonElement>("button", { name: "下一页" }).disabled).toBe(true);
    fireEvent.click(reload);
    await screen.findByText("已发布成绩 · 第 8 版");
    expect(pageImage().src).toContain("expected_release_id=release-8");
    expect(calls.filter((call) => call.url.pathname.endsWith("/result"))).toHaveLength(2);
  });

  it.each([false, true])("%s高分视图的图片失败可识别发布变化", async (highScore) => {
    let reads = 0;
    const result = oldResult();
    installApi((call) => {
      if (call.url.pathname.endsWith("/result")) return jsonResponse({ result: ++reads === 1 ? result : { ...result, release_id: "release-8", release_version: 8 } });
      return resultRoute(call, result);
    });
    render(<App />);
    await screen.findByRole("img", { name: "本人试卷第 1 页" });
    if (highScore) fireEvent.click(screen.getByRole("button", { name: "查看高分试卷" }));
    fireEvent.error(pageImage());
    await screen.findByRole("button", { name: "查看当前成绩并重新核对" });
    expect(pageImage()).toBeNull();
    expect(screen.getByText("已发布成绩 · 第 7 版")).toBeTruthy();
    expect(reads).toBe(2);
  });

  it.each(["bad-image", "network"])("%s故障提供原页重试，不擅自切换成绩版本", async (failure) => {
    const result = oldResult();
    let reads = 0;
    installApi((call) => {
      if (call.url.pathname.endsWith("/result") && ++reads > 1 && failure === "network") throw new TypeError("offline");
      return resultRoute(call, result);
    });
    render(<App />);
    await screen.findByRole("img", { name: "本人试卷第 1 页" });
    fireEvent.click(screen.getByRole("button", { name: "下一页" }));
    fireEvent.error(pageImage());
    await waitFor(() => expect(screen.getByRole<HTMLButtonElement>("button", { name: "重试答卷图像" }).disabled).toBe(false));
    expect(screen.getByRole("alert").textContent).toContain("答卷图像暂时无法加载");
    expect(screen.queryByRole("button", { name: "查看当前成绩并重新核对" })).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "重试答卷图像" }));
    expect(pageImage().alt).toBe("本人试卷第 2 页");
    expect(pageImage().src).toContain("expected_release_id=release-7");
  });

  it("普通整页批注失败可单独重试，保留当前页图像", async () => {
    let failed = true;
    installApi((call) => {
      if (call.url.pathname.endsWith("/annotations") && failed) return jsonResponse({ error: { code: "failed" } }, 503);
      return resultRoute(call, oldResult());
    });
    render(<App />);
    const retry = await screen.findByRole("button", { name: "重试整页批注" });
    expect(pageImage().alt).toBe("本人试卷第 1 页");
    failed = false;
    fireEvent.click(retry);
    await waitFor(() => expect(screen.queryByRole("button", { name: "重试整页批注" })).toBeNull());
    expect(pageImage().alt).toBe("本人试卷第 1 页");
  });

  it("上一页批注迟到409不能关闭新页", async () => {
    const pending = deferredResponse();
    const result = oldResult();
    installApi((call) => call.url.pathname.endsWith("/questions/question-1/annotations") ? pending.promise : resultRoute(call, result));
    render(<App />);
    await screen.findByRole("img", { name: "本人试卷第 1 页" });
    fireEvent.click(screen.getByRole("button", { name: "下一页" }));
    await act(async () => pending.resolve(changed()));
    expect(pageImage().alt).toBe("本人试卷第 2 页");
    expect(screen.queryByRole("button", { name: "查看当前成绩并重新核对" })).toBeNull();
  });

  it("上一页图片迟到版本复核不能关闭高分视图", async () => {
    const pending = deferredResponse();
    const result = oldResult();
    let reads = 0;
    installApi((call) => {
      if (call.url.pathname.endsWith("/result") && ++reads > 1) return pending.promise;
      return resultRoute(call, result);
    });
    render(<App />);
    await screen.findByRole("img", { name: "本人试卷第 1 页" });
    fireEvent.error(pageImage());
    expect(screen.getByRole<HTMLButtonElement>("button", { name: "重试答卷图像" }).disabled).toBe(true);
    fireEvent.click(screen.getByRole("button", { name: "查看高分试卷" }));
    await act(async () => pending.resolve(jsonResponse({ result: { ...result, release_id: "release-8", release_version: 8 } })));
    expect(pageImage().alt).toBe("高分试卷第 1 页");
    expect(screen.queryByRole("button", { name: "查看当前成绩并重新核对" })).toBeNull();
    expect(screen.queryByRole("button", { name: "重试答卷图像" })).toBeNull();
  });

  it("申请正在提交时整页重新核对不能卸载回执等待", async () => {
    let changedPage = false;
    const pending = deferredResponse();
    const result = oldResult();
    const calls = installApi((call) => {
      if (call.init.method === "POST") return pending.promise;
      if (changedPage && call.url.pathname.endsWith("/questions/question-2/annotations")) return changed();
      return resultRoute(call, result);
    });
    render(<App />);
    fireEvent.click(await screen.findByRole("button", { name: /^第 1 题，/ }));
    fireEvent.change(await screen.findByRole("textbox", { name: "具体说明" }), { target: { value: "请核对本题第二步已经写出的解答过程。" } });
    await waitFor(() => expect(screen.getByRole<HTMLButtonElement>("button", { name: "确认提交（仅一次）" }).disabled).toBe(false));
    changedPage = true;
    fireEvent.click(screen.getByRole("button", { name: "下一页" }));
    const reload = await screen.findByRole<HTMLButtonElement>("button", { name: "查看当前成绩并重新核对" });
    fireEvent.click(screen.getByRole("button", { name: "确认提交（仅一次）" }));
    expect(reload.disabled).toBe(true);
    fireEvent.click(reload);
    expect(calls.filter((call) => call.url.pathname.endsWith("/result"))).toHaveLength(1);
    expect(screen.getByRole("textbox", { name: "具体说明" })).toBeTruthy();
    await act(async () => pending.resolve(jsonResponse({ appeal: makeAppeal() })));
    await screen.findByText("本题已提交过复核申请，不能重复提交。处理结果会显示在页面下方。");
  });
});
