import { Children, type ReactElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import type { ColumnType } from "antd/es/table";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { OcrTask } from "../../api/submissions";
import type { SubmissionView } from "./captureQueries";
import { captureSubmissionColumns } from "./submissionColumns";

const mocks = vi.hoisted(() => ({
  integrity: vi.fn(), quality: vi.fn(), ready: vi.fn(), ocr: vi.fn(), segments: vi.fn(),
}));

vi.mock("../../api/capture", () => ({ runImageQualityCheck: mocks.quality }));
vi.mock("../../api/submissions", () => ({
  runQualityCheck: mocks.integrity, updateSubmissionStatus: mocks.ready,
  createOcrTask: mocks.ocr, generateAnswerSegments: mocks.segments,
}));
vi.mock("../../api/client", () => ({
  ApiClientError: class extends Error {},
  getSafeUserText: (value: string, fallback: string) => value || fallback,
  getUserErrorMessage: (error: unknown, fallback: string) => error instanceof Error ? error.message : fallback,
}));

type Options = Parameters<typeof captureSubmissionColumns>[0];
interface ActionButtonProps {
  children: string;
  disabled: boolean;
  loading: boolean;
  onClick: () => void;
}

function row(qualityStatus = "passed", status = "ready_for_ocr", taskStatus = "", segmentCount = 0): SubmissionView {
  return {
    submission: {
      id: "submission-row-14", tenant_id: "tenant-14", exam_id: "exam-14", candidate_no: "考号与对象 ID 不同",
      source_type: "image_upload", status, expected_page_count: 2, actual_page_count: 2,
      quality_status: qualityStatus, quality_issues: [], collected_by: "teacher-14", revision: 7,
      created_at: "2026-10-05T00:00:00Z", summary: { latest_ocr_status: taskStatus, segment_count: segmentCount },
    },
    pages: [], ocrTasks: [], ocrNextCursor: "", ocrHasMore: false, segments: [],
  };
}

function options() {
  return {
    studentName: vi.fn(() => "无姓名权限"), canWrite: true, actioning: "ocr-submission-row-14",
    runAction: vi.fn<Options["runAction"]>().mockResolvedValue(undefined),
    refreshSingle: vi.fn<Options["refreshSingle"]>().mockResolvedValue(undefined),
    openPages: vi.fn<Options["openPages"]>().mockResolvedValue(undefined),
    openOcrDrawer: vi.fn<Options["openOcrDrawer"]>().mockResolvedValue(undefined),
  };
}

function primaryButton(record: SubmissionView, settings: Options) {
  const column = captureSubmissionColumns(settings).find((item) => item.title === "下一步") as ColumnType<SubmissionView>;
  const cell = column.render!(undefined, record, 0) as ReactElement<{ children: ReactElement[] }>;
  return Children.toArray(cell.props.children)[0] as ReactElement<ActionButtonProps>;
}

describe("答题卡下一步列展示与操作边界", () => {
  beforeEach(() => {
    vi.resetAllMocks();
    mocks.integrity.mockResolvedValue({ result: { valid: true } });
  });

  it.each([
    ["检查图片质量", "unchecked", "ready_for_ocr", "completed", 0, "quality"],
    ["转入待识别", "passed", "quality_checked", "completed", 0, "ready"],
    ["开始识别", "passed", "ready_for_ocr", "", 0, "ocr"],
    ["重试识别", "passed", "ready_for_ocr", "failed", 0, "ocr"],
    ["生成题目区域", "passed", "ready_for_ocr", "completed", 0, "segment"],
    ["查看答题卡", "passed", "ready_for_ocr", "completed", 1, null],
  ] as const)("汇总列表的%s保留操作优先级、对象 ID 和只读边界", async (label, qualityStatus, status, taskStatus, segmentCount, actionKey) => {
    const record = row(qualityStatus, status, taskStatus, segmentCount);
    const settings = options();
    const button = primaryButton(record, settings);
    expect(renderToStaticMarkup(button)).toContain(label);
    expect(button.props.disabled).toBe(false);
    expect(button.props.loading).toBe(true);
    expect(primaryButton(record, { ...settings, canWrite: false }).props.disabled).toBe(actionKey !== null);
    button.props.onClick();

    if (actionKey === null) {
      expect(settings.openPages).toHaveBeenCalledWith(record);
      expect(settings.runAction).not.toHaveBeenCalled();
      return;
    }
    const [key, action, successText, reload] = settings.runAction.mock.calls[0];
    expect(key).toBe(`${actionKey}-${record.submission.id}`);
    expect(successText).toBeTruthy();
    await action();
    await reload!();
    expect(settings.refreshSingle).toHaveBeenCalledWith(record.submission.id);
    const calls = {
      quality: mocks.integrity, ready: mocks.ready, ocr: mocks.ocr, segment: mocks.segments,
    };
    if (actionKey === "ready") {
      expect(mocks.ready).toHaveBeenCalledWith(record.submission.id, "ready_for_ocr", 7);
    } else {
      expect(calls[actionKey]).toHaveBeenCalledWith(record.submission.id);
    }
    if (actionKey === "quality") {
      expect(mocks.quality).toHaveBeenCalledWith(record.submission.id);
      expect(mocks.integrity.mock.invocationCallOrder[0]).toBeLessThan(mocks.quality.mock.invocationCallOrder[0]);
    }
  });

  it("详情最新任务优先于列表汇总失败状态", () => {
    const record = row("passed", "ready_for_ocr", "failed");
    const task = (id: string, status: string, createdAt: string): OcrTask => ({
      id, tenant_id: "tenant-14", submission_id: record.submission.id, status, engine: "ocr", engine_version: "v1",
      min_confidence: 0.8, result_count: 1, requires_human_review: false, requested_by: "teacher-14", created_at: createdAt,
    });
    record.ocrTasks = [task("older", "failed", "2026-10-04T00:00:00Z"), task("latest", "completed", "2026-10-05T00:00:00Z")];
    expect(primaryButton(record, options()).props.children).toBe("生成题目区域");
  });

  it("完整性失败保留原错误并停止图像检测", async () => {
    mocks.integrity.mockResolvedValue({ result: { valid: false } });
    const settings = options();
    primaryButton(row("failed"), settings).props.onClick();
    const action = settings.runAction.mock.calls[0][1];
    await expect(action()).rejects.toThrow("答卷完整性未通过，请先补齐或更正页面");
    expect(mocks.quality).not.toHaveBeenCalled();
    expect(mocks.ready).not.toHaveBeenCalled();
    expect(mocks.ocr).not.toHaveBeenCalled();
    expect(mocks.segments).not.toHaveBeenCalled();
  });
});
