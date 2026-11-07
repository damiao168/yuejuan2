import { describe, expect, it } from "vitest";
import { publicationCheckPresentation, publicationChecklist, publicationReadiness, publicationTaskGroups } from "./publicationChecklist";
import type { ScoreReleaseGate } from "../../api/scoreReleases";
import type { QualityCheckResult, RosterReport } from "../../api/scores";

const gate: ScoreReleaseGate = { version: "1", passed: true, blocking: [], warnings: [], counts: {}, generated_at: "" };
const quality: QualityCheckResult = { stage: "publish", can_publish: true, quality: { passed: true, issues: [] } };
const roster: RosterReport = { entries: [], summary: { expected: 1, received: 1, graded: 1, absent: 0, unresolved: 0, missing_pages: 0, unidentified: 0 } };

describe("统一发布待办", () => {
  it("尚未结束收卷的阻断留在答卷收集环节，并明确说明下一步", () => {
    const item = { code: "capture_collection_incomplete", message: "collection incomplete", blocking: true, count: 2, action_route: "capture" };
    const result = publicationChecklist(quality, { ...gate, passed: false, blocking: [item] }, roster);
    expect(result.passed).toBe(false);
    expect(result.items[0].actionRoute).toBe("capture");
    expect(publicationTaskGroups(result.items)[0]).toMatchObject({ key: "collection", summary: "答卷导入尚未结束" });
    expect(publicationCheckPresentation(result.items[0]).label).toBe("结束本场答卷导入");
  });
  it("自动缺考不生成补卷待办，已交卷未汇总和身份待核对仍分别阻断", () => {
    const absent = { key: "student-a", status: "absent" as const, resolution_code: "absent", expected_page_count: 2, actual_page_count: 0 };
    const absentRoster = { entries: [absent], summary: { ...roster.summary, graded: 0, received: 0, absent: 1 } };
    expect(publicationChecklist(quality, gate, absentRoster)).toMatchObject({ passed: true, items: [] });
    const waiting = { ...absent, key: "student-b", status: "submitted" as const, resolution_code: "grading_incomplete", actual_page_count: 2 };
    const identity = { ...waiting, key: "student-c", status: "unmatched" as const, resolution_code: "student_identity_unresolved" };
    const result = publicationChecklist(quality, gate, { entries: [absent, waiting, identity], summary: { ...absentRoster.summary, expected: 3, received: 2, unresolved: 2 } });
    expect(result.passed).toBe(false);
    expect(result.items.map(item => item.code)).toEqual(["unidentified_submission", "missing_final_grades"]);
    expect(result.items.every(item => item.count === 1)).toBe(true);
  });
  it("相同规则合并为一次处理，保留不同来源读数，使用较大值而非相加", () => {
    const issue = { code: "unfinished_review_tasks", message: "待阅", blocking: true, count: 2 };
    const result = publicationChecklist({ ...quality, can_publish: false, quality: { passed: false, issues: [issue] } }, { ...gate, passed: false, blocking: [{ ...issue, count: 3 }] }, roster);
    expect(result.passed).toBe(false);
    expect(result.items).toHaveLength(1);
    expect(result.items[0].count).toBe(3);
    expect(result.items[0].sources.map(source => source.count)).toEqual([2, 3]);
  });
  it("任一检查缺失或名册仍未解决时，不显示全部通过", () => {
    expect(publicationChecklist(quality, null, roster).passed).toBe(false);
    expect(publicationChecklist(quality, gate, null).ready).toBe(false);
    const result = publicationChecklist(quality, gate, { ...roster, summary: { ...roster.summary, unresolved: 1 } });
    expect(result.passed).toBe(false);
    expect(result.items[0].code).toBe("roster_unresolved");
    expect(publicationChecklist(quality, gate, roster).passed).toBe(true);
  });
  it("不同规则不因文案相同而合并，提醒仍可见且不单独阻断", () => {
    const result = publicationChecklist(quality, { ...gate, warnings: [{ code: "a", message: "需核查", count: 1, blocking: false }, { code: "b", message: "需核查", count: 2, blocking: false }] }, roster);
    expect(result.items).toHaveLength(2);
    expect(result.passed).toBe(true);
  });
  it("按负责环节汇总检查项，不相加不同口径人数、题数和记录数", () => {
    const issues = [
      { code: "grades_not_confirmed", count: 3 }, { code: "missing_final_grades", count: 20 },
      { code: "missing_pages_unresolved", count: 2 }, { code: "quality_questions_missing", count: 1 },
      { code: "unidentified_submission", count: 4 }
    ].map(issue => ({ ...issue, message: "请先处理", blocking: true }));
    const result = publicationChecklist({ ...quality, can_publish: false, quality: { passed: false, issues } }, gate, roster);
    const groups = publicationTaskGroups(result.items);
    expect(groups.map(group => group.label)).toEqual(["考试准备", "答卷收集", "阅卷", "成绩汇总"]);
    expect(groups.map(group => group.items.length)).toEqual([1, 2, 1, 1]);
    expect(groups[1].items.map(item => item.count)).toEqual([4, 2]);
    expect(result.passed).toBe(false);
  });
  it("未知阻断保留，已知规则优先于通用路由，提醒不混入阻断分组", () => {
    const issues = [
      { code: "future_rule", action_route: "questions" },
      { code: "future_unmapped" },
      { code: "missing_final_grades", action_route: "roster" }
    ].map(issue => ({ ...issue, message: "发布检查需要处理", count: 1, blocking: true }));
    const result = publicationChecklist(quality, { ...gate, passed: false, blocking: issues, warnings: [{ code: "warning", message: "可选提醒", count: 1, blocking: false }] }, roster);
    const groups = publicationTaskGroups(result.items);
    expect(groups.map(group => group.key)).toEqual(["preparation", "grading", "scores"]);
    expect(groups.flatMap(group => group.items).map(item => item.code)).toHaveLength(3);
    expect(result.items).toHaveLength(4);
    expect(result.passed).toBe(false);
  });
});

describe("发布就绪判断", () => {
  const ready = (q: QualityCheckResult | null, g: ScoreReleaseGate | null, r: RosterReport | null) => publicationReadiness(q, g, r).status;
  it("等待自动汇总的答卷不算名册待办；仍缺小题评分的答卷继续阻断", () => {
    const awaiting = { key: "student-a", status: "submitted" as const, resolution_code: "awaiting_aggregation", expected_page_count: 2, actual_page_count: 2 };
    expect(ready(quality, gate, { entries: [awaiting], summary: { ...roster.summary, graded: 0, unresolved: 0 } })).toBe("ready");
    const incomplete = { ...awaiting, key: "student-b", resolution_code: "grading_incomplete" };
    const result = publicationReadiness(quality, gate, { entries: [awaiting, incomplete], summary: { ...roster.summary, unresolved: 1 } });
    expect(result.status).toBe("blocked");
    expect(result.groups.map(group => group.key)).toEqual(["grading"]);
  });
  it("只剩自动准备项时可以发布；某一来源失败却没有任何原因时不能当作通过", () => {
    const automatic = [{ code: "no_submission_grades", message: "内部准备状态", blocking: true, count: 1 }];
    expect(ready({ ...quality, can_publish: false, quality: { passed: false, issues: automatic } }, { ...gate, passed: false, blocking: automatic }, roster)).toBe("ready");
    expect(ready({ ...quality, can_publish: false, quality: { passed: false, issues: automatic } }, { ...gate, passed: false }, roster)).toBe("unexplained");
    expect(ready(quality, null, roster)).toBe("loading");
  });
});
