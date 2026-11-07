import { describe, expect, it } from "vitest";
import { studentPortalDestination } from "./loginDestination";

describe("unified login student return path", () => {
  it("keeps the requested exam and the actual school code", () => {
    const search = new URLSearchParams({ return_to: "/student/exams/exam-1?school=old#question-2" }).toString();
    expect(studentPortalDestination(search, "S053")).toBe("/student/exams/exam-1?school=S053#question-2");
  });
  it.each(["https://outside.invalid/student/", "//outside.invalid/student/", "/admin/dashboard", "/student/../admin", "/student/%2e%2e/admin", "/student/\\outside.invalid", "/student/\n"])("rejects return path %j", value => {
    expect(studentPortalDestination(new URLSearchParams({ return_to: value }).toString())).toBe("/student/");
  });
  it("defaults to the student homepage without substituting the tenant code", () => {
    expect(studentPortalDestination("?tenant=demo")).toBe("/student/");
  });
});
