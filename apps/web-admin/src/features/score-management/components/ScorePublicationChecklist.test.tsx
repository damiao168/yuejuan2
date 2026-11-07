// @vitest-environment jsdom
import { act } from "react";
import { createRoot } from "react-dom/client";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";
import type { ScoreReleaseGate } from "../../../api/scoreReleases";
import type { QualityCheckResult, RosterReport } from "../../../api/scores";
import { ScorePublicationChecklist } from "./ScorePublicationChecklist";

const quality: QualityCheckResult = { stage: "publish", can_publish: false, quality: { passed: false, issues: [
  { code: "missing_submission_unresolved", message: "基础成绩检查发现仍有学生没有答卷", blocking: true, count: 3 },
  { code: "no_submission_grades", message: "阅卷完成后请先汇总成绩", blocking: true, count: 1 }
] } };
const gate: ScoreReleaseGate = { version: "1", passed: false, blocking: [
  { code: "missing_submission_unresolved", message: "发布前请核对未收到的答卷", blocking: true, count: 3, action_route: "roster" },
  { code: "quality_questions_missing", message: "考试小题尚未保存", blocking: true, count: 1, action_route: "questions" }
], warnings: [], counts: {}, generated_at: "" };
const roster: RosterReport = { entries: [1, 2, 3].map(index => ({ key: `student-${index}`, student_id: `student-${index}`, status: "unmatched" as const, resolution_code: "missing_submission", expected_page_count: 1, actual_page_count: 0 })), summary: { expected: 3, received: 0, graded: 0, absent: 0, unresolved: 3, missing_pages: 0, unidentified: 0 } };

const completeQuality = { ...quality, can_publish: true, quality: { passed: true, issues: [] } };
const completeRoster = { ...roster, entries: [], summary: { ...roster.summary, unresolved: 0 } };

function markup(qualityResult = quality, gateResult = gate, rosterResult = roster, actionFor = vi.fn(() => undefined) as (code: string, route?: string) => { label: string; run: () => void } | undefined) {
  const host = document.createElement("div");
  host.innerHTML = renderToStaticMarkup(<ScorePublicationChecklist quality={qualityResult} gate={gateResult} roster={rosterResult} actionFor={actionFor} />);
  return host;
}

describe("成绩发布分环节检查", () => {
  it("按前置依赖合并入口，详情保留原因且默认关闭，不显示内部代码", () => {
    const actionFor = vi.fn((code: string) => ({ label: code === "quality_questions_missing" ? "查看本场考试小题" : "处理", run: vi.fn() }));
    const host = markup(quality, gate, roster, actionFor);
    expect([...host.querySelectorAll(".score-publication-task-heading strong")].map(node => node.textContent)).toEqual(["考试准备", "答卷收集"]);
    expect(host.textContent).toContain("2 个环节待完成");
    expect(host.querySelectorAll(".score-publication-task-group")).toHaveLength(2);
    expect(host.querySelectorAll(".score-publication-task-group button")).toHaveLength(2);
    expect(host.textContent).toContain("还有 3 项答卷匹配需要核对");
    expect(host.textContent).toContain("基础成绩检查发现仍有学生没有答卷");
    expect(host.textContent).toContain("发布前请核对未收到的答卷");
    expect([...host.querySelectorAll("details")].every(details => !details.open)).toBe(true);
    for (const text of ["quality_questions_missing", "missing_submission_unresolved", "核对来源", "技术详情", "<code"]) expect(host.innerHTML).not.toContain(text);
    expect(actionFor).toHaveBeenCalledWith("quality_questions_missing", "questions");
  });

  it("自动汇总不再产生手工入口，真实问题和非阻断提醒继续区分", () => {
    const issues = ["unidentified_submission", "missing_pages_unresolved", "ocr_failed_unhandled", "unfinished_review_tasks", "missing_final_grades", "no_submission_grades"].map(code => ({ code, message: "需要完成对应环节", blocking: true, count: 2 }));
    const warnings = ["total_score_warning", "question_score_warning", "question_coverage_warning"].map(code => ({ code, message: "请核对本场考试的满分", blocking: false, count: 1 }));
    const host = markup({ ...quality, quality: { passed: false, issues } }, { ...gate, blocking: [], warnings }, completeRoster, () => ({ label: "进入处理", run: vi.fn() }));
    const groups = host.querySelectorAll(".score-publication-task-group");
    expect(groups).toHaveLength(2);
    expect([...groups].map(group => group.querySelectorAll("button").length)).toEqual([1, 1]);
    expect([...host.querySelectorAll(".score-publication-task-heading .muted")].map(node => node.textContent)).toEqual(["3 项检查", "2 项检查"]);
    const reminders = host.querySelector<HTMLDetailsElement>(".score-publication-reminders")!;
    expect(reminders.open).toBe(false);
    expect(reminders.querySelectorAll("li")).toHaveLength(3);
    expect(reminders.textContent).toContain("不影响发布");
    expect(reminders.querySelector("button")).toBeNull();
    expect(host.textContent).not.toContain("检查已通过");
  });

  it("每个环节保留第一个当前可用入口，并执行原操作", async () => {
    const action = vi.fn();
    const actionFor = vi.fn((code: string) => code === "missing_pages_unresolved" ? { label: "核查答卷", run: action } : undefined);
    const result = { ...gate, blocking: [...gate.blocking.filter(issue => issue.code === "missing_submission_unresolved"), { code: "missing_pages_unresolved", message: "请补齐缺页答卷", count: 1, blocking: true }] };
    const host = document.createElement("div"); document.body.append(host);
    const root = createRoot(host);
    (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
    try {
      await act(async () => root.render(<ScorePublicationChecklist quality={completeQuality} gate={result} roster={completeRoster} actionFor={actionFor} />));
      expect(host.querySelectorAll("button")).toHaveLength(1);
      await act(async () => host.querySelector("button")!.click());
      expect(action).toHaveBeenCalledOnce();
      expect(host.querySelector(".score-publication-task-group")?.textContent).toContain("2 项检查");
    } finally { await act(async () => root.unmount()); host.remove(); }
  });

  it("仅待自动汇总确认时不要求管理员再次操作，其他真实阻断仍展示", () => {
    const issues = ["no_submission_grades", "grades_not_confirmed"].map(code => ({ code, message: "内部准备状态", blocking: true, count: 1 }));
    const actionFor = vi.fn(() => undefined);
    const host = markup({ ...quality, quality: { passed: false, issues } }, { ...gate, blocking: issues }, completeRoster, actionFor);
    expect(host.textContent).toBe("");
    expect(actionFor).not.toHaveBeenCalled();
    const blocked = markup({ ...quality, quality: { passed: false, issues } }, { ...gate, blocking: [...issues, { code: "missing_pages_unresolved", message: "缺页", count: 1, blocking: true }] }, completeRoster);
    expect(blocked.textContent).toContain("页面待补齐");
  });

  // 只剩自动汇总确认项时服务端发布前会自动处理；不能再提示管理员“刷新核对”，刷新也不会改变结果。
  it("只剩自动准备项且有提醒时显示检查已通过，不再提示刷新", () => {
    const issues = ["no_submission_grades", "grades_not_confirmed"].map(code => ({ code, message: "内部准备状态", blocking: true, count: 1 }));
    const warnings = [{ code: "question_coverage_warning", message: "标准卷覆盖不足", blocking: false, count: 1 }];
    const host = markup({ ...quality, quality: { passed: false, issues } }, { ...gate, blocking: issues, warnings }, completeRoster);
    expect(host.textContent).toContain("检查已通过");
    expect(host.textContent).toContain("其他提醒 · 1 项");
    expect(host.textContent).not.toContain("请刷新");
    expect(host.textContent).not.toContain("检查尚未通过");
  });

  it("未读取完整或无明细的失败门禁仍不能显示通过；仅提醒可以通过", () => {
    const pending = renderToStaticMarkup(<ScorePublicationChecklist quality={null} gate={null} roster={null} actionFor={() => undefined} />);
    expect(pending).toContain("检查尚未读取完整，暂不能确认发布条件");
    expect(pending).not.toContain("检查已通过");
    const failed = markup(completeQuality, { ...gate, blocking: [], warnings: [] }, completeRoster);
    expect(failed.textContent).toContain("当前检查尚未通过");
    expect(failed.textContent).not.toContain("检查已通过");
    const host = markup(completeQuality, { ...gate, passed: true, blocking: [], warnings: [{ code: "custom", message: "请核对考试名称 custom_warning", blocking: false, count: 1 }] }, completeRoster);
    expect(host.textContent).toContain("检查已通过");
    expect(host.textContent).toContain("请核对考试名称");
    expect(host.innerHTML).not.toContain("custom_warning");
    expect(host.querySelector<HTMLDetailsElement>(".score-publication-reminders")?.open).toBe(false);
  });
});
