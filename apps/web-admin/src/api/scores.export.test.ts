// @vitest-environment jsdom
import { afterEach, expect, it, vi } from "vitest";
import { apiClient } from "./client";
import { exportPublishedExamGrades } from "./scores";

afterEach(() => vi.restoreAllMocks());

it("教师导出请求Excel并保留取消信号", async () => {
  const signal = new AbortController().signal;
  const request = vi.spyOn(apiClient, "requestBlob").mockResolvedValue({ blob: new Blob(), contentType: "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", filename: undefined, watermark: undefined });
  await exportPublishedExamGrades("exam /1", signal);
  expect(request).toHaveBeenCalledExactlyOnceWith("/api/v1/exams/exam%20%2F1/score-releases/export?format=xlsx", { signal });
});
