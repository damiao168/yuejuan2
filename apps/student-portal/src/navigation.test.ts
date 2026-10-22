import { afterEach, describe, expect, it, vi } from "vitest";
import { goExam, goHome, normalizeStudentLocation, readStudentPage, studentLocation, STUDENT_NAVIGATION_EVENT } from "./navigation";

afterEach(() => { window.history.replaceState(null, "", "/"); vi.restoreAllMocks(); });

describe("学生端无井号路由", () => {
  it.each([
    ["/", { kind: "home" }],
    ["/exams/exam%201", { kind: "exam", examID: "exam 1" }],
    ["/exams/exam-1?from=school", { kind: "exam", examID: "exam-1" }],
    ["/exams/%E0%A4%A", { kind: "home" }],
    ["/unknown", { kind: "home" }]
  ])("直达%s和刷新解析同一页面", (path, expected) => {
    window.history.replaceState(null, "", path);
    expect(readStudentPage()).toEqual(expected);
  });

  it("旧考试书签转换为普通地址，保留查询并使用replace避免产生多余历史", () => {
    window.history.replaceState({ marker: "kept" }, "", "/?tenant=school-a&from=entry#/exams/exam%201?from=school");
    const replace = vi.spyOn(window.history, "replaceState");
    normalizeStudentLocation();
    expect(replace).toHaveBeenCalledWith({ marker: "kept" }, "", "/exams/exam%201?tenant=school-a&from=school");
    expect(window.location.hash).toBe("");
    expect(studentLocation()).toBe("/exams/exam%201?tenant=school-a&from=school");
    expect(readStudentPage()).toEqual({ kind: "exam", examID: "exam 1" });
  });

  it.each([
    "/#//other.example/exams/another",
    "/#/\\other.example/exams/another",
    "/#detail",
    "/exams/exam-1#/exams/another",
    "/exams/exam-1#page=2"
  ])("普通片段或非根旧入口不迁移：%s", (path) => {
    window.history.replaceState(null, "", path);
    const before = window.location.href;
    normalizeStudentLocation();
    expect(window.location.href).toBe(before);
    expect(readStudentPage()).toEqual(path.startsWith("/exams/") ? { kind: "exam", examID: "exam-1" } : { kind: "home" });
  });

  it("页面按钮使用history并通知App，浏览器片段不被误作考试地址", () => {
    const changed = vi.fn();
    window.addEventListener(STUDENT_NAVIGATION_EVENT, changed);
    try {
      goExam("exam /1");
      expect(window.location.pathname).toBe("/exams/exam%20%2F1");
      expect(window.location.hash).toBe("");
      expect(readStudentPage()).toEqual({ kind: "exam", examID: "exam /1" });
      goHome();
      expect(window.location.pathname).toBe("/");
      expect(changed).toHaveBeenCalledTimes(2);
      window.history.replaceState(null, "", "/exams/exam-1#detail");
      normalizeStudentLocation();
      expect(readStudentPage()).toEqual({ kind: "exam", examID: "exam-1" });
    } finally { window.removeEventListener(STUDENT_NAVIGATION_EVENT, changed); }
  });
});
