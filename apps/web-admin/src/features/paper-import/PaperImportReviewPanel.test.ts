import { describe, expect, it } from "vitest";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { PAPER_REVIEW_PAGE_SIZE, PaperImportReviewPanel, displayPaperImportValue, rubricFromCandidate } from "./PaperImportReviewPanel";

describe("paper import review value display", () => {
  it("preserves scalar answers and serializes structured answers safely", () => {
    expect(displayPaperImportValue("A")).toBe("A");
    expect(displayPaperImportValue(42)).toBe("42");
    expect(displayPaperImportValue(false)).toBe("false");
    expect(displayPaperImportValue(["A", "C"])).toBe('[\n  "A",\n  "C"\n]');
    expect(displayPaperImportValue({ value: 3 })).toBe('{\n  "value": 3\n}');
  });

  it("keeps candidate identity outside the rubric and preserves all extracted rubric data", () => {
    const rubric = rubricFromCandidate({
      candidate_id: "rubric-1",
      points: [{ id: "p1", description: "步骤正确", score: null }, { id: "p2", description: "结论正确", score: 2, required: false }],
      deductions: [{ description: "单位错误", score: -1 }],
      examples: ["示例"],
      confidence: 0.8,
      source_refs: [],
      issues: []
    }, 6);
    expect(rubric).toEqual({
      status: "draft",
      max_score: 6,
      points: [
        { id: "p1", description: "步骤正确", score: 0, required: true, evidence_requirements: undefined },
        { id: "p2", description: "结论正确", score: 2, required: false, evidence_requirements: undefined }
      ],
      deductions: [{ description: "单位错误", score: -1 }],
      examples: ["示例"]
    });
  });

  it("shows readable math while keeping duplicate raw text hidden by default", () => {
    const draft = {
      candidate_id: "q1", source_refs: [], question_no: "1", question_type: "single_choice",
      score: 0, stem: "计算 $x^2$", knowledge_points: [], confidence: 0.98, issues: [],
      answer_key: { standard_answer: "B", equivalent_answers: [], tolerance: {} },
      solution: { raw_text: "因为 $x^2=4$", steps: [], source_refs: [] },
      rubric: { status: "draft", max_score: 0, points: [], deductions: [], examples: [] },
    };
    const job = {
      question_candidates: [{ candidate_id: "q1", options: ["A. 3", "B. 4"], knowledge_point_hints: [], confidence: .98, source_refs: [], issues: [] }],
      answer_candidates: [], solution_candidates: [], rubric_candidates: [],
    };

    const html = renderToStaticMarkup(createElement(PaperImportReviewPanel, {
      job: job as never, drafts: [draft] as never, onChange: () => undefined, onOpenSource: () => undefined,
    }));

    expect(html).toContain("aria-label=\"编辑题目\"");
    expect(html).toContain("paper-import-question-edit");
    expect(html).toContain("lucide-pencil");
    expect(html).not.toContain("显示原始文本");
    expect(html).toContain("katex");
    expect(html).not.toContain("题干原文（Markdown / LaTeX）");
    expect(html).not.toContain("解析原文（Markdown / LaTeX）");
    expect(html).not.toContain("查看题干来源");
    expect(html).not.toContain("查看答案来源");
    expect(html).not.toContain("查看解析来源");
    expect(html).toContain("一、单选题");
    expect(html).toContain("每题 0 分，共 1 题，合计 0 分");
    expect(html).toContain("paper-import-option is-correct");
    expect(html).not.toContain("第 1 题");
    expect(html).not.toContain("提取证据参考");
    expect(html).not.toContain("题干与选项");
    expect(html).not.toContain("标准答案");
    expect(html).not.toContain("教师解析");
    expect(html).not.toContain("评分细则");
  });

  it("explains which recognized questions update existing content and which are new", () => {
    const draft = (candidate_id: string, question_no: string, match_status: string) => ({
      candidate_id, source_refs: [], question_no, question_type: "short_answer",
      score: 5, stem: `题目 ${question_no}`, knowledge_points: [], confidence: 0.9, issues: [], match_status
    });
    const job = {
      question_candidates: ["q1", "q2", "q3"].map((candidate_id) => ({ candidate_id, options: [], knowledge_point_hints: [], confidence: .9, source_refs: [], issues: [] })),
      answer_candidates: [], solution_candidates: [], rubric_candidates: []
    };

    const html = renderToStaticMarkup(createElement(PaperImportReviewPanel, {
      job: job as never,
      drafts: [draft("q1", "1", "matched"), draft("q2", "2", "create"), draft("q3", "2", "ambiguous")] as never,
      onChange: () => undefined,
      onOpenSource: () => undefined
    }));

    expect(html).toContain("1 道将更新已有题目");
    expect(html).toContain("1 道将新增");
    expect(html).toContain("1 道需要确认");
    expect(html).toContain("更新已有题目");
    expect(html).toContain("重复题号待确认");
    expect(html).toContain("不会因为重复粘贴而静默新增同一道题");
  });

  it("uses the fixed answer for fill-in-the-blank questions without showing a second rubric editor", () => {
    const draft = {
      candidate_id: "fill-1", source_refs: [], question_no: "8", question_type: "fill_blank",
      score: 2, stem: "函数的零点是 ____。", knowledge_points: [], confidence: 0.96, issues: [],
      answer_key: { standard_answer: "1", equivalent_answers: ["x=1"], tolerance: {} },
      rubric: { status: "draft", max_score: 1, points: [{ id: "legacy", description: "旧评分点", score: 1, required: true }], deductions: [], examples: [] }
    };
    const job = {
      question_candidates: [{ candidate_id: "fill-1", options: [], knowledge_point_hints: [], confidence: .96, source_refs: [], issues: [] }],
      answer_candidates: [], solution_candidates: [], rubric_candidates: []
    };

    const html = renderToStaticMarkup(createElement(PaperImportReviewPanel, {
      job: job as never, drafts: [draft] as never, onChange: () => undefined, onOpenSource: () => undefined
    }));

    expect(html).toContain("1");
    expect(html).not.toContain("评分细则");
    expect(html).not.toContain("旧评分点");
    expect(html).not.toContain("添加采分点");
  });

  it("mounts only one review page for large papers", () => {
    const drafts = Array.from({ length: PAPER_REVIEW_PAGE_SIZE + 2 }, (_, index) => ({
      candidate_id: `q${index + 1}`, source_refs: [], question_no: String(index + 1), question_type: "short_answer",
      score: 5, stem: `唯一题干-${index + 1}`, knowledge_points: [], confidence: 0.9, issues: [],
      solution: { raw_text: `唯一解析-${index + 1} $x_${index + 1}$`, steps: [], source_refs: [] },
      rubric: { status: "draft", max_score: 5, points: [], deductions: [], examples: [] }
    }));
    const job = {
      question_candidates: drafts.map((draft) => ({ candidate_id: draft.candidate_id, options: [], knowledge_point_hints: [], confidence: .9, source_refs: [], issues: [] })),
      answer_candidates: [], solution_candidates: [], rubric_candidates: []
    };

    const html = renderToStaticMarkup(createElement(PaperImportReviewPanel, {
      job: job as never, drafts: drafts as never, onChange: () => undefined, onOpenSource: () => undefined
    }));

    expect(html).toContain(`1–${PAPER_REVIEW_PAGE_SIZE}`);
    expect(html).toContain(`/ ${PAPER_REVIEW_PAGE_SIZE + 2} 道题`);
    expect(html).toContain(`唯一题干-${PAPER_REVIEW_PAGE_SIZE}`);
    expect(html).not.toContain(`唯一题干-${PAPER_REVIEW_PAGE_SIZE + 1}`);
    expect(html).not.toContain(`唯一解析-${PAPER_REVIEW_PAGE_SIZE + 2}`);
  });
});
