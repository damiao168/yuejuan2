import type { ScoreReleaseGate } from "../../api/scoreReleases";
import type { QualityCheckResult, RosterReport } from "../../api/scores";

export type PublicationCheckItem = {
  code: string; message: string; blocking: boolean; count: number;
  actionRoute?: string;
  sources: Array<{ name: string; count: number; message: string }>;
};

const checkLabels: Record<string, string> = {
  unfinished_review_tasks: "完成阅卷", unfinished_arbitration_tasks: "完成仲裁",
  ocr_failed_unhandled: "处理识别异常", missing_final_grades: "补齐小题得分",
  grades_not_confirmed: "确认本场成绩", no_submission_grades: "生成成绩",
  missing_submission_unresolved: "核查未收到的答卷", unidentified_submission: "核对答卷身份",
  missing_pages_unresolved: "补齐答卷页面", roster_unresolved: "核对考试名单",
  question_coverage_incomplete: "核对小题得分与满分", score_integrity_mismatch: "重新核对成绩汇总",
  release_snapshot_incomplete: "补齐发布版本的小题成绩", quality_questions_missing: "保存本场考试的小题",
  regrade_release_pending: "完成复评", missing_submission_grades: "补齐答卷总分", capture_collection_incomplete: "结束本场答卷导入"
};

export function publicationCheckPresentation(item: PublicationCheckItem) {
  const situations: Record<string, string> = {
    quality_questions_missing: "当前选中的考试尚未保存小题。请先核对考试名称，再添加或导入小题并保存分值。",
    capture_collection_incomplete: "本场答卷导入尚未结束。请先核对未匹配答卷的学生，结束本场导入后再发布。",
    missing_submission_unresolved: `还有 ${item.count} 项答卷匹配需要核对。`,
    no_submission_grades: "本场考试还没有已汇总的成绩。阅卷完成后，发布时会自动汇总。",
    grades_not_confirmed: `还有 ${item.count} 份成绩未确认。`,
    unfinished_review_tasks: `还有 ${item.count} 项阅卷任务未完成。`,
    unfinished_arbitration_tasks: `还有 ${item.count} 项仲裁任务未完成。`,
    ocr_failed_unhandled: `还有 ${item.count} 项识别异常未处理。`,
    missing_pages_unresolved: "仍有答卷存在缺页或页面异常。",
    unidentified_submission: "仍有答卷身份或重复记录需要核对。",
    roster_unresolved: `还有 ${item.count} 条考试名单记录需要核对。`,
    missing_final_grades: "仍有答卷的小题尚未完成最终评分。",
    question_coverage_incomplete: "仍有答卷的小题得分或满分与考试配置不一致。",
    score_integrity_mismatch: "当前总分与最新小题得分不一致，需要重新汇总。",
    release_snapshot_incomplete: `有 ${item.count} 项发布版本的小题成绩不完整。`,
    regrade_release_pending: `还有 ${item.count} 项复评结果尚未生成可发布版本。`,
    missing_submission_grades: `有 ${item.count} 份答卷无法自动汇总总分，通常是仍有小题没有评分。`
  };
  return { label: checkLabels[item.code] ?? "核对发布条件", situation: situations[item.code] ?? item.message };
}

const checkOrder = ["quality_questions_missing", "missing_submission_unresolved", "unidentified_submission", "missing_pages_unresolved", "ocr_failed_unhandled", "roster_unresolved", "unfinished_review_tasks", "unfinished_arbitration_tasks", "missing_final_grades", "no_submission_grades", "missing_submission_grades", "question_coverage_incomplete", "score_integrity_mismatch", "grades_not_confirmed", "regrade_release_pending", "release_snapshot_incomplete"];

const taskGroups = [
  { key: "preparation", label: "考试准备", codes: ["quality_questions_missing"], routes: ["questions"] },
  { key: "collection", label: "答卷收集", codes: ["capture_collection_incomplete", "missing_submission_unresolved", "unidentified_submission", "missing_pages_unresolved", "ocr_failed_unhandled", "roster_unresolved"], routes: ["roster", "capture", "processing"] },
  { key: "grading", label: "阅卷", codes: ["unfinished_review_tasks", "unfinished_arbitration_tasks", "missing_final_grades", "regrade_release_pending"], routes: ["grading", "arbitration", "regrade"] },
  { key: "scores", label: "成绩汇总", codes: ["no_submission_grades", "missing_submission_grades", "question_coverage_incomplete", "score_integrity_mismatch", "grades_not_confirmed", "release_snapshot_incomplete"], routes: [] }
];

const shortCheckLabels: Record<string, string> = {
  quality_questions_missing: "小题未保存", missing_submission_unresolved: "答卷未收齐", capture_collection_incomplete: "答卷导入尚未结束",
  unidentified_submission: "身份待核对", missing_pages_unresolved: "页面待补齐",
  ocr_failed_unhandled: "识别异常", roster_unresolved: "名单待核对",
  unfinished_review_tasks: "阅卷未完成", unfinished_arbitration_tasks: "仲裁未完成",
  missing_final_grades: "小题评分未完成", regrade_release_pending: "复评未完成",
  no_submission_grades: "成绩待汇总", question_coverage_incomplete: "小题得分与满分待核对",
  score_integrity_mismatch: "总分待重算", grades_not_confirmed: "成绩待确认",
  release_snapshot_incomplete: "发布版本成绩待补齐", missing_submission_grades: "答卷总分待补齐"
};

export function publicationTaskGroups(items: PublicationCheckItem[]) {
  // 按负责环节合并入口；未知阻断仍留在成绩检查中，不能因没有映射而消失。
  return taskGroups.map(group => {
    const groupedItems = items.filter(item => item.blocking && (taskGroups.find(candidate => candidate.codes.includes(item.code))
      ?? taskGroups.find(candidate => candidate.routes.includes(item.actionRoute ?? ""))
      ?? taskGroups[taskGroups.length - 1]).key === group.key);
    const labels = groupedItems.map(item => shortCheckLabels[item.code] ?? "发布条件待核对");
    return { key: group.key, label: group.label, items: groupedItems,
      summary: labels.length > 2 ? `${labels.slice(0, 2).join("、")}等` : labels.join("、") };
  }).filter(group => group.items.length > 0);
}

/** 发布时由服务端在同一事务里自动汇总、确认；它们只说明“还没生成草稿”，不是管理员待办。 */
export const automaticPreparationCodes = new Set(["no_submission_grades", "grades_not_confirmed"]);

// 名册中这两类结论已经无须人工处理：缺考按无匹配答卷自动判断，等待汇总的答卷在发布时自动生成总分。
const settledRosterResolutions = new Set(["graded", "absent", "awaiting_aggregation"]);

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
    const rosterCodes: Record<string, string> = { missing_submission: "missing_submission_unresolved", grading_incomplete: "missing_final_grades", duplicate_submission: "unidentified_submission", student_unidentified: "unidentified_submission", student_identity_unresolved: "unidentified_submission", student_not_in_roster: "unidentified_submission", absent_has_submission: "unidentified_submission", missing_pages: "missing_pages_unresolved" };
    const counts = new Map<string, number>();
    for (const entry of roster.entries ?? []) {
      if (settledRosterResolutions.has(entry.resolution_code)) continue;
      const code = rosterCodes[entry.resolution_code] ?? "roster_unresolved";
      counts.set(code, (counts.get(code) ?? 0) + 1);
    }
    const classified = [...counts.values()].reduce((sum, count) => sum + count, 0);
    if (classified < roster.summary.unresolved) counts.set("roster_unresolved", (counts.get("roster_unresolved") ?? 0) + roster.summary.unresolved - classified);
    for (const [code, count] of counts) add({ code, message: `名册中 ${count} 条记录尚未解决`, blocking: true, count, action_route: "roster" }, "名册对账（记录数）");
  }
  const ready = Boolean(quality && gate && roster);
  // 先展示小题、收卷等前置问题，再展示汇总与确认；顺序只改变引导，不替代服务端发布门禁。
  const order = (code: string) => { const index = checkOrder.indexOf(code); return index < 0 ? checkOrder.length : index; };
  return { ready, passed: ready && Boolean(quality?.can_publish && quality.quality.passed && gate?.passed && roster?.summary.unresolved === 0) && ![...items.values()].some(item => item.blocking), items: [...items.values()].sort((a, b) => Number(b.blocking) - Number(a.blocking) || order(a.code) - order(b.code)) };
}

export type PublicationReadiness = Readonly<{
  /** loading：检查未读全；blocked：有管理员可处理的阻断；unexplained：检查失败但没有给出原因；ready：可以发布。 */
  status: "loading" | "blocked" | "unexplained" | "ready";
  groups: ReturnType<typeof publicationTaskGroups>;
  reminders: PublicationCheckItem[];
}>;

/**
 * 页面据此决定主按钮。只剩自动准备项时视为可以发布，因为生成发布结果时服务端会先汇总确认再检查；
 * 这只是界面引导，服务端发布门禁仍会在事务中重新判断。
 */
export function publicationReadiness(quality: QualityCheckResult | null, gate: ScoreReleaseGate | null, roster: RosterReport | null): PublicationReadiness {
  const checklist = publicationChecklist(quality, gate, roster);
  const reminders = checklist.items.filter(item => !item.blocking);
  if (!checklist.ready) return { status: "loading", groups: [], reminders };
  const groups = publicationTaskGroups(checklist.items.filter(item => !automaticPreparationCodes.has(item.code)));
  if (groups.length) return { status: "blocked", groups, reminders };
  // 某一来源判定失败却没有任何阻断说明时，不能因为另一来源只有自动项就当作通过。
  const explained = (source: string) => checklist.items.some(item => item.blocking && item.sources.some(entry => entry.name === source));
  const qualityFailed = !quality?.quality.passed || !quality.can_publish;
  const unexplained = (qualityFailed && !explained("基础成绩")) || (!gate?.passed && !explained("门户发布"));
  return { status: unexplained ? "unexplained" : "ready", groups, reminders };
}
