// @vitest-environment jsdom
import { act, type ReactNode } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Exam } from "../../../api/exams";
import type { ReadinessScoringPreview } from "../../../api/configuration";
import { ExamScoringPreparation } from "./ExamScoringPreparation";

const api = vi.hoisted(() => ({ questions: vi.fn(), profiles: vi.fn(), get: vi.fn(), put: vi.fn(), grades: vi.fn(), navigate: vi.fn() }));
vi.mock("../../../api/papers", () => ({ listQuestions: api.questions }));
vi.mock("../../../api/assessment", () => ({ listSubjectProfiles: api.profiles, getQuestionAssessmentProfile: api.get, updateQuestionAssessmentProfile: api.put }));
vi.mock("../../../api/org", () => ({ listGrades: api.grades }));
vi.mock("antd", () => ({
  Alert: ({ message, description }: { message: ReactNode; description?: ReactNode }) => <aside>{message}{description}</aside>,
  Button: ({ children, onClick, "aria-expanded": expanded }: { children: ReactNode; onClick?: () => void; "aria-expanded"?: boolean }) => <button aria-expanded={expanded} onClick={onClick}>{children}</button>,
  Table: ({ dataSource, columns }: { dataSource: unknown[]; columns: { title: string; render: (_: undefined, row: never) => ReactNode }[] }) => <table><tbody>{dataSource.map((row, index) => <tr key={index}>{columns.map((column) => <td key={column.title}>{column.render(undefined, row as never)}</td>)}</tr>)}</tbody></table>
}));

function preview(mode: string, source: ReadinessScoringPreview["source"] = "default", extra: Partial<ReadinessScoringPreview> = {}): ReadinessScoringPreview {
  return { question_id: "q1", question_no: "1", scoring_policy: { mode }, source, profile: { id: "senior-math", code: "senior-math", education_stage: "senior", subject_code: "mathematics", version: 1 }, archetype_code: "short_constructed", risk_tier: "R2", allowed_evidence_types: ["text_span"], reason: "已按本场题型匹配", ...extra } as ReadinessScoringPreview;
}

describe("开考评分方案摘要", () => {
  let root: Root; let host: HTMLDivElement;
  const exam = { id: "exam/1", status: "configured", grading_mode: "double_mark" } as Exam;
  beforeEach(() => {
    vi.resetAllMocks(); Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
    host = document.createElement("div"); document.body.append(host); root = createRoot(host);
  });
  afterEach(async () => { await act(async () => root.unmount()); host.remove(); });
  async function render(rows?: ReadinessScoringPreview[], canManage = true, status = "configured") { await act(async () => root.render(<ExamScoringPreparation exam={{ ...exam, status } as Exam} preview={rows} canManage={canManage} onNavigate={api.navigate} />)); }
  async function click(label: string) { await act(async () => [...host.querySelectorAll("button")].find((button) => button.textContent === label)!.click()); }

  it("直接展示有效默认方案，默认折叠明细且不要求选择学段模板或逐题保存", async () => {
    await render([preview("DUAL_HUMAN"), preview("RULE_AUTO", "default", { question_id: "q2", question_no: "2" })]);
    expect(host.textContent).toContain("已匹配 2 题");
    expect(host.textContent).toContain("两名教师独立评分 1 题");
    expect(host.textContent).toContain("规则自动判分 1 题");
    expect(host.querySelector("table")).toBeNull();
    expect(host.querySelector("select")).toBeNull();
    expect(host.textContent).not.toMatch(/未保存|必填|核对并补齐|刷新评分/);
    expect(api.questions).not.toHaveBeenCalled(); expect(api.get).not.toHaveBeenCalled(); expect(api.profiles).not.toHaveBeenCalled(); expect(api.grades).not.toHaveBeenCalled(); expect(api.put).not.toHaveBeenCalled();
  });
  it("展开后沿用人工及双评设置，并保留逐题调整入口", async () => {
    await render([preview("HUMAN_PRIMARY", "saved"), preview("DUAL_HUMAN", "saved", { question_id: "q2", question_no: "2" })]);
    expect(host.textContent).toContain("沿用单题设置 2 题");
    await click("查看 / 调整");
    expect(host.querySelectorAll("tr")).toHaveLength(2); expect(host.textContent).toContain("教师人工评分"); expect(host.textContent).toContain("两名教师独立评分");
    expect(host.querySelector("table")?.textContent).toContain("高中数学 · 第 1 版");
    await click("前往题目页逐题调整"); expect(api.navigate).toHaveBeenCalledWith("/exams/exam%2F1/questions");
    expect(api.put).not.toHaveBeenCalled();
  });
  it("真实异常按题号说明原因，并按服务端指定位置处理", async () => {
    await render([preview("AI_ASSIST", "default", { question_no: "3(2)", blocking_reason: "本题评分方式需要核对", blocking_section: "questions" })]);
    expect(host.textContent).toContain("待处理 1 项"); expect(host.textContent).toContain("第 3(2) 题"); expect(host.textContent).toContain("本题评分方式需要核对");
    expect(host.textContent).not.toContain("系统建议分数，教师逐题确认 1 题");
    await click("去处理"); expect(api.navigate).toHaveBeenCalledWith("/exams/exam%2F1/questions");
  });
  it("整区填空只有共用作答区域时按实际处理方式显示人工，保留原评分策略", async () => {
    const shared = preview("RULE_AUTO", "default", { processing_mode: "MANUAL_ONLY", reason: "整区填空没有单题作答区域，转人工评分" });
    await render([shared]);
    expect(host.textContent).toContain("仅人工评分 1 题"); expect(host.textContent).not.toContain("规则自动判分");
    await click("查看 / 调整");
    expect(host.querySelector("table")?.textContent).toContain("仅人工评分"); expect(host.querySelector("table")?.textContent).toContain("没有单题作答区域");
    expect(shared.scoring_policy.mode).toBe("RULE_AUTO");
  });
  it("整卷相同考试级异常合并为一个入口，题目级异常仍保留题号", async () => {
    const reason = "本场学段存在冲突，请核对学生年级";
    const rows = Array.from({ length: 19 }, (_, index) => preview("AI_ASSIST", "default", { question_id: `q${index + 1}`, question_no: String(index + 1), blocking_reason: reason, blocking_section: "exam" }));
    rows.push(preview("AI_ASSIST", "saved", { question_id: "q20", question_no: "3(2)", blocking_reason: reason, blocking_section: "questions" }));
    await render(rows);
    const issues = host.querySelector('[aria-label="待处理评分方案"]')!;
    expect(issues.querySelectorAll("li")).toHaveLength(2); expect(issues.textContent).toContain("第 3(2) 题");
    expect(host.querySelector(".scoring-verification-count")?.textContent).toContain("待处理 2 项");
    expect([...issues.querySelectorAll("button")].filter((button) => button.textContent === "核对考试信息")).toHaveLength(1);
    await click("核对考试信息"); expect(api.navigate).toHaveBeenCalledWith("/exams");
  });
  it("缺少预览时提供共用重试入口，不把加载失败显示为未保存", async () => {
    await render(); expect(host.querySelector("aside")?.textContent).toContain("重新检查"); expect(host.textContent).not.toContain("未保存");
  });
  it.each([false, true])("只读或冻结状态只提供查看入口（管理权限 %s）", async (canManage) => {
    await render([preview("DUAL_HUMAN", "snapshot")], canManage, canManage ? "ready" : "configured");
    await click("查看评分明细"); expect(host.textContent).toContain("已冻结方案"); expect(host.textContent).toContain("前往题目页查看"); expect(host.textContent).not.toContain("逐题调整");
  });
});
