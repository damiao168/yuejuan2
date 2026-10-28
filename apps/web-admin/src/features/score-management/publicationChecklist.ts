import type { ScoreReleaseGate } from "../../api/scoreReleases";
import type { QualityCheckResult, RosterReport } from "../../api/scores";

export type PublicationCheckItem = {
  code: string; message: string; blocking: boolean; count: number;
  actionRoute?: string;
  sources: Array<{ name: string; count: number; message: string }>;
};

export function publicationChecklist(quality: QualityCheckResult | null, gate: ScoreReleaseGate | null, roster: RosterReport | null) {
  const items = new Map<string, PublicationCheckItem>();
  const add = (issue: { code: string; message: string; blocking: boolean; count: number; action_route?: string }, source: string) => {
    const existing = items.get(issue.code);
    if (existing) {
      // 两套检查重复统计同一规则时取较大值，不能相加成更多待办；各自读数保留供核对。
      existing.count = Math.max(existing.count, issue.count);
      existing.blocking ||= issue.blocking;
      existing.actionRoute ||= issue.action_route;
      existing.sources.push({ name: source, count: issue.count, message: issue.message });
    } else items.set(issue.code, { code: issue.code, message: issue.message, count: issue.count, blocking: issue.blocking, actionRoute: issue.action_route, sources: [{ name: source, count: issue.count, message: issue.message }] });
  };
  quality?.quality.issues.forEach(issue => add(issue, "基础成绩"));
  [...(gate?.blocking ?? []), ...(gate?.warnings ?? [])].forEach(issue => add(issue, "门户发布"));
  // 名册按记录数统计，终评可能按题数统计；同规则只合并入口，来源仍明确各自口径。
  if (roster && roster.summary.unresolved > 0) {
    const rosterCodes: Record<string, string> = { missing_submission: "missing_submission_unresolved", grading_incomplete: "missing_final_grades", duplicate_submission: "unidentified_submission", student_unidentified: "unidentified_submission", student_not_in_roster: "unidentified_submission", absent_has_submission: "unidentified_submission", missing_pages: "missing_pages_unresolved" };
    const counts = new Map<string, number>();
    for (const entry of roster.entries ?? []) {
      if (["graded", "absent"].includes(entry.resolution_code)) continue;
      const code = rosterCodes[entry.resolution_code] ?? "roster_unresolved";
      counts.set(code, (counts.get(code) ?? 0) + 1);
    }
    const classified = [...counts.values()].reduce((sum, count) => sum + count, 0);
    if (classified < roster.summary.unresolved) counts.set("roster_unresolved", (counts.get("roster_unresolved") ?? 0) + roster.summary.unresolved - classified);
    for (const [code, count] of counts) add({ code, message: `名册中 ${count} 条记录尚未解决`, blocking: true, count, action_route: "roster" }, "名册对账（记录数）");
  }
  const ready = Boolean(quality && gate && roster);
  return { ready, passed: ready && Boolean(quality?.can_publish && quality.quality.passed && gate?.passed && roster?.summary.unresolved === 0) && ![...items.values()].some(item => item.blocking), items: [...items.values()].sort((a, b) => Number(b.blocking) - Number(a.blocking)) };
}
