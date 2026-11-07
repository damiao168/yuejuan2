import { beforeEach, expect, it } from "vitest";
import { unifiedLoginURL } from "./loginEntry";

beforeEach(() => window.history.replaceState({}, "", "/"));

it.each([
  ["/student/?school=S053", "/student/?school=S053"],
  ["/student/exams/exam%20%2F1?school=S053", "/student/exams/exam%20%2F1?school=S053"],
  ["/?school=S053#/exams/exam-1", "/student/exams/exam-1?school=S053"],
  ["/student/?school=S053#/exams/exam-1", "/student/exams/exam-1?school=S053"],
  ["/student/?return_to=https://other.example/", "/student/?return_to=https://other.example/"]
])("旧入口%s只生成本站学生路径回跳", (path, expected) => {
  window.history.replaceState({}, "", path);
  const target = new URL(unifiedLoginURL());
  expect(target.origin).toBe(window.location.origin);
  expect(target.pathname).toBe("/");
  expect(target.searchParams.get("return_to")).toBe(expected);
  expect(target.searchParams.has("username")).toBe(false);
  expect(target.searchParams.has("password")).toBe(false);
});
