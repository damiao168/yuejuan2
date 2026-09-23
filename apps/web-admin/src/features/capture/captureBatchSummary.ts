import type { CaptureBatchDetail } from "../../api/capture";

export function captureProgressPercent(detail?: CaptureBatchDetail): number | null {
  if (!detail) return null;
  if (detail.batch.status === "completed") return 100;
  if (detail.batch.page_count === 0) return null;
  return Math.min(
    100,
    Math.round(
      (detail.pages.filter((item) => item.status === "ready").length /
        detail.batch.page_count) *
        100,
    ),
  );
}

export function captureSummary(detail?: CaptureBatchDetail) {
  return detail
    ? [
        { label: "文件", value: detail.batch.file_count },
        { label: "页面", value: detail.batch.page_count },
        { label: "答卷", value: detail.batch.submission_count },
        { label: "待确认", value: detail.batch.review_count },
        { label: "失败", value: detail.batch.failed_count },
      ]
    : [];
}

export function captureSubmissionIds(detail?: CaptureBatchDetail): string[] {
  return detail
    ? (Array.from(
        new Set(
          detail.pages.map((item) => item.submission_id).filter(Boolean),
        ),
      ) as string[])
    : [];
}

export function captureIssuePages(detail?: CaptureBatchDetail) {
  return detail
    ? detail.pages.filter((item) =>
        ["needs_review", "quality_rejected", "failed"].includes(
          item.status,
        ),
      )
    : [];
}
