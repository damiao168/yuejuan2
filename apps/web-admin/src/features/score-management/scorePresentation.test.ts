import { describe, expect, it } from "vitest";
import {
  basePublicationLabel, portalPublicationLabel, qualityCheckStatus,
  rosterReconciliationStatus, rosterStatusTone, scoreWorkspacePresentation, summaryStatus, type ScorePublicationState
} from "./scorePresentation";

const published: ScorePublicationState = {
  examId: "exam-1", loadingExams: false, loadingScores: false,
  error: null, examStatus: "published", releaseVersion: 3
};

describe("score publication presentation", () => {
  it("uses the same unresolved roster count for label and tone", () => {
    expect(rosterReconciliationStatus(0)).toEqual({ tone: "success", label: "对账完成" });
    expect(rosterReconciliationStatus(2)).toEqual({ tone: "danger", label: "2 项待处理" });
  });
  it("keeps teacher display copy and classes independent from the administrator workspace", () => {
    const teacher = scoreWorkspacePresentation("teacher");
    const admin = scoreWorkspacePresentation("admin");
    expect(teacher.shellClass).toBe("score-shell read-only");
    expect(teacher.workspaceClass).toBe("score-workspace read-only");
    expect(teacher.title).toBe("班级成绩");
    expect(teacher.emptyGrades).toBe("该考试暂无成绩，请等待阅卷完成。");
    expect(admin.shellClass).toBe("score-shell");
    expect(admin.workspaceClass).toBe("score-workspace");
    expect(admin.title).toBe("成绩发布");
    expect(admin.summaryTitle).toBe("基础成绩检查");
  });
  it.each([
    [{ ...published, examId: "", loadingScores: true, error: "failed" }, "请选择考试", "请选择考试"],
    [{ ...published, loadingScores: true, error: "failed" }, "正在核对", "正在核对"],
    [{ ...published, loadingExams: true }, "正在核对", "当前版本 V3"],
    [{ ...published, error: "failed" }, "状态暂不可用", "状态暂不可用"],
    [published, "已公开并锁定", "当前版本 V3"],
    [{ ...published, examStatus: "finalized", releaseVersion: undefined }, "尚未公开", "门户无当前版本"]
  ] as const)("preserves selection, reload and error precedence: %j", (state, base, portal) => {
    expect(basePublicationLabel(state)).toBe(base);
    expect(portalPublicationLabel(state)).toBe(portal);
  });

  it("uses the current portal pointer even when its version is zero", () => {
    expect(portalPublicationLabel({ ...published, releaseVersion: 0 })).toBe("当前版本 V0");
  });

  it("keeps teacher status independent of administrator publish readiness", () => {
    expect(summaryStatus("teacher", "grading", true, true)).toEqual({ tone: "neutral", label: "阅卷中" });
    expect(summaryStatus("teacher", undefined, true, true)).toEqual({ tone: "neutral", label: "未选择" });
    expect(summaryStatus("teacher", "new-state", false, false)).toEqual({ tone: "neutral", label: "未知状态" });
  });

  it("shows locking before readiness and otherwise retains the blocking state", () => {
    expect(summaryStatus("admin", "grading", true, false)).toEqual({ tone: "success", label: "已锁定" });
    expect(summaryStatus("admin", "grading", false, true)).toEqual({ tone: "success", label: "可公开" });
    expect(summaryStatus("admin", "grading", false, false)).toEqual({ tone: "danger", label: "待处理" });
  });

  it("does not invent a quality result and counts blocking issue records rather than affected papers", () => {
    expect(qualityCheckStatus(null)).toBeNull();
    const quality = {
      stage: "publish", can_publish: false,
      quality: { passed: false, issues: [
        { code: "blocked", message: "blocked", blocking: true, count: 20 },
        { code: "warning", message: "warning", blocking: false, count: 30 }
      ] }
    };
    expect(qualityCheckStatus(quality)).toEqual({ tone: "danger", label: "1 项须处理" });
    expect(qualityCheckStatus({ ...quality, quality: { ...quality.quality, passed: true } })).toEqual({ tone: "success", label: "检查通过" });
  });

  it.each([
    ["graded", "success"], ["absent", "neutral"], ["unmatched", "danger"], ["missing_pages", "danger"], ["future", "danger"]
  ])("retains roster status severity for %s", (status, tone) => {
    expect(rosterStatusTone(status)).toBe(tone);
  });
});
