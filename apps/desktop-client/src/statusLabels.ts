import type { OfflineDraftEnvelope } from "./lib/offlineStore";
import type { SyncQueueItem } from "./types";

export const queueStatusLabels: Record<SyncQueueItem["status"], string> = {
  pending: "等待上传",
  uploading: "上传中",
  succeeded: "已完成",
  failed: "上传失败",
  conflict: "需要处理",
  not_configured: "未配置/待接入"
};

export const offlineSyncStatusLabels: Record<OfflineDraftEnvelope["syncStatus"], string> = {
  draft: "本地草稿",
  syncing: "同步中",
  synced: "已同步",
  failed: "同步失败",
  conflict: "存在冲突"
};

// 任务状态与上传/考试状态分开解释；已提交仍可能等待后续复核，不等于考试完成或成绩发布。
export const reviewTaskStates: Record<string, { label: string; detail: string; canGrade: boolean }> = {
  pending: { label: "待分配", detail: "尚未分配给阅卷教师", canGrade: false },
  assigned: { label: "已分配", detail: "可打开任务开始阅卷", canGrade: true },
  in_progress: { label: "阅卷中", detail: "可继续填写并提交人工评分", canGrade: true },
  returned: { label: "已退回", detail: "请核对退回原因后重新阅卷", canGrade: true },
  submitted: { label: "已提交", detail: "人工评分已提交，等待后续处理", canGrade: false },
  completed: { label: "已完成", detail: "该阅卷任务已完成", canGrade: false },
  cancelled: { label: "已取消", detail: "任务已取消，不能继续提交", canGrade: false },
  withdrawn: { label: "已撤回", detail: "任务已撤回，请刷新任务列表", canGrade: false },
  revoked: { label: "已撤回", detail: "任务已撤回，请刷新任务列表", canGrade: false }
};

export function reviewTaskState(status: string) {
  return reviewTaskStates[status] ?? { label: "状态待核对", detail: "客户端无法识别该任务状态，请刷新或联系学校管理员", canGrade: false };
}

export const genericStatusLabels: Record<string, string> = {
  draft: "草稿",
  planned: "待处理",
  pending: "等待处理",
  queued: "排队中",
  assigned: "已分配",
  in_progress: "处理中",
  processing: "处理中",
  uploading: "上传中",
  uploaded: "已上传",
  completed: "已完成",
  succeeded: "已完成",
  failed: "失败",
  cancelled: "已取消",
  collecting: "采集中",
  grading: "阅卷中",
  reviewing: "复核中",
  finalized: "待发布",
  published: "已发布",
  archived: "已归档"
};

export function genericStatusLabel(status: string): string {
  return genericStatusLabels[status] ?? "未知状态";
}

export const subjectLabels: Record<string, string> = {
  chinese: "语文",
  mathematics: "数学",
  math: "数学",
  english: "英语",
  physics: "物理",
  chemistry: "化学",
  biology: "生物",
  history: "历史",
  geography: "地理",
  politics: "政治",
  ethics_politics: "政治"
};
