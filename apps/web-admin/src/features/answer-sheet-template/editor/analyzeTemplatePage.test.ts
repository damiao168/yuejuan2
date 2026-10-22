// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { createHash } from "node:crypto";
import type { TemplateAnalysisCandidate, TemplatePageAnalysis } from "../../../api/answerSheetTemplateAnalysis";
import { ApiClientError } from "../../../api/client";
import { answerQuestion } from "../testing/answerSheetFixtures";
import measuredPageReplay from "../testing/measuredPageReplay.json";
import { createTemplatePageAnalyzer } from "./analyzeTemplatePage";
import { commitPageAnalysis, unresolvedTemplateMessage } from "./regionLifecycle";

const mocks = vi.hoisted(() => ({ analyze: vi.fn() }));
vi.mock("../../../api/answerSheetTemplateAnalysis", () => ({ analyzeAnswerSheetTemplatePage: mocks.analyze }));
const bbox = { x: .1, y: .2, width: .7, height: .3 };
const detectedFrames = [{ ...bbox, confidence: .95 }];
function response(): TemplatePageAnalysis { return { template_id: "template", template_revision: 4, source_sha256: "original-source",
  analysis: { request_id: "analysis", schema_version: "answer-sheet-analysis-v1", status: "review_required", semantic_status: "completed",
    coordinate_space: "source_display_normalized", page_no: 1, source_sha256: createHash("sha256").update("uploaded-page").digest("hex"), candidates: [
    { page_no: 1, candidate_id: "frame", printed_question_label: "1", role: "answer", proposed_bbox: bbox,
      source_bbox: { ...bbox, x: .2 }, association_status: "associated", semantic_check: "supported", evidence_refs: [], reason_codes: [] }
  ] } }; }
const run = (questions = [{ ...answerQuestion, question_type: "calculation" }], pageNo = 1) => createTemplatePageAnalyzer({ templateId: "template", revision: 4, sourceHash: "original-source", questions })({
  canvas: { width: 3000, height: 4000 } as HTMLCanvasElement, pageNo, frames: detectedFrames, signal: new AbortController().signal
});

describe("template page analysis adapter", () => {
  beforeEach(() => {
    vi.resetAllMocks();
    mocks.analyze.mockResolvedValue(response());
    vi.spyOn(HTMLCanvasElement.prototype, "getContext").mockReturnValue({ fillRect: vi.fn(), drawImage: vi.fn() } as unknown as CanvasRenderingContext2D);
    vi.spyOn(HTMLCanvasElement.prototype, "toDataURL").mockReturnValue(`data:image/jpeg;base64,${btoa("uploaded-page")}`);
    Object.defineProperty(crypto, "subtle", { configurable: true, value: { digest: async (_algorithm: string, bytes: Uint8Array) =>
      new Uint8Array(createHash("sha256").update(bytes).digest()).buffer } });
  });
  afterEach(() => vi.restoreAllMocks());

  it("sends bounded JPEG pixels and maps final proposed geometry to the current exam question", async () => {
    const result = await run();
    expect(mocks.analyze.mock.calls[0][1]).toMatchObject({ expected_revision: 4, expected_source_sha256: "original-source",
      width: 1200, height: 1600, media_type: "image/jpeg", sha256: createHash("sha256").update("uploaded-page").digest("hex") });
    expect(result.regions).toHaveLength(1);
    expect(result.regions[0]).toMatchObject({ question_id: answerQuestion.id, ...bbox });
    expect(result.unassigned).toEqual([]);
    expect(HTMLCanvasElement.prototype.toDataURL).toHaveBeenCalledWith("image/jpeg", .92);
  });

  it("preserves the page request budget by reducing JPEG quality only when necessary", async () => {
    vi.mocked(HTMLCanvasElement.prototype.toDataURL).mockImplementation((_format, quality) =>
      `data:image/jpeg;base64,${quality === .92 ? "A".repeat(1_400_000) : btoa("uploaded-page")}`);
    await run();
    expect(vi.mocked(HTMLCanvasElement.prototype.toDataURL).mock.calls.map(call => call[1])).toEqual([.92, .85]);
    expect(mocks.analyze).toHaveBeenCalledOnce();
  });

  it("uses independently measured registration marks without adding a confirmation step", async () => {
    const value = response();
    value.analysis.registration = { version: "corner-registration-v1", status: "detected", coordinate_space: "source_normalized",
      registration_marks: [{ id: "pixel-mark", x: .02, y: .03, width: .01, height: .01 }] };
    mocks.analyze.mockResolvedValue(value);
    const result = await run();
    expect(result.registrationMarks).toMatchObject([{ x: .02, y: .03, width: .01, height: .01,
      origin: "local_detector", review_state: "suggested", semantic_check: "supported", analysis_run_id: "analysis" }]);
    expect(result.unassigned).toEqual([]);
  });

  it("distinguishes a completed registration search with no marks from an older service without that result", async () => {
    expect((await run()).registrationMarks).toBeUndefined();
    const value = response();
    value.analysis.registration = { version: "corner-registration-v1", status: "not_found", coordinate_space: "source_normalized", registration_marks: [] };
    mocks.analyze.mockResolvedValue(value);
    expect((await run()).registrationMarks).toEqual([]);
  });

  it("reports the actual semantic model only after completed analysis", async () => {
    const value = response(); value.analysis.model_identity = { model_name: "school-vision-model" };
    mocks.analyze.mockResolvedValue(value);
    expect((await run()).modelName).toBe("school-vision-model");
    value.analysis.semantic_status = "failed";
    expect((await run()).modelName).toBeUndefined();
  });

  it.each(["answer", "identity", "objective_group"] as const)(
    "does not directly apply a supported %s candidate with explicit forbidden evidence", async role => {
      const value = response();
      Object.assign(value.analysis.candidates[0], { role, identity_label: role === "identity" ? "条码" : null,
        reason_codes: ["CONTENT_FORBIDDEN_WRITING"] });
      mocks.analyze.mockResolvedValue(value);
      const result = await run();
      expect(result.regions).toEqual([]);
      expect(result.identityRegions).toEqual([]);
      expect(result.objectiveGroups).toEqual([]);
      expect(result.unassigned).toMatchObject([{ semantic_check: "needs_review", review_state: "suggested",
        reason_codes: ["CONTENT_FORBIDDEN_WRITING", "SEMANTIC_FORBIDDEN_AREA_DETECTED"] }]);
      expect(result.unassigned[0].question_id).toBeUndefined();
      expect(result.notice).toContain("禁止作答");
    });

  it.each(["MODEL_OBS_WARNING_CATEGORY_CONFLICT", "REGION_CONFLICT", "MODEL_OBS_MULTIPLE_QUESTION_LABELS"])(
    "preserves an explicit supported-model contradiction %s for correction", async code => {
      const value = response(); value.analysis.candidates[0].reason_codes = [code];
      mocks.analyze.mockResolvedValue(value);
      const result = await run();
      expect(result.regions).toEqual([]);
      expect(result.unassigned).toMatchObject([{ semantic_check: "needs_review" }]);
      expect(result.processingSucceeded).toBe(true);
    });

  it("automatically applies a supported model result when OCR diagnostics contain no contradictory evidence", async () => {
    const value = response(); value.analysis.candidates[0].reason_codes = ["SEMANTIC_OCR_UNAVAILABLE", "SEMANTIC_CHECK_BUDGET_EXCEEDED"];
    mocks.analyze.mockResolvedValue(value);
    const result = await run();
    expect(result.regions).toMatchObject([{ question_id: answerQuestion.id, semantic_check: "supported" }]);
    expect(result.unassigned).toEqual([]);
    expect(result.notice).toBeUndefined();
  });

  it.each([undefined, "not_evaluated", "needs_review"] as const)("keeps a completed but unverified %s classification pending", async semanticCheck => {
    const value = response();
    Object.assign(value.analysis.candidates[0], { role: "identity", identity_label: "姓名", semantic_check: semanticCheck,
      reason_codes: ["SEMANTIC_CONTENT_CONFLICT"] });
    mocks.analyze.mockResolvedValue(value);
    const result = await run();
    expect(result.identityRegions).toEqual([]);
    expect(result.regions).toEqual([]);
    expect(result.unassigned).toMatchObject([{ label: "待核对区域", semantic_check: "needs_review", review_state: "suggested" }]);
    expect(result.notice).toContain("图像内容与区域类别不一致");
  });

  it.each(["answer", "identity", "objective_group"] as const)("retains %s candidate role and printed label for later manual-area decisions", async role => {
    const value = response();
    Object.assign(value.analysis.candidates[0], { role, printed_question_label: "14", semantic_check: "needs_review" });
    mocks.analyze.mockResolvedValue(value);
    const result = await run();
    expect(result.unassignedCandidates).toEqual([{ id: result.unassigned[0].id, page_no: 1, role,
      question_no: "14", region: result.unassigned[0] }]);
    expect(result.unassigned[0].question_id).toBeUndefined();
  });

  it("returns one complete objective block instead of treating multi-question rows as individual questions", async () => {
    const value = response(), row = value.analysis.candidates[0];
    value.analysis.candidates = [
      { ...row, candidate_id: "objective-block", role: "objective_group", printed_question_label: null, association_status: "unassociated" },
      { ...row, candidate_id: "choice-row", proposed_bbox: { x: .1, y: .22, width: .7, height: .05 } },
      { ...row, candidate_id: "unknown-inner-row", role: "uncertain", printed_question_label: null, association_status: "unassociated",
        proposed_bbox: { x: .1, y: .3, width: .7, height: .05 } }
    ];
    mocks.analyze.mockResolvedValue(value);
    const result = await run([answerQuestion]);
    expect(result.regions).toEqual([]);
    expect(result.unassigned).toEqual([]);
    expect(result.objectiveGroups).toMatchObject([{ ...bbox, label: "选择题区域" }]);
    expect(result.objectiveGroups?.[0].question_id).toBeUndefined();
    expect(result.notice).toBeUndefined();
  });

  it("automatically applies model-identified objective items without a second grid confirmation", async () => {
    const value = response();
    Object.assign(value.analysis.candidates[0], { role: "objective_group", printed_question_label: null,
      association_status: "unassociated", objective_items: [{ printed_question_label: "1", bbox: { x: .12, y: .22, width: .3, height: .1 },
        options: ["A", "B", "C", "D"].map((label, index) => ({ label,
          bbox: { x: .14 + index * .05, y: .24, width: .03, height: .02 } })) }] });
    mocks.analyze.mockResolvedValue(value);
    const result = await run([{ ...answerQuestion, options: ["a", "b", "c", "d"], question_type: "single_choice" }]);
    expect(result.regions).toMatchObject([{ question_id: answerQuestion.id, origin: "model", review_state: "suggested",
      semantic_check: "supported", option_coordinate_space: "question_relative", option_regions: [{ label: "A" }, { label: "B" }, { label: "C" }, { label: "D" }] }]);
    expect(result.unassigned).toEqual([]);
    expect(result.objectiveGroups).toEqual([]);
    expect(result.notice).toBeUndefined();
  });

  it("consumes real measured choices directly for OMR and clears old instruction, split-choice and fill candidates", async () => {
    // 几何与原因码来自真实服务响应；只替换运行标识、证据引用，不用规则均分框代替真实选项坐标。
    const value = response();
    value.analysis.candidates = structuredClone(measuredPageReplay) as TemplateAnalysisCandidate[];
    mocks.analyze.mockResolvedValue(value);
    const official = Array.from({ length: 16 }, (_, index) => ({ ...answerQuestion, id: `q${index + 1}`, question_no: `${index + 1}`,
      options: index < 11 ? ["A", "B", "C", "D"] : [],
      question_type: index < 8 ? "single_choice" : index < 11 ? "multiple_choice" : index < 14 ? "fill_blank" : "calculation" }));
    const result = await run(official);
    expect(result.regions.map(region => region.question_id).sort()).toEqual(official.map(question => question.id).sort());
    expect(result.identityRegions?.map(region => region.label).sort()).toEqual(["姓名", "条码", "准考证号"].sort());
    expect(result.objectiveGroups).toEqual([]);
    expect(result.unassigned).toEqual([]);
    const group = value.analysis.candidates.find(candidate => candidate.role === "objective_group")!;
    for (const item of group.objective_items!) {
      const roi = result.regions.find(region => region.label === item.printed_question_label)!;
      expect(roi.option_coordinate_space).toBe("question_relative");
      for (const option of item.options) {
        const relative = roi.option_regions!.find(region => region.label === option.label)!;
        expect(roi.x + relative.x * roi.width).toBeCloseTo(option.bbox.x, 8);
        expect(roi.y + relative.y * roi.height).toBeCloseTo(option.bbox.y, 8);
        expect(relative.width * roi.width).toBeCloseTo(option.bbox.width, 8);
        expect(relative.height * roi.height).toBeCloseTo(option.bbox.height, 8);
      }
    }
    const next = commitPageAnalysis({ pages: [{ page_no: 1, width: 1600, height: 1132, registration_marks: [],
      question_regions: [], identity_regions: [] }], editor_state: { version: 1, source_sha256: "original-source",
      candidates: ["old-instruction", "old-single-choice", "old-multiple-choice", "old-fill-parent"].map(id => ({
        id, page_no: 1, role: "uncertain", region: { id, ...bbox, origin: "model", review_state: "suggested", semantic_check: "needs_review" }
      })) } }, { sourceHash: "original-source", successfulPages: [1], pages: new Map([[1, result.regions]]),
      identities: new Map([[1, result.identityRegions ?? []]]), candidates: [] });
    expect(next.editor_state?.candidates).toEqual([]);
    expect(unresolvedTemplateMessage(next)).toBe("");
    expect(next.pages[0].question_regions.filter(region => region.option_regions?.length)).toHaveLength(11);
  });

  it("attaches a verified shared fill section while preserving each measured answer ROI", async () => {
    const value = response();
    value.analysis.candidates = structuredClone(measuredPageReplay) as TemplateAnalysisCandidate[];
    value.analysis.fill_grading_groups = [{ group_id: "measured-12-14", question_labels: ["12", "13", "14"],
      bbox: { x: .066, y: .505, width: .282, height: .113 }, reason_codes: ["LOCAL_FILL_SECTION_VERIFIED"] }];
    mocks.analyze.mockResolvedValue(value);
    const official = Array.from({ length: 16 }, (_, index) => ({ ...answerQuestion, id: `q${index + 1}`, question_no: `${index + 1}`,
      options: index < 11 ? ["A", "B", "C", "D"] : [],
      question_type: index < 8 ? "single_choice" : index < 11 ? "multiple_choice" : index < 14 ? "fill_blank" : "calculation" }));
    const result = await run(official);
    const fill = result.regions.filter(region => ["q12", "q13", "q14"].includes(region.question_id ?? ""));
    expect(fill).toHaveLength(3);
    for (const region of fill) {
      expect(region.grading_group_id).toBe("measured-12-14");
      expect(region).toMatchObject(value.analysis.candidates.find(candidate => candidate.printed_question_label === region.label)!.proposed_bbox!);
      expect(region.semantic_check).toBe("supported");
    }
    expect(result.unassigned).toEqual([]);
  });

  it("automatically corresponds all 19 formal questions across two pages and excludes the forbidden sign", async () => {
    const official = Array.from({ length: 19 }, (_, index) => ({ ...answerQuestion, id: `q${index + 1}`, question_no: `${index + 1}`,
      options: index < 11 ? ["a", "b", "c", "d"] : [],
      question_type: index < 8 ? "single_choice" : index < 11 ? "multiple_choice" : index < 14 ? "fill_blank" : "calculation" }));
    const front = response(), back = response();
    const seed = front.analysis.candidates[0];
    const objective = { ...seed, candidate_id: "choice-block", role: "objective_group" as const, printed_question_label: null,
      association_status: "unassociated" as const, proposed_bbox: { x: .05, y: .08, width: .9, height: .35 },
      objective_items: official.slice(0, 11).map((question, index) => ({ printed_question_label: question.question_no,
        bbox: { x: .1, y: .1 + index * .027, width: .5, height: .022 },
        options: ["A", "B", "C", "D"].map((label, optionIndex) => ({ label,
          bbox: { x: .2 + optionIndex * .06, y: .103 + index * .027, width: .03, height: .015 } })) })) };
    front.analysis.candidates = [objective, ...official.slice(11, 16).map((question, index) => ({ ...seed,
      candidate_id: `answer-${question.question_no}`, printed_question_label: question.question_no,
      proposed_bbox: { x: .1, y: .46 + index * .09, width: .7, height: .08 } })),
      { ...seed, candidate_id: "forbidden-sign", role: "instruction", printed_question_label: null,
        association_status: "unassociated", proposed_bbox: { x: .82, y: .9, width: .05, height: .05 },
        reason_codes: ["LOCAL_FORBIDDEN_LABEL_VERIFIED"] }];
    back.analysis.page_no = 2;
    back.analysis.candidates = official.slice(16).map((question, index) => ({ ...seed, page_no: 2,
      candidate_id: `answer-${question.question_no}`, printed_question_label: question.question_no,
      proposed_bbox: { x: .1, y: .15 + index * .25, width: .7, height: .2 } }));
    mocks.analyze.mockResolvedValueOnce(front).mockResolvedValueOnce(back);
    const firstPage = await run(official), secondPage = await run(official, 2);
    expect([...(firstPage.unassigned), ...secondPage.unassigned, ...(firstPage.objectiveGroups ?? [])]).toEqual([]);
    const final = commitPageAnalysis({ pages: [1, 2].map(page_no => ({ page_no, width: 1200, height: 1600,
      registration_marks: [], question_regions: [], identity_regions: [] })) }, { sourceHash: "original-source", successfulPages: [1, 2],
      pages: new Map([[1, firstPage.regions], [2, secondPage.regions]]), identities: new Map(), candidates: [] });
    expect(final.pages.flatMap(page => page.question_regions.map(region => region.question_id))).toEqual(official.map(question => question.id));
    expect(final.pages[0].question_regions.filter(region => region.option_regions?.length)).toHaveLength(11);
    expect(unresolvedTemplateMessage(final)).toBe("");
    expect(final.editor_state?.candidates).toEqual([]);
    expect(final.pages.every(page => page.question_regions.every(region => region.review_state === "suggested"))).toBe(true);
  });

  it.each(["answer", "uncertain"] as const)("preserves a local fill hint after gateway role %s without assigning a question", async role => {
    const value = response();
    Object.assign(value.analysis.candidates[0], { origin: "local_detector", role, printed_question_label: "12",
      association_status: "unassociated", semantic_check: "needs_review" });
    mocks.analyze.mockResolvedValue(value);
    const result = await run([{ ...answerQuestion, question_no: "12", question_type: "fill_blank" }]);
    expect(result.regions).toEqual([]);
    expect(result.unassigned).toMatchObject([{ origin: "local_detector", review_state: "suggested",
      label: "填空第 12 题 · 待核对", suggestion_source: "ocr_layout", semantic_check: "needs_review" }]);
    expect(result.unassigned[0].question_id).toBeUndefined();
  });

  it("does not invent a fill hint for a label outside the official fill questions", async () => {
    const value = response();
    Object.assign(value.analysis.candidates[0], { origin: "local_detector", printed_question_label: "12",
      association_status: "unassociated", semantic_check: "needs_review" });
    mocks.analyze.mockResolvedValue(value);
    expect((await run()).unassigned[0].label).toBe("待核对区域");
  });

  it("rejects legacy per-question choice suggestions and explains how to select the whole block", async () => {
    const result = await run([answerQuestion]);
    expect(result.regions).toEqual([]);
    expect(result.unassigned).toEqual([]);
    expect(result.objectiveGroups).toEqual([]);
    expect(result.notice).toContain("选择题单行框已忽略");
  });

  it("never uses an objective block from failed semantic analysis", async () => {
    const value = response();
    Object.assign(value.analysis.candidates[0], { role: "objective_group", printed_question_label: null, association_status: "unassociated" });
    value.analysis.semantic_status = "failed";
    mocks.analyze.mockResolvedValue(value);
    expect((await run()).objectiveGroups).toEqual([]);
  });

  it("sends only the coordinate contract from local frames containing detection confidence", async () => {
    await run();
    const request = JSON.parse(JSON.stringify(mocks.analyze.mock.calls[0][1]));
    expect(request.candidates).toEqual([{ candidate_id: "page-1-frame-1", bbox, evidence_refs: [] }]);
    expect(Object.keys(request.candidates[0].bbox).sort()).toEqual(["height", "width", "x", "y"]);
  });

  it("explains rejected analysis request format without suggesting missing form fields", async () => {
    mocks.analyze.mockRejectedValue(new ApiClientError(400, "invalid_request", "请检查必填项和填写格式。"));
    await expect(run()).rejects.toThrow("答题卡分析请求格式未通过校验，请刷新页面后重试");
  });

  it("explicitly reports completed analysis with no reliable answer regions", async () => {
    const value = response(); value.analysis.candidates = [];
    mocks.analyze.mockResolvedValue(value);
    const result = await run();
    expect(result.regions).toEqual([]); expect(result.unassigned).toEqual([]);
    expect(result.notice).toContain("未找到可靠的答题区域");
    expect(result.processingSucceeded).toBe(true);
  });

  it.each([
    ["LOCAL_FILL_RECOVERY_OCR_TIMEOUT", "局部填空文字识别超时"],
    ["LOCAL_FILL_RECOVERY_OCR_FAILED", "局部填空文字识别未完成"],
    ["LOCAL_FILL_RECOVERY_OCR_UNAVAILABLE", "局部填空文字识别暂不可用"],
    ["LOCAL_FILL_RECOVERY_BUDGET_EXCEEDED", "局部填空检测已达处理时限"]
  ])("reports page-level local failure %s independently of model completion", async (code, message) => {
    const value = response(); value.analysis.reason_codes = [code];
    mocks.analyze.mockResolvedValue(value);
    const result = await run();
    expect(result.regions).toHaveLength(1);
    expect(result.processingSucceeded).toBe(true);
    expect(result.retainLocalFillCandidates).toBe(true);
    expect(result.notice).toContain(message);
    expect(result.notice).not.toContain(code);
  });

  it("does not echo unknown page reasons or use them to preserve stale suggestions", async () => {
    const value = response();
    value.analysis.reason_codes = ["raw-private-reason", "toString", "__proto__", "constructor"];
    mocks.analyze.mockResolvedValue(value);
    const result = await run();
    expect(result.notice).toBeUndefined();
    expect(result.processingSucceeded).toBe(true);
    expect(result.retainLocalFillCandidates).toBe(false);
  });

  it.each(["not_configured", "failed", "not_applicable"] as const)("keeps geometry unassigned when semantics are %s", async semanticStatus => {
    const value = response(); value.analysis.semantic_status = semanticStatus;
    value.analysis.candidates[0].role = "uncertain"; mocks.analyze.mockResolvedValue(value);
    const result = await run(); expect(result.regions).toEqual([]); expect(result.unassigned).toHaveLength(1);
    expect(result.notice).toContain("人工关联");
  });

  it("preserves the actual failed image request category without creating local question associations", async () => {
    const value = response(); value.analysis.semantic_status = "failed";
    value.analysis.semantic_error = { code: "model_request_rejected", retryable: false };
    mocks.analyze.mockResolvedValue(value);
    const result = await run();
    expect(result.notice).toContain("图像请求被拒绝");
    expect(result.regions).toEqual([]);
    expect(result.unassigned).toHaveLength(1);
    expect(result.modelName).toBeUndefined();
  });

  it("reports a rejected model request before overlapping fallback geometry", async () => {
    const value = response(); value.analysis.semantic_status = "failed";
    value.analysis.semantic_error = { code: "model_request_rejected", upstream_http_status: 400, upstream_error_code: "invalid_parameter" };
    value.analysis.candidates[0].reason_codes = ["REGION_CONFLICT"];
    mocks.analyze.mockResolvedValue(value);
    const result = await run();
    expect(result.notice).toContain("图像请求被拒绝");
    expect(result.notice).toContain("HTTP 400");
    expect(result.notice).toContain("供应商未接受当前请求参数");
    expect(result.notice).not.toContain("重叠冲突");
    expect(result.regions).toEqual([]);
  });

  it("translates a safe missing-model category and HTTP status", async () => {
    const value = response(); value.analysis.semantic_status = "failed";
    value.analysis.semantic_error = { code: "model_request_rejected", upstream_http_status: 404, upstream_error_code: "model_not_found" };
    mocks.analyze.mockResolvedValue(value);
    const result = await run();
    expect(result.notice).toContain("HTTP 404");
    expect(result.notice).toContain("供应商未找到所选模型");
    expect(result.notice).not.toContain("model_not_found");
  });

  it.each(["raw-provider-code-with-secret", "toString", "__proto__", "constructor"])("does not echo unknown upstream code %s and keeps the HTTP status", async upstreamCode => {
    const value = response(); value.analysis.semantic_status = "failed";
    value.analysis.semantic_error = { code: "model_request_rejected", upstream_http_status: 400,
      upstream_error_code: upstreamCode };
    mocks.analyze.mockResolvedValue(value);
    const result = await run();
    expect(result.notice).toContain("HTTP 400");
    expect(result.notice).not.toContain(upstreamCode);
    expect(result.notice).toContain("核对模型接口配置");
  });

  it("keeps duplicate or unknown printed labels unassigned", async () => {
    const value = response(); const candidate = value.analysis.candidates[0];
    value.analysis.candidates.push({ ...candidate, candidate_id: "other" }, { ...candidate, candidate_id: "unknown", printed_question_label: "999" });
    mocks.analyze.mockResolvedValue(value);
    const result = await run(); expect(result.regions).toEqual([]); expect(result.unassigned).toHaveLength(3);
  });

  it("requires boundary correction when semantic association has a region conflict", async () => {
    const value = response(); value.analysis.candidates[0].reason_codes = ["REGION_CONFLICT"];
    mocks.analyze.mockResolvedValue(value);
    const result = await run(); expect(result.regions).toEqual([]); expect(result.unassigned).toHaveLength(1);
    expect(result.notice).toContain("重叠冲突");
  });

  it("discards suspected synthetic page partitions even when the backend marks them associated", async () => {
    const value = response();
    value.analysis.candidates[0].reason_codes = ["SUSPECTED_SYNTHETIC_PAGE_PARTITION", "REGION_CONFLICT"];
    mocks.analyze.mockResolvedValue(value);
    const result = await run();
    expect(result.regions).toEqual([]);
    expect(result.unassigned).toEqual([]);
    expect(result.processingSucceeded).toBe(false);
    expect(result.notice).toBe("第 1 页模型返回疑似均分的区域，未通过版面校验，已忽略这批候选；请核对模型图像理解能力或手工框选。");
  });

  it("retains valid suggestions while excluding flagged proposals and reports flags from the original response", async () => {
    const value = response();
    value.analysis.candidates.push({ ...value.analysis.candidates[0], candidate_id: "synthetic",
      reason_codes: ["SUSPECTED_SYNTHETIC_PAGE_PARTITION"] });
    mocks.analyze.mockResolvedValue(value);
    const result = await run();
    expect(result.regions).toHaveLength(1);
    expect(result.regions[0].question_id).toBe(answerQuestion.id);
    expect(result.unassigned).toEqual([]);
    expect(result.notice).toContain("已忽略这批候选");
    expect(result.processingSucceeded).toBe(true);
    value.analysis.candidates = [{ ...value.analysis.candidates[1], role: "identity", proposed_bbox: null }];
    const filtered = await run();
    expect(filtered.regions).toEqual([]);
    expect(filtered.unassigned).toEqual([]);
    expect(filtered.notice).toContain("已忽略这批候选");
  });

  it("rejects invalid coordinates and unsupported instruction regions", async () => {
    const value = response(); const candidate = value.analysis.candidates[0];
    value.analysis.candidates = [{ ...candidate, proposed_bbox: { ...bbox, width: Number.NaN } },
      { ...candidate, proposed_bbox: { ...bbox, x: .9 } }, { ...candidate, role: "instruction" }];
    mocks.analyze.mockResolvedValue(value);
    const result = await run(); expect(result.regions).toEqual([]); expect(result.unassigned).toEqual([]);
  });

  it("filters a verified forbidden sign out of the canvas while retaining the actual answer region", async () => {
    const value = response(), answer = value.analysis.candidates[0];
    value.analysis.candidates.push({ ...answer, candidate_id: "forbidden-sign", role: "instruction",
      printed_question_label: null, association_status: "unassociated", semantic_check: "supported",
      reason_codes: ["MODEL_PROPOSED_IDENTITY", "LOCAL_FORBIDDEN_LABEL_VERIFIED"] });
    mocks.analyze.mockResolvedValue(value);
    const result = await run();
    expect(result.regions).toMatchObject([{ question_id: answerQuestion.id, ...bbox }]);
    expect(result.unassigned).toEqual([]);
    expect(result.identityRegions).toEqual([]);
    expect(result.objectiveGroups).toEqual([]);
    expect(result.processingSucceeded).toBe(true);
    expect(result.notice).toBeUndefined();
  });

  it.each([
    ["LOCAL_FORBIDDEN_OCR_UNAVAILABLE", "禁区文字核验暂不可用"],
    ["LOCAL_FORBIDDEN_OCR_WARMING", "禁区文字核验正在准备"],
    ["LOCAL_FORBIDDEN_OCR_TIMEOUT", "禁区文字核验超时"],
    ["LOCAL_FORBIDDEN_OCR_FAILED", "禁区文字核验未完成"],
    ["LOCAL_FORBIDDEN_CHECK_BUDGET_EXCEEDED", "禁区文字核验已达本页处理时限"]
  ])("keeps an unknown region and explains forbidden review failure %s", async (code, explanation) => {
    const value = response();
    Object.assign(value.analysis.candidates[0], { role: "uncertain", printed_question_label: null,
      association_status: "unassociated", semantic_check: "needs_review", reason_codes: ["SEMANTIC_EVIDENCE_INSUFFICIENT"] });
    value.analysis.reason_codes = [code];
    mocks.analyze.mockResolvedValue(value);
    const result = await run();
    expect(result.regions).toEqual([]);
    expect(result.unassigned).toMatchObject([{ ...bbox, label: "待核对区域", semantic_check: "needs_review" }]);
    expect(result.unassigned[0].question_id).toBeUndefined();
    expect(result.notice).toContain(explanation);
    expect(result.notice).toContain("未对应区域可重试或修改");
    expect(result.notice).not.toContain(code);
    expect(result.processingSucceeded).toBe(true);
    expect(result.retainLocalFillCandidates).toBe(false);
  });

  it("does not expose arbitrary forbidden OCR text or trigger local fill retention", async () => {
    const value = response();
    value.analysis.reason_codes = ["PRIVATE_STUDENT_VALUE", "__proto__", "constructor", "LOCAL_FORBIDDEN_OCR_TIMEOUT_EXTRA"];
    mocks.analyze.mockResolvedValue(value);
    const result = await run();
    expect(result.notice).toBeUndefined();
    expect(result.regions).toHaveLength(1);
    expect(result.processingSucceeded).toBe(true);
    expect(result.retainLocalFillCandidates).toBe(false);
  });

  it("reports forbidden and fill failures independently and retains the existing fill failure behavior", async () => {
    const value = response();
    value.analysis.reason_codes = ["LOCAL_FORBIDDEN_OCR_TIMEOUT", "LOCAL_FILL_RECOVERY_OCR_TIMEOUT"];
    mocks.analyze.mockResolvedValue(value);
    const result = await run();
    expect(result.notice).toContain("禁区文字核验超时");
    expect(result.notice).toContain("局部填空文字识别超时");
    expect(result.processingSucceeded).toBe(true);
    expect(result.retainLocalFillCandidates).toBe(true);
  });

  it("keeps named identity fields separate from question associations without storing entered values", async () => {
    const value = response(), candidate = value.analysis.candidates[0];
    value.analysis.candidates.push({ ...candidate, candidate_id: "student-number", role: "identity", identity_label: "学号",
      printed_question_label: "1", association_status: "unassociated" });
    mocks.analyze.mockResolvedValue(value);
    const result = await run();
    expect(result.regions).toHaveLength(1);
    expect(result.unassigned).toEqual([]);
    expect(result.identityRegions).toMatchObject([{ label: "学号", ...bbox, suggestion_source: "multimodal_layout" }]);
    expect(result.identityRegions?.[0].question_id).toBeUndefined();
    expect(result.identityRegions?.[0].option_regions).toBeUndefined();
  });

  it("uses a fixed fallback identity label instead of retaining an unrecognized student value", async () => {
    const value = response();
    Object.assign(value.analysis.candidates[0], { role: "identity", printed_question_label: "张三 20260001", identity_label: "张三" });
    mocks.analyze.mockResolvedValue(value);
    const result = await run();
    expect(result.regions).toEqual([]); expect(result.unassigned).toEqual([]);
    expect(result.identityRegions).toMatchObject([{ label: "身份信息" }]);
    expect(JSON.stringify(result.identityRegions)).not.toContain("张三");
    value.analysis.semantic_status = "failed";
    expect((await run()).identityRegions).toEqual([]);
  });

  it.each(["revision", "source", "template"])("rejects responses for a different %s", async field => {
    const value = response();
    if (field === "revision") value.template_revision++;
    if (field === "source") value.source_sha256 = "other";
    if (field === "template") value.template_id = "other";
    mocks.analyze.mockResolvedValue(value);
    await expect(run()).rejects.toThrow("版本已变化");
  });

  it("does not send oversized pixels after all compression attempts", async () => {
    vi.mocked(HTMLCanvasElement.prototype.toDataURL).mockReturnValue(`data:image/jpeg;base64,${"A".repeat(1500000)}`);
    await expect(run()).rejects.toThrow("大小限制");
    expect(mocks.analyze).not.toHaveBeenCalled();
    expect(HTMLCanvasElement.prototype.toDataURL).toHaveBeenCalledTimes(5);
  });
});
