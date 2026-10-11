import { describe, expect, it, vi } from "vitest";
import { DesktopApiClient } from "./client";
import { listExams } from "./exams";
import { listReviewTasks } from "./review";

describe("考试查询参数", () => {
  it.each([
    { name: "省略筛选时没有问号", filter: undefined, url: "/api/v1/exams" },
    { name: "空筛选值不写入查询", filter: { status: "", school_id: "" }, url: "/api/v1/exams" },
    { name: "只保留状态", filter: { status: "ready" }, url: "/api/v1/exams?status=ready" },
    { name: "状态和学校保持顺序与编码", filter: { status: "ready", school_id: "school/a b" }, url: "/api/v1/exams?status=ready&school_id=school%2Fa+b" },
  ])("$name", async ({ filter, url }) => {
    const client = new DesktopApiClient({ baseUrl: "https://grading.example.edu" });
    const response = { exams: [] };
    const request = vi.spyOn(client, "request").mockResolvedValue(response);
    expect(await listExams(client, filter)).toBe(response);
    expect(request).toHaveBeenCalledTimes(1);
    expect(request).toHaveBeenCalledWith(url);
  });
});

describe("阅卷任务查询参数", () => {
  it.each([
    { name: "省略筛选时没有问号", filter: undefined, url: "/api/v1/review-tasks" },
    { name: "空筛选值不写入查询", filter: { status: "", assigned_to: "" }, url: "/api/v1/review-tasks" },
    { name: "只保留阅卷人", filter: { assigned_to: "teacher-1" }, url: "/api/v1/review-tasks?assigned_to=teacher-1" },
    { name: "状态和阅卷人保持顺序与编码", filter: { status: "in progress", assigned_to: "teacher/a b" }, url: "/api/v1/review-tasks?status=in+progress&assigned_to=teacher%2Fa+b" },
  ])("$name", async ({ filter, url }) => {
    const client = new DesktopApiClient({ baseUrl: "https://grading.example.edu" });
    const response = { tasks: [] };
    const request = vi.spyOn(client, "request").mockResolvedValue(response);
    expect(await listReviewTasks(client, filter)).toBe(response);
    expect(request).toHaveBeenCalledTimes(1);
    expect(request).toHaveBeenCalledWith(url);
  });
});
