import { describe, expect, it } from "vitest";
import { publicationChecklist } from "./publicationChecklist";
import type { ScoreReleaseGate } from "../../api/scoreReleases";
import type { QualityCheckResult, RosterReport } from "../../api/scores";

const gate: ScoreReleaseGate = { version: "1", passed: true, blocking: [], warnings: [], counts: {}, generated_at: "" };
const quality: QualityCheckResult = { stage: "publish", can_publish: true, quality: { passed: true, issues: [] } };
const roster: RosterReport = { entries: [], summary: { expected: 1, received: 1, graded: 1, absent: 0, unresolved: 0, missing_pages: 0, unidentified: 0 } };

describe("统一发布待办", () => {
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
});
