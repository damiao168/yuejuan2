// @vitest-environment jsdom
import { render, screen, cleanup } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { ImageGradingRecord } from "./ImageGradingRecord";

vi.mock("./GradingMathMarkdown", () => ({ GradingMathMarkdown: ({ children, className }: { children: string; className?: string }) => <div className={className} data-grading-math="true">{children}</div> }));
vi.mock("./GradingAIPlanSettings", () => ({ gradingAIModeLabels: { single: "单模型评分 · 非三智能体", dual_conditional_arbiter: "双模型评分 · 超差启用第三模型" } }));
afterEach(cleanup);
const grade = {
  question_id: "q", suggested_score: 9, max_score: 10,
  raw_output: {
    schema_version: "grading-image-v3", model_version: "model-a",
    scoring_basis: { standard_answer: "[2,+∞)", rubric_points: [{ id: "p", description: "正确写出闭区间", score: 2 }] },
    observation: { members: [{ question_id: "q", presence: "present", answer: { value: "[2,+∞)" }, evidence: [{ evidence_id: "e", text: "左端为方括号" }] }] },
    judgments: [{ criterion_id: "p", status: "supported", reason: "端点2包含在区间中", evidence_ids: ["e"] }],
    reasoning_content: "PRIVATE_PROVIDER_CONTENT"
  }
};

describe("教师查看图像评分依据", () => {
  it("保留单模型实情，展示冻结标准与采分理由，不读取供应商私有思维链", () => {
    render(<ImageGradingRecord grade={grade} />);
    expect(screen.getByText("单模型评分 · 非三智能体")).toBeTruthy();
    expect(screen.getByText("正确写出闭区间")).toBeTruthy();
    expect(screen.getByText("端点2包含在区间中")).toBeTruthy();
    expect(screen.getByText("左端为方括号")).toBeTruthy();
    expect(screen.queryByText("PRIVATE_PROVIDER_CONTENT")).toBeNull();
  });
  it("识别作答、评分点、理由、证据和冻结参考资料都交给公式展示，原文保持不变", () => {
    const answer = String.raw`P=p_1p_2...p_n`;
    const point = String.raw`命中 x_1+x_2=3`;
    const reason = String.raw`因此 \frac13 是正确结果`;
    const snippet = String.raw`写出 a_n=2^n`;
    const standard = String.raw`S_n=\frac{n(n+1)}2`;
    const solution = String.raw`由 x^2+y^2=1 得解`;
    render(<ImageGradingRecord grade={{ ...grade, raw_output: {
      ...grade.raw_output,
      scoring_basis: { standard_answer: standard, solution_text: solution, rubric_points: [{ id: "p", description: point, score: 2 }] },
      observation: { members: [{ question_id: "q", presence: "present", answer: { value: answer }, evidence: [{ evidence_id: "e", text: snippet }] }] },
      judgments: [{ criterion_id: "p", status: "supported", reason, evidence_ids: ["e"] }]
    } }} />);
    for (const content of [answer, point, reason, snippet, standard, solution]) {
      expect(screen.getByText(content).getAttribute("data-grading-math")).toBe("true");
    }
    expect(screen.getByText(point).className).toBe("grading-record-point-title");
  });
  it("角色部分失败时不把父候选分显示为汇总建议分", () => {
    render(<ImageGradingRecord grade={{ ...grade, raw_output: { ...grade.raw_output, panel_execution: {
      configured_mode: "dual_conditional_arbiter", decision: "teacher_review", reason_codes: ["role_failed"],
      role_results: [{ role: "primary_a", status: "succeeded", score: 9, model: { model_name: "model-a" }, raw_output: grade.raw_output }, { role: "primary_b", status: "failed", model: { model_name: "model-b" } }]
    } } }} />);
    expect(screen.getByText("未汇总，需教师处理")).toBeTruthy();
    expect(screen.getByText("本次未形成汇总分，请教师评分")).toBeTruthy();
    expect(screen.queryByText("建议分 9 / 10")).toBeNull();
  });
  it("最终零分是有效汇总，不能误判为未汇总", () => {
    render(<ImageGradingRecord grade={{ ...grade, raw_output: { ...grade.raw_output, panel_execution: { configured_mode: "dual", decision: "primary_consensus", final_score: 0, primary_threshold: 1, role_results: [] } } }} />);
    expect(screen.getByText("建议分 0 / 10")).toBeTruthy();
    expect(screen.getByText("前两模型分差未超过允许的 1 分，未调用第三模型。取 A、B 均分后向上取整。")).toBeTruthy();
    expect(screen.queryByText("未汇总，需教师处理")).toBeNull();
  });
});
