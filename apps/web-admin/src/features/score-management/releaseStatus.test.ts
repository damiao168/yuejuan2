import { describe, expect, it } from "vitest";
import type { RegradeJob, ScoreRelease } from "../../api/scoreReleases";
import { activeRegradeCount, formatReleaseTime, pendingCorrections, releaseStatusModel, rosterCounts, type ReleaseStatusInput } from "./releaseStatus";

const base: ReleaseStatusInput = { examId: "exam-1", loading: false, error: null, readiness: "ready", blockedGroups: 0, pendingCorrections: 0 };
const release = (id: string, version: number, status: string, extra: Record<string, unknown> = {}) =>
  ({ id, version, status, published_at: status === "published" ? "2026-10-31T13:30:00Z" : null, ...extra }) as unknown as ScoreRelease;
const job = (id: string, status: string, extra: Record<string, unknown> = {}) =>
  ({ id, status, updated_at: "2026-10-31T10:00:00Z", ...extra }) as unknown as RegradeJob;

describe("学生端发布状态", () => {
  it("读取中或读取失败时不给发布、导出入口，避免依据旧快照操作", () => {
    expect(releaseStatusModel({ ...base, examId: "" })).toEqual({ tone: "neutral", text: "请选择考试" });
    expect(releaseStatusModel({ ...base, loading: true, publishedRelease: release("r1", 1, "published") }).primary).toBeUndefined();
    expect(releaseStatusModel({ ...base, error: "失败" }).primary).toBeUndefined();
  });

  it("未发布时按检查结论给出唯一主按钮", () => {
    expect(releaseStatusModel(base)).toEqual({ tone: "processing", text: "未发布 · 检查已通过", primary: { kind: "publish", label: "发布成绩" } });
    expect(releaseStatusModel({ ...base, readiness: "blocked", blockedGroups: 2 }).primary).toEqual({ kind: "locate", label: "还有 2 项待处理" });
    expect(releaseStatusModel({ ...base, readiness: "unexplained" }).primary?.kind).toBe("locate");
    expect(releaseStatusModel({ ...base, readiness: "loading" }).primary).toBeUndefined();
  });

  it("已发布时显示版本和时间；有待发布更正时主按钮改为发布更正", () => {
    const published = release("r1", 3, "published");
    const text = releaseStatusModel({ ...base, publishedRelease: published }).text;
    expect(text).toMatch(/^已发布 V3 · \d{4}-\d{2}-\d{2} \d{2}:\d{2}$/);
    expect(releaseStatusModel({ ...base, publishedRelease: published }).primary?.kind).toBe("export");
    const correction = releaseStatusModel({ ...base, publishedRelease: published, pendingCorrections: 1 });
    expect(correction.text).toContain("有 1 项更正待发布");
    expect(correction.primary).toEqual({ kind: "correction", label: "发布更正" });
  });

  it("不能解析的发布时间不显示，不编造时间", () => {
    expect(formatReleaseTime("not-a-date")).toBe("");
    expect(formatReleaseTime(null)).toBe("");
  });
});

describe("待发布更正", () => {
  it("复评完成后按结果版本是否已发布判断，未生成结果版本的仍待发布", () => {
    const jobs = [job("done", "ready_for_release"), job("drafted", "ready_for_release"), job("fresh", "ready_for_release"), job("running", "running"), job("cancelled", "cancelled")];
    const releases = [release("p", 2, "published", { regrade_job_id: "done" }), release("d", 3, "draft", { regrade_job_id: "drafted" })];
    const pending = pendingCorrections(jobs, releases);
    expect(pending.map(item => item.job.id)).toEqual(["drafted", "fresh"]);
    expect(pending[0].draft?.id).toBe("d");
    expect(pending[1].draft).toBeUndefined();
    expect(activeRegradeCount(jobs)).toBe(1);
  });
});

describe("名单人数", () => {
  it("有成绩人数包括等待自动汇总的答卷", () => {
    const counts = rosterCounts({
      entries: [
        { key: "a", status: "graded", resolution_code: "graded", expected_page_count: 1, actual_page_count: 1 },
        { key: "b", status: "submitted", resolution_code: "awaiting_aggregation", expected_page_count: 1, actual_page_count: 1 },
        { key: "c", status: "absent", resolution_code: "absent", expected_page_count: 1, actual_page_count: 0 }
      ],
      summary: { expected: 3, received: 2, graded: 1, absent: 1, unresolved: 0, missing_pages: 0, unidentified: 0 }
    });
    expect(counts).toEqual({ expected: 3, received: 2, absent: 1, withScores: 2 });
    expect(rosterCounts(null)).toBeNull();
  });
});
