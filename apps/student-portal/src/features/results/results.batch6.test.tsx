import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import type { StudentQuestion, StudentResult } from "../../api";
import { displaySubject, ExamDiagnosis, formatScore, ScoreHero } from "./ResultOverview";
import { SubjectPerformance } from "./SubjectPerformance";

type BalanceItem = NonNullable<StudentResult["subject_balance"]>[number];
const balances: BalanceItem[] = [
  { subject: "english", student_score_rate: 0.69, school_mean_score_rate: 0.71, sample_size: 80 },
  { subject: "math", student_score_rate: 0.82, school_mean_score_rate: 0.74, sample_size: 80 },
  { subject: "chinese", student_score_rate: 0.76, school_mean_score_rate: 0.72, sample_size: 80 },
];
const result: StudentResult = {
  exam_id: "exam-1", release_id: "release-1", release_version: 1,
  total_score: 82, max_score: 100, overall_total_score: 0, overall_max_score: 0,
  rankings: { class_rank: 0, class_size: 10, grade_rank: 2, grade_size: 30 },
  appeal_window: { open: false },
};

describe("released-result presentation and subject identity", () => {
  it("keeps explicit zero totals and missing ranks separate from subject scores", () => {
    const { container } = render(<ScoreHero result={result} />);
    expect([...container.querySelectorAll(".score-metric strong")].map((element) => element.textContent)).toEqual(["82 / 100", "—", "2 / 30"]);
    expect(container.textContent).toContain("得分率 82%");
    expect(formatScore(0)).toBe("0");
    expect(formatScore(1.26)).toBe("1.3");
    expect(displaySubject("MATH")).toBe("数学");
    expect(displaySubject("unknown")).toBe("unknown");
    expect(displaySubject()).toBe("考试");
  });

  it("preserves missing-score diagnosis ordering and action", () => {
    const questions: StudentQuestion[] = [
      { question_id: "q1", question_no: "1", score: 4, max_score: 5, knowledge_points: ["A", "B"] },
      { question_id: "q2", question_no: "2", score: 0, max_score: 10, knowledge_points: ["A", "C"] },
      { question_id: "q3", question_no: "3", score: 3, max_score: 3 },
    ];
    const onStart = vi.fn();
    const { container } = render(<ExamDiagnosis questions={questions} onStart={onStart} />);
    expect(container.textContent).toContain("2 道失分题 · 1 道部分得分");
    expect([...container.querySelector(".diagnosis-list")!.querySelectorAll("p")].map((node) => node.textContent)).toEqual(["第 2 题 −10 分", "第 1 题 −1 分"]);
    fireEvent.click(screen.getByRole("button", { name: /查看失分题/ }));
    expect(onStart).toHaveBeenCalledTimes(1);
  });

  it("does not render charts without released subject aggregates", () => {
    const { container } = render(<SubjectPerformance items={[]} />);
    expect(container.firstChild).toBeNull();
  });

  it("preserves subject order, radar labels and shared series controls", () => {
    const { container } = render(<SubjectPerformance items={balances} />);
    expect([...container.querySelectorAll("tbody th")].map((node) => node.textContent)).toEqual(["数学", "语文", "英语"]);
    fireEvent.click(screen.getByRole("button", { name: "展开学科雷达图" }));
    const radar = screen.getByRole("img", { name: "我的各科得分率与本次参试学生平均雷达图" });
    expect([...radar.querySelectorAll(".radar-label")].map((node) => node.getAttribute("text-anchor"))).toEqual(["middle", "start", "end"]);
    expect([...radar.querySelectorAll(".radar-dot.student title")].map((node) => node.textContent)).toEqual(["数学 我的得分率 82%", "语文 我的得分率 76%", "英语 我的得分率 69%"]);
    fireEvent.click(screen.getByRole("button", { name: "我的得分率" }));
    expect(container.querySelector(".radar-series.student")).toBeNull();
    expect(container.querySelector(".subject-bar.student")).toBeNull();
    expect(container.querySelector(".radar-series.average")).not.toBeNull();
    expect(container.querySelector(".subject-bar.average")).not.toBeNull();
  });

  it("retains unavailable numeric data while keeping SVG coordinates finite", () => {
    const { container } = render(<SubjectPerformance items={[
      { ...balances[0], student_score_rate: Number.NaN, school_mean_score_rate: Number.POSITIVE_INFINITY },
    ]} />);
    expect(screen.getAllByText("暂无数据")).toHaveLength(2);
    for (const rect of container.querySelectorAll("rect")) {
      expect(Number.isFinite(Number(rect.getAttribute("y")))).toBe(true);
      expect(rect.getAttribute("height")).toBe("0");
    }
  });

  it("preserves each radar subject node when an earlier subject is inserted", () => {
    // 新科目插入会改变坐标索引；同科目的圆点和文字仍应是原来的节点，不串用旧位置。
    const { container, rerender } = render(<SubjectPerformance items={balances} />);
    fireEvent.click(screen.getByRole("button", { name: "展开学科雷达图" }));
    const oldStudent = screen.getByText("数学 我的得分率 82%", { selector: ".radar-dot.student title" }).parentElement;
    const oldAverage = screen.getByText("数学 本次参试学生平均 74%", { selector: ".radar-dot.average title" }).parentElement;
    rerender(<SubjectPerformance items={[{ subject: "biology", student_score_rate: 0.8, school_mean_score_rate: 0.7, sample_size: 80 }, ...balances]} />);
    expect(screen.getByText("数学 我的得分率 82%", { selector: ".radar-dot.student title" }).parentElement).toBe(oldStudent);
    expect(screen.getByText("数学 本次参试学生平均 74%", { selector: ".radar-dot.average title" }).parentElement).toBe(oldAverage);
    expect(container.querySelectorAll(".radar-dot.student")).toHaveLength(4);
  });
});
