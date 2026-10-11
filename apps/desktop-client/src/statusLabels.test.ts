import { describe, expect, it } from "vitest";
import { offlineSyncStatusLabels, queueStatusLabels, reviewTaskState } from "./statusLabels";

describe("desktop user-facing status labels", () => {
  it("separates submitted grading from completion and refuses unknown task actions", () => {
    expect(reviewTaskState("submitted")).toMatchObject({ label: "已提交", canGrade: false });
    expect(reviewTaskState("in_progress")).toMatchObject({ label: "阅卷中", canGrade: true });
    expect(reviewTaskState("returned")).toMatchObject({ label: "已退回", canGrade: true });
    expect(reviewTaskState("withdrawn")).toMatchObject({ label: "已撤回", canGrade: false });
    expect(reviewTaskState("future_state")).toMatchObject({ label: "状态待核对", canGrade: false });
  });
  it("localizes upload queue states", () => {
    expect(queueStatusLabels).toMatchObject({
      pending: "等待上传",
      uploading: "上传中",
      succeeded: "已完成",
      failed: "上传失败"
    });
  });

  it("localizes offline draft sync states", () => {
    expect(offlineSyncStatusLabels).toEqual({
      draft: "本地草稿",
      syncing: "同步中",
      synced: "已同步",
      failed: "同步失败",
      conflict: "存在冲突"
    });
  });
});
