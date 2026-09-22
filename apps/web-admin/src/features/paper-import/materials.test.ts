import { describe, expect, it } from "vitest";
import type { PaperImportSource } from "../../api/papers";
import { filesFromClipboard, hasBlockingImportIssues, hasNoExamContentDetected, isPaperImportCancelled, isSupportedPaperImportFile, markImportFieldConfirmed, normalizePastedMarkdown, orderedSourcesAfterMove, orderedSourcesAfterRemoval, paperImportProgress, paperImportReviewIssues, paperImportSummary, pastedMarkdownFile, sourcesAfterRoleChange } from "./materials";

describe("paper import materials", () => {
  it("accepts every supported document and image extension", () => {
    for (const name of ["paper.pdf", "answer.docx", "a.png", "b.jpg", "c.jpeg", "d.tif", "e.tiff", "notes.txt", "questions.md", "rubric.markdown"]) {
      expect(isSupportedPaperImportFile(new File(["x"], name))).toBe(true);
    }
    expect(isSupportedPaperImportFile(new File(["x"], "archive.zip"))).toBe(false);
  });

  it("turns pasted Markdown into an auditable UTF-8 source without changing math", async () => {
    const file = pastedMarkdownFile("\r\n# 试题\r\n\r\n已知 $x^2=4$。\r\n", new Date(2026, 8, 19, 9, 8, 7));
    expect(file.name).toBe("pasted-material-20260919-090807.md");
    expect(file.type).toBe("text/markdown");
    expect(await file.text()).toBe("# 试题\n\n已知 $x^2=4$。");
  });

  it("removes binary clipboard controls without changing supported Markdown math delimiters", async () => {
    const source = "\uFEFF# 数学\r\n\u0000行内 \\(x^2=4\\)\f\u0007块级：\n\\[\\frac{1}{2}\\pi\\]\n$$y=e^x$$";
    const expected = "# 数学\n行内 \\(x^2=4\\)\n\n块级：\n\\[\\frac{1}{2}\\pi\\]\n$$y=e^x$$";
    expect(normalizePastedMarkdown(source)).toBe(expected);
    expect(await pastedMarkdownFile(source).text()).toBe(expected);
  });

  it("reorders sources with contiguous stable indexes", () => {
    const source = (id: string, document_index: number): PaperImportSource => ({ id, document_index, file_asset_id: id, role_hint: "auto", detected_role: "unknown", role_confidence: 0, processing_status: "processed" });
    expect(orderedSourcesAfterMove([source("a", 0), source("b", 1), source("c", 2)], "b", -1).map((item) => [item.id, item.document_index])).toEqual([["b", 0], ["a", 1], ["c", 2]]);
    expect(orderedSourcesAfterRemoval([source("a", 0), source("b", 1), source("c", 2)], "b").map((item) => [item.id, item.document_index])).toEqual([["a", 0], ["c", 1]]);
    expect(sourcesAfterRoleChange([source("a", 0)], "a", "solution")[0].role_hint).toBe("solution");
  });

  it("reads image items and gives unnamed screenshots an auditable filename", () => {
    const image = new File(["x"], "image.png", { type: "image/png" });
    const files = filesFromClipboard({ files: [] as unknown as FileList, items: [{ kind: "file", getAsFile: () => image }] as unknown as DataTransferItemList }, new Date(2026, 7, 30, 13, 55, 0));
    expect(files).toHaveLength(1);
    expect(files[0].name).toBe("clipboard-20260830-135500-01.png");
  });

  it("accepts multiple clipboard images in their original order", () => {
    const first = new File(["a"], "image.png", { type: "image/png" });
    const second = new File(["b"], "image.png", { type: "image/png" });
    const files = filesFromClipboard({ files: [first, second] as unknown as FileList, items: [] as unknown as DataTransferItemList }, new Date(2026, 7, 30, 13, 55, 0));
    expect(files.map((file) => file.name)).toEqual(["clipboard-20260830-135500-01.png", "clipboard-20260830-135500-02.png"]);
  });

  it("does not duplicate a clipboard file exposed through both files and items", () => {
    const image = new File(["a"], "capture.png", { type: "image/png" });
    const files = filesFromClipboard({ files: [image] as unknown as FileList, items: [{ kind: "file", getAsFile: () => new File(["a"], "capture.png", { type: "image/png" }) }] as unknown as DataTransferItemList });
    expect(files).toHaveLength(1);
  });

  it("tracks only fields changed by a human", () => {
    const draft = { question_no: "1", question_type: "essay", score: 10, stem: "题干", knowledge_points: [], confidence: 1, issues: [], source_refs: [] };
    const changed = markImportFieldConfirmed(draft, "score", { score: 12 });
    expect(changed.score).toBe(12);
    expect(changed.human_confirmed_fields).toEqual(["score"]);
  });

  it("summarizes partial imports and blocks apply without questions", () => {
    const base = { id: "i", generation: 1, run_id: "r1", source_revision: "s1", exam_id: "e", exam_paper_id: "", paper_file_asset_id: "", answer_file_asset_id: "", status: "review_required" as const, subject: "math", sources: [], question_candidates: [], answer_candidates: [{ candidate_id: "a1", equivalent_answers: [], confidence: 1, source_refs: [], issues: [] }], solution_candidates: [], rubric_candidates: [], structured_issues: [], questions: [], issues: [], created_at: "2026-08-30T00:00:00Z" };
    expect(paperImportSummary(base).answers).toBe(1);
    expect(hasBlockingImportIssues(base)).toBe(true);
  });

  it("condenses repetitive review warnings into distinct actions", () => {
    const issue = (code: string, message: string, question_no?: string) => ({ code, message, question_no, severity: "error" as const, certainty: "confirmed" as const, source_refs: [] });
    const structured_issues = [
      issue("HUMAN_REVIEW_REQUIRED", "第1题尚未完成人工核对", "1"),
      issue("HUMAN_REVIEW_REQUIRED", "第2题尚未完成人工核对", "2"),
      issue("MISSING_SCORE", "第1题缺少有效分值", "1"),
      issue("MISSING_SCORE", "第2题缺少有效分值", "2"),
      issue("SECTION_COUNT_MISMATCH", "单项选择预计8道，当前识别7道"),
      issue("QUESTION_COUNT_MISMATCH", "考试配置共21道，当前识别7道"),
      { ...issue("solution_truncated", "第7题解析可能不完整", "7"), severity: "warning" as const, certainty: "suspected" as const }
    ];

    const visible = paperImportReviewIssues({ structured_issues });
    expect(visible.map((item) => item.message)).toEqual([
      "第7题解析可能不完整",
      "2 道题尚未填写分值",
      "考试配置共21道，当前识别7道"
    ]);
  });

  it("reports only factual worker counters and never invents a stage percentage", () => {
    const source = (processing_status: PaperImportSource["processing_status"]): PaperImportSource => ({ id: "s", document_index: 0, file_asset_id: "f", role_hint: "auto", detected_role: "unknown", role_confidence: 0, processing_status });
    const base = { id: "i", generation: 1, run_id: "r1", source_revision: "s1", exam_id: "e", exam_paper_id: "", paper_file_asset_id: "", answer_file_asset_id: "", status: "processing" as const, subject: "math", question_candidates: [], answer_candidates: [], solution_candidates: [], rubric_candidates: [], structured_issues: [], questions: [], issues: [], created_at: "2026-08-30T00:00:00Z" };

    expect(paperImportProgress({ ...base, sources: [source("pending")] })).toMatchObject({ percent: undefined, label: "等待 Worker" });
    expect(paperImportProgress({ ...base, sources: [source("processing")], runtime_progress: { task_type: "ocr", task_status: "running", stage: "text_ocr", completed: 2, total: 5, unit: "page", message: "已识别 2/5 页", updated_at: "2026-08-30T00:00:02Z" } })).toMatchObject({ percent: 40, label: "文字识别", detail: expect.stringContaining("2/5") });
    expect(paperImportProgress({ ...base, sources: [source("processed")], runtime_progress: { task_type: "paper_parse", task_status: "running", updated_at: "2026-08-30T00:00:02Z" } })).toMatchObject({ percent: undefined, label: "题目结构解析" });
		expect(paperImportProgress({ ...base, sources: [source("processed")], runtime_progress: { task_type: "paper_parse", task_status: "running", stage: "paper_parse", phase: "model_request", parse_route: "compact_model", completed: 2, total: 5, unit: "parse_chunk", message: "大模型已完成 2/5 个实际解析块", updated_at: "2026-08-30T00:00:02Z" } })).toMatchObject({ percent: 40, counter: "2/5 个解析块", detail: expect.stringContaining("仅歧义解析块调用大模型") });
		expect(paperImportProgress({ ...base, sources: [source("processed")], runtime_progress: { task_type: "paper_parse", task_status: "running", stage: "paper_parse", phase: "deterministic_structuring", parse_route: "anchored", completed: 1, total: 1, unit: "parse_chunk", message: "确定性结构重建完成，未调用大模型", updated_at: "2026-08-30T00:00:02Z" } })).toMatchObject({ percent: 100, detail: expect.stringContaining("本阶段未调用大模型") });
		expect(paperImportProgress({ ...base, subject: "mathematics", authoritative_subject_code: "mathematics", formula_status: "running", sources: [source("processed")], runtime_progress: { task_type: "paper_formula", task_status: "running", stage: "formula_recognition", completed: 3, total: 12, unit: "formula_region", model: "PP-FormulaNet_plus-M", updated_at: "2026-08-30T00:00:02Z" } })).toMatchObject({ percent: 25, label: "数学公式识别", detail: expect.stringContaining("PP-FormulaNet_plus-M") });
		expect(paperImportProgress({ ...base, sources: [source("processed")], runtime_progress: { task_type: "paper_formula", task_status: "running", stage: "formula_recognition", phase: "model_loading", completed: 0, total: 12, unit: "formula_region", cold_start: true, progress_changed_at: "2026-08-30T00:00:01Z", updated_at: "2026-08-30T00:00:02Z" } })).toMatchObject({ percent: undefined, counter: "0/12 个 ROI", changedAt: "2026-08-30T00:00:01Z", detail: expect.stringContaining("不会重新下载模型") });
  });

  it("reports unrelated uploads as a completed but blocked recognition result", () => {
    const job = {
      id: "i", generation: 1, run_id: "r1", source_revision: "s1", exam_id: "e", exam_paper_id: "", paper_file_asset_id: "", answer_file_asset_id: "",
      status: "review_required" as const, subject: "math", sources: [], question_candidates: [],
      answer_candidates: [], solution_candidates: [], rubric_candidates: [], questions: [], issues: [],
      structured_issues: [{ code: "NO_EXAM_CONTENT_DETECTED", severity: "error" as const, certainty: "confirmed" as const, message: "未识别到考试内容", resolution_hint: "重新上传", source_refs: [] }],
      created_at: "2026-08-30T00:00:00Z"
    };

    expect(hasNoExamContentDetected(job)).toBe(true);
    expect(paperImportProgress(job)).toMatchObject({ percent: 100, label: "未识别到考试内容" });
    expect(hasBlockingImportIssues(job)).toBe(true);
  });

  it("distinguishes a manually stopped import from a recognition failure", () => {
    const job = {
      id: "i", generation: 1, run_id: "r1", source_revision: "s1", exam_id: "e", exam_paper_id: "", paper_file_asset_id: "", answer_file_asset_id: "",
      status: "cancelled" as const, subject: "math", sources: [], question_candidates: [], answer_candidates: [],
      solution_candidates: [], rubric_candidates: [], structured_issues: [], questions: [], issues: ["识别任务已手动停止"],
      error_code: "paper_import_cancelled", created_at: "2026-08-30T00:00:00Z"
    };

    expect(isPaperImportCancelled(job)).toBe(true);
    expect(paperImportProgress(job)).toMatchObject({ percent: undefined, label: "已停止识别" });
  });
});
