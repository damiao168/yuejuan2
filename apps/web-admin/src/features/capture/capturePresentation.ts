export const statusLabels: Record<string, string> = {
  draft: "等待上传",
  uploading: "正在上传",
  matching: "等待匹配",
  processing: "正在处理",
  needs_review: "需要确认",
  ready: "处理完成",
  completed: "已完成",
  cancelled: "已取消",
  uploaded: "已上传",
  queued: "等待处理",
  duplicate: "重复文件",
  failed: "处理失败",
  grouped: "已归入答卷",
  registration: "正在对齐版面",
  deleted: "已删除",
  quality_rejected: "质量不合格",
  normalized: "质量已通过",
  page_matching: "正在匹配模板",
};

export function statusTone(
  status: string,
): "success" | "warning" | "danger" | "processing" | "neutral" {
  if (status === "completed" || status === "ready" || status === "grouped")
    return "success";
  if (status === "failed" || status === "cancelled") return "danger";
  if (
    status === "needs_review" ||
    status === "duplicate" ||
    status === "quality_rejected"
  )
    return "warning";
  if (status === "processing" || status === "queued" || status === "uploading" || status === "page_matching")
    return "processing";
  return "neutral";
}
