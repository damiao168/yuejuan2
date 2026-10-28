import { Children, type ReactElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import type { ColumnType } from "antd/es/table";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { OcrTask } from "../../api/submissions";
import type { SubmissionView } from "./captureQueries";
import { captureSubmissionColumns } from "./submissionColumns";

const writes = vi.hoisted(() => ({ integrity: vi.fn(), quality: vi.fn(), ready: vi.fn(), ocr: vi.fn(), segments: vi.fn() }));
vi.mock("../../api/capture", () => ({ runImageQualityCheck: writes.quality }));
vi.mock("../../api/submissions", () => ({ runQualityCheck: writes.integrity, updateSubmissionStatus: writes.ready, createOcrTask: writes.ocr, generateAnswerSegments: writes.segments }));
type Options = Parameters<typeof captureSubmissionColumns>[0];
function row(quality = "passed", status = "ready_for_ocr", task = "", count = 0): SubmissionView {
  return { submission: { id: "submission-row-14", tenant_id: "tenant-14", exam_id: "exam-14", candidate_no: "考号与对象 ID 不同", source_type: "image_upload", status,
    expected_page_count: 2, actual_page_count: 2, quality_status: quality, quality_issues: [], collected_by: "teacher", revision: 7, created_at: "", summary: { latest_ocr_status: task, segment_count: count } },
    pages: [], ocrTasks: [], ocrNextCursor: "", ocrHasMore: false, segments: [] };
}
function options(): Options { return { studentName: vi.fn(() => "无姓名权限"), openPages: vi.fn().mockResolvedValue(undefined), openOcrDrawer: vi.fn().mockResolvedValue(undefined) }; }
function primary(record: SubmissionView, settings: Options) {
  const column = captureSubmissionColumns(settings).find(item => item.title === "查看") as ColumnType<SubmissionView>;
  const cell = column.render!(undefined, record, 0) as ReactElement<{ children: ReactElement[] }>;
  return Children.toArray(cell.props.children)[0] as ReactElement<{ children: string; onClick: () => void }>;
}
describe("历史答卷只读主操作", () => {
  beforeEach(() => vi.clearAllMocks());
  it.each([
    ["unchecked", "pages_uploaded", "", 0], ["passed", "quality_checked", "", 0], ["passed", "ready_for_ocr", "queued", 0],
    ["passed", "ready_for_ocr", "failed", 0], ["passed", "ready_for_ocr", "completed", 0], ["passed", "ready_for_ocr", "completed", 1]
  ] as const)("质量=%s 状态=%s 识别=%s 时查看材料不推进旧处理链", (quality, status, task, count) => {
    const record = row(quality, status, task, count); const settings = options(); const button = primary(record, settings);
    expect(renderToStaticMarkup(button)).toContain("查看答题卡"); button.props.onClick(); expect(settings.openPages).toHaveBeenCalledExactlyOnceWith(record);
    for (const write of Object.values(writes)) expect(write).not.toHaveBeenCalled();
    expect(captureSubmissionColumns(settings).some(item => item.title === "下一步")).toBe(false);
  });
  it("详情最新任务仍决定历史处理状态，查看不会重新创建识别或切题", () => {
    const record = row("passed", "ready_for_ocr", "failed");
    record.ocrTasks = [{ id: "latest", submission_id: record.submission.id, status: "completed", created_at: "2026-10-28" } as OcrTask];
    const column = captureSubmissionColumns(options()).find(item => item.title === "当前状态") as ColumnType<SubmissionView>;
    expect(renderToStaticMarkup(column.render!(undefined, record, 0) as ReactElement)).toContain("题目区域尚未形成");
    expect(primary(record, options()).props.children).toBe("查看答题卡");
  });
});
