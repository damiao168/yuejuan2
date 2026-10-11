import { fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import App from "./App";
import { installApi, jsonResponse, makeQuestion, makeResult, resultRoute } from "./test/studentAppBatch8Fixtures";

beforeEach(() => {
  window.location.hash = "/exams/exam-1";
  window.sessionStorage.clear();
});
afterEach(() => vi.unstubAllGlobals());

describe("题目与批注的发布版本核对", () => {
  it.each([
    { name: "批注版本冲突不能继续显示成功返回的题目", questionError: undefined, annotationError: "student_release_changed" },
    { name: "题目普通失败不能掩盖批注版本冲突", questionError: "unavailable", annotationError: "student_release_changed" },
    { name: "批注登录失效不能掩盖题目版本冲突", questionError: "student_release_changed", annotationError: "unauthenticated" }
  ])("$name", async ({ questionError, annotationError }) => {
    const result = makeResult();
    installApi((call) => {
      if (call.url.pathname.endsWith("question-1/annotations")) {
        return jsonResponse({ error: { code: annotationError } }, annotationError === "unauthenticated" ? 401 : 409);
      }
      if (call.url.pathname.endsWith("questions/question-1") && questionError) {
        return jsonResponse({ error: { code: questionError } }, questionError === "student_release_changed" ? 409 : 503);
      }
      return resultRoute(call, result);
    });
    render(<App />);
    fireEvent.click(await screen.findByRole("button", { name: /^第 1 题，/ }));
    await screen.findByText("学校已更新当前成绩。原版本草稿已保留，请查看当前成绩并重新核对题目后再申请复核。");
    expect(screen.queryByRole("textbox", { name: "具体说明" })).toBeNull();
    expect(screen.queryByRole("button", { name: "重新登录" })).toBeNull();
  });

  it("返回其他题目的详情时不显示或提交该题证据", async () => {
    const result = makeResult();
    installApi((call) => {
      if (call.url.pathname.endsWith("questions/question-1")) {
        return jsonResponse({ question: makeQuestion({ question_id: "question-2", stem: "不应显示的另一题题干" }) });
      }
      return resultRoute(call, result);
    });
    render(<App />);
    fireEvent.click(await screen.findByRole("button", { name: /^第 1 题，/ }));
    await screen.findByText("暂时无法连接服务，请稍后重试。");
    expect(screen.queryByText("不应显示的另一题题干")).toBeNull();
    expect(screen.queryByRole("textbox", { name: "具体说明" })).toBeNull();
  });
});
