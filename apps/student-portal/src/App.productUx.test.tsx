import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import App, { appealReleaseState } from "./App";
import { PortalApiError } from "./api";
import { friendlyError } from "./errors";
import { appealDraftKey, readAppealDraft, studentDraftOwner, writeAppealDraft } from "./appealDrafts";
import { StudentText } from "./features/questions/StudentText";
import { deferredResponse, installApi, jsonResponse, makeAppeal, makeQuestion, makeResult, resultRoute, studentUser } from "./test/studentAppBatch8Fixtures";

beforeEach(() => { window.location.hash = "/exams/exam-1"; window.sessionStorage.clear(); });
afterEach(() => vi.unstubAllGlobals());

describe("学生复盘与复核恢复", () => {
  it("公开题干和行内、块公式可读，普通文本不会变为 HTML", () => {
    const { container } = render(<StudentText>{"求 $x^2$ 的值\n\\[\\frac{1}{2}\\]\n<img src=x onerror=alert(1)>"}</StudentText>);
    expect(container.querySelectorAll(".katex")).toHaveLength(2);
    expect(container.querySelector("img")).toBeNull();
    expect(container.textContent).toContain("<img src=x onerror=alert(1)>");
  });

  it("换题、连续复盘和重挂载保留本题草稿，批注重试不重新读取题目", async () => {
    const first = makeQuestion({ stem: "已公开的第一题要求" });
    const second = makeQuestion({ question_id: "question-2", question_no: "2" });
    const result = makeResult([first, second]);
    let annotationFailures = 1;
    const calls = installApi((call) => {
      if (call.url.pathname.endsWith("question-1/annotations") && annotationFailures-- > 0) return jsonResponse({ error: { code: "failed" } }, 503);
      return resultRoute(call, result);
    });
    const view = render(<App />);
    fireEvent.click(await screen.findByRole("button", { name: /^第 1 题，/ }));
    const reason = await screen.findByRole<HTMLTextAreaElement>("textbox", { name: "具体说明" });
    expect(screen.getByText("已公开的第一题要求")).toBeTruthy();
    fireEvent.change(reason, { target: { value: "请核对第二步已写出的完整计算过程。" } });
    const detailRequests = calls.filter((call) => call.url.pathname.endsWith("questions/question-1")).length;
    fireEvent.click(screen.getByRole("button", { name: "重试批注" }));
    await waitFor(() => expect(screen.queryByRole("button", { name: "重试批注" })).toBeNull());
    expect(screen.getByRole("textbox", { name: "具体说明" })).toBe(reason);
    expect(calls.filter((call) => call.url.pathname.endsWith("questions/question-1"))).toHaveLength(detailRequests);
    fireEvent.click(screen.getByRole("button", { name: "下一失分题" }));
    await screen.findByRole("heading", { name: "第 2 题 · 作答与评分依据" });
    fireEvent.click(screen.getByRole("button", { name: "上一失分题" }));
    await waitFor(() => expect(screen.getByRole<HTMLTextAreaElement>("textbox", { name: "具体说明" }).value).toContain("完整计算过程"));
    view.unmount(); render(<App />);
    fireEvent.click(await screen.findByRole("button", { name: /^第 1 题，/ }));
    await waitFor(() => expect(screen.getByRole<HTMLTextAreaElement>("textbox", { name: "具体说明" }).value).toContain("完整计算过程"));
  });

  it("草稿按用户和发布版本隔离，退出清除，成功提交清除", async () => {
    const owner = studentDraftOwner(studentUser);
    const key = appealDraftKey(owner, "exam-1", "release-7", "question-1");
    writeAppealDraft(key, { reasonCode: "recognition_error", reason: "请核对第二步已写出的完整计算过程。" });
    expect(readAppealDraft(appealDraftKey(owner, "exam-1", "release-8", "question-1"))).toBeUndefined();
    expect(readAppealDraft(appealDraftKey(studentDraftOwner({ ...studentUser, id: "another" }), "exam-1", "release-7", "question-1"))).toBeUndefined();
    installApi((call) => call.url.pathname.endsWith("/question-appeals") && call.init.method === "POST" ? jsonResponse({ appeal: makeAppeal() }) : call.url.pathname === "/api/v1/auth/logout" ? jsonResponse({}) : resultRoute(call, makeResult()));
    render(<App />);
    fireEvent.click(await screen.findByRole("button", { name: /^第 1 题，/ }));
    await waitFor(() => expect(screen.getByRole<HTMLButtonElement>("button", { name: "确认提交（仅一次）" }).disabled).toBe(false));
    fireEvent.click(screen.getByRole("button", { name: "确认提交（仅一次）" }));
    await screen.findByText("本题已提交过复核申请，不能重复提交。处理结果会显示在页面下方。");
    expect(readAppealDraft(key)).toBeUndefined();
    writeAppealDraft(key, { reasonCode: "recognition_error", reason: "另一份待清理的测试说明" });
    fireEvent.click(screen.getByRole("button", { name: "退出登录" }));
    await screen.findByRole("heading", { name: "查看已发布成绩" });
    expect(readAppealDraft(key)).toBeUndefined();
  });

  it("提交期间冻结切题和返回，避免丢失回执", async () => {
    const pending = deferredResponse();
    installApi((call) => call.init.method === "POST" ? pending.promise : resultRoute(call, makeResult([makeQuestion(), makeQuestion({ question_id: "question-2", question_no: "2" })])));
    render(<App />);
    fireEvent.click(await screen.findByRole("button", { name: /^第 1 题，/ }));
    fireEvent.change(await screen.findByRole("textbox", { name: "具体说明" }), { target: { value: "请核对第二步已写出的完整计算过程。" } });
    await waitFor(() => expect(screen.getByRole<HTMLButtonElement>("button", { name: "确认提交（仅一次）" }).disabled).toBe(false));
    fireEvent.click(screen.getByRole("button", { name: "确认提交（仅一次）" }));
    expect(screen.getByRole<HTMLButtonElement>("button", { name: /^第 2 题，/ }).disabled).toBe(true);
    expect(screen.getByRole<HTMLButtonElement>("button", { name: "← 返回我的考试" }).disabled).toBe(true);
    pending.resolve(jsonResponse({ appeal: makeAppeal() }));
    await screen.findByText("本题已提交过复核申请，不能重复提交。处理结果会显示在页面下方。");
  });

  it("401重登保留同一身份草稿，截止409不误报断网", async () => {
    expect(friendlyError(new PortalApiError(401, "invalid_credentials", "private"))).toContain("账号或密码不正确");
    expect(friendlyError(new PortalApiError(409, "appeal_window_closed", "private"))).toContain("已截止");
    expect(friendlyError(new PortalApiError(409, "appeal_already_filed", "private"))).toContain("已经提交");
    installApi((call) => call.url.pathname === "/api/v1/auth/login" ? jsonResponse({ user: studentUser }) : call.init.method === "POST" ? jsonResponse({ error: { code: "unauthenticated" } }, 401) : resultRoute(call, makeResult()));
    render(<App />);
    fireEvent.click(await screen.findByRole("button", { name: /^第 1 题，/ }));
    fireEvent.change(await screen.findByRole("textbox", { name: "具体说明" }), { target: { value: "请核对第二步已写出的完整计算过程。" } });
    await waitFor(() => expect(screen.getByRole<HTMLButtonElement>("button", { name: "确认提交（仅一次）" }).disabled).toBe(false));
    fireEvent.click(screen.getByRole("button", { name: "确认提交（仅一次）" }));
    fireEvent.click(await screen.findByRole("button", { name: "重新登录" }));
    fireEvent.change(screen.getByRole("textbox", { name: "学校代码" }), { target: { value: "school-a" } });
    fireEvent.change(screen.getByRole("textbox", { name: "账号" }), { target: { value: "student-001" } });
    fireEvent.change(screen.getByLabelText("密码"), { target: { value: "testing" } });
    fireEvent.click(screen.getByRole("button", { name: "登录" }));
    fireEvent.click(await screen.findByRole("button", { name: /^第 1 题，/ }));
    await waitFor(() => expect(screen.getByRole<HTMLTextAreaElement>("textbox", { name: "具体说明" }).value).toContain("完整计算过程"));
  });

  it("关联版本号更大也不能冒充当前生效成绩", () => {
    const appeal = makeAppeal({ status: "resolved", new_release_id: "release-9", new_release_version: 9 });
    expect(appealReleaseState(appeal, makeResult())).toContain("不是本次处理关联的发布");
    expect(appealReleaseState(appeal, makeResult(undefined, { release_id: "release-9", release_version: 9 }))).toContain("已是当前生效成绩");
  });

  it("回到标签页检查新成绩，并显示检查时间", async () => {
    window.location.hash = "/";
    let published = false;
    installApi((call) => call.url.pathname === "/api/v1/student/exams" ? jsonResponse({ exams: published ? [{ exam_id: "exam-1", name: "新发布月考", subject: "math", release_version: 7, published_at: "2026-10-05T08:00:00Z" }] : [] }) : undefined);
    render(<App />);
    await screen.findByText("学校尚未发布你的成绩");
    expect(screen.getByText(/最近检查/)).toBeTruthy();
    published = true; fireEvent(window, new Event("focus"));
    await screen.findByRole("heading", { name: "新发布月考" });
  });
});
