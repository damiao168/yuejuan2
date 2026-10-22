import { describe, expect, it } from "vitest";
import type { LayoutRegion, TemplateLayout } from "../../../api/configuration";
import { commitPageAnalysis, localForbiddenReviewMessage, regionIssueMessage, regionRequiresReview, unresolvedTemplateMessage, type PageAnalysisCommit } from "./regionLifecycle";
import { templateLayoutHistoryReducer } from "./templateLayoutHistory";

const automatic = (id: string, patch: Partial<LayoutRegion> = {}): LayoutRegion => ({ id, x: .1, y: .1, width: .3, height: .3,
  origin: "model", review_state: "suggested", semantic_check: "supported", ...patch });
function layout(): TemplateLayout {
  return { pages: [1, 2].map(page_no => ({ page_no, width: 1000, height: 1000, registration_marks: [],
    question_regions: [], identity_regions: [] })), editor_state: { version: 1, source_sha256: "source", candidates: [] } };
}
const commit = (patch: Partial<PageAnalysisCommit> = {}): PageAnalysisCommit => ({ sourceHash: "source", successfulPages: [1],
  pages: new Map(), identities: new Map(), candidates: [], ...patch });

describe("region candidate review boundary", () => {
  it("preserves a manual fill area and its measured child ROIs when reanalysis cannot recover every member", () => {
    const old = layout();
    old.pages[0].grading_groups = [{ group_id: "manual-fill", question_ids: ["q12", "q13", "q14"],
      bbox: { x: .05, y: .05, width: .5, height: .5 } }];
    old.pages[0].question_regions = [automatic("real-12", { question_id: "q12" })];
    const next = commitPageAnalysis(old, commit({ pages: new Map([[1, [automatic("replacement-12", { question_id: "q12" })]]]),
      candidates: [{ id: "new-parent", page_no: 1, role: "uncertain", region: automatic("new-parent") }] }));
    expect(next.pages[0].grading_groups).toBe(old.pages[0].grading_groups);
    expect(next.pages[0].question_regions).toEqual(old.pages[0].question_regions);
    expect(next.editor_state?.candidates).toEqual([]);
  });

  it("replaces only automatic registration marks and preserves human point corrections on retries", () => {
    const old = layout();
    const human = automatic("human-point", { x: .01, y: .01, width: .01, height: .01, review_state: "edited" });
    old.pages[0].registration_marks = [human, automatic("old-point", { x: .98, y: .01, width: .01, height: .01 })];
    const next = commitPageAnalysis(old, commit({ registrationMarks: new Map([[1, [
      automatic("overlapping-pixel-point", { x: .012, y: .01, width: .01, height: .01 }),
      automatic("new-point", { x: .98, y: .02, width: .01, height: .01 })
    ]]]) }));
    expect(next.pages[0].registration_marks.map(mark => mark.id)).toEqual(["human-point", "new-point"]);
    expect(next.pages[0].registration_marks[0]).toBe(human);
    expect(unresolvedTemplateMessage(next)).toBe("");
    expect(commitPageAnalysis(next, commit()).pages[0].registration_marks).toBe(next.pages[0].registration_marks);
    expect(commitPageAnalysis(next, commit({ registrationMarks: new Map([[1, []]]) })).pages[0].registration_marks).toEqual([human]);
    expect(old.pages[0].registration_marks.map(mark => mark.id)).toEqual(["human-point", "old-point"]);
  });
  it("replaces a wrong automatic choice block and identity with the corrected run instead of accumulating", () => {
    const old = layout();
    old.pages[0].identity_regions = [automatic("wrong-name", { label: "姓名" })];
    old.editor_state!.candidates = [{ id: "wrong-choice", page_no: 1, role: "objective_group", region: automatic("wrong-choice") }];
    const next = commitPageAnalysis(old, commit({ identities: new Map([[1, [automatic("correct-name", { label: "姓名", x: .6 })]]]),
      candidates: [{ id: "correct-choice", page_no: 1, role: "objective_group", region: automatic("correct-choice", { y: .6 }) }] }));
    expect(next.pages[0].identity_regions.map(region => region.id)).toEqual(["correct-name"]);
    expect(next.editor_state!.candidates.map(candidate => candidate.id)).toEqual(["correct-choice"]);
  });

  it("clears old suggestions on a legitimately empty success while retaining failed-page suggestions", () => {
    const old = layout();
    old.pages.forEach((page, index) => { page.question_regions = [automatic(`q${index}`, { question_id: `q${index}` })]; });
    old.editor_state!.candidates = [1, 2].map(page_no => ({ id: `c${page_no}`, page_no, role: "uncertain", region: automatic(`c${page_no}`) }));
    const next = commitPageAnalysis(old, commit());
    expect(next.pages[0].question_regions).toEqual([]);
    expect(next.pages[1]).toBe(old.pages[1]);
    expect(next.editor_state!.candidates.map(candidate => candidate.id)).toEqual(["c2"]);
  });

  it("keeps distinct generic identity fields while deduplicating the same physical field", () => {
    const first = automatic("identity-left", { label: "身份信息", x: .1, width: .2 });
    const second = automatic("identity-right", { label: "身份信息", x: .6, width: .2 });
    const duplicate = automatic("identity-left-again", { label: "身份信息", x: .11, width: .2 });
    const next = commitPageAnalysis(layout(), commit({ identities: new Map([[1, [first, second, duplicate]]]) }));
    expect(next.pages[0].identity_regions.map(region => region.id)).toEqual([first.id, second.id]);
    expect(unresolvedTemplateMessage(next)).toBe("");
  });

  it("retains the human named identity field over a separate automatic duplicate", () => {
    const old = layout();
    const human = automatic("human-name", { origin: "human", review_state: "edited", label: " 姓名 ", width: .2 });
    old.pages[0].identity_regions = [human];
    const next = commitPageAnalysis(old, commit({ identities: new Map([[1, [automatic("model-name", { label: "姓名", x: .6, width: .2 })]]]) }));
    expect(next.pages[0].identity_regions).toEqual([human]);
  });

  it("retains only evidenced local fill candidates on the failed local page while replacing model results", () => {
    const old = layout();
    const fill = automatic("fill-12", { origin: "local_detector", semantic_check: "needs_review",
      label: "填空第 12 题 · 待核对", reason_codes: ["LOCAL_FILL_REQUIRES_REVIEW"] });
    old.editor_state!.candidates = [
      { id: fill.id, page_no: 1, role: "uncertain", region: fill },
      { id: "wrong-model", page_no: 1, role: "uncertain", region: automatic("wrong-model", { x: .6 }) },
      { id: "old-local-geometry", page_no: 1, role: "uncertain", region: automatic("old-local-geometry", { origin: "local_detector" }) },
      { id: "other-page-fill", page_no: 2, role: "uncertain", region: { ...fill, id: "other-page-fill" } }
    ];
    const next = commitPageAnalysis(old, commit({ successfulPages: [1, 2], retainLocalFillPages: [1],
      candidates: [{ id: "new-model", page_no: 1, role: "uncertain", region: automatic("new-model", { x: .6 }) }] }));
    expect(next.editor_state!.candidates.map(candidate => candidate.id)).toEqual(["fill-12", "new-model"]);
    expect(next.editor_state!.candidates[0].region).toBe(fill);
    expect(next.pages.every(page => !page.question_regions.length)).toBe(true);
    // 下一次局部检测真正成功时，合法空结果仍需清掉旧框，失败保留不能永久固化候选。
    expect(commitPageAnalysis(next, commit()).editor_state!.candidates).toEqual([]);
  });

  it("prefers fresh local fill geometry by the safe question hint while retaining unrecovered fills", () => {
    const old = layout();
    const fill = (id: string, label: string, x: number) => automatic(id, { x, width: .2, origin: "local_detector",
      semantic_check: "needs_review", label, reason_codes: ["LOCAL_FILL_REQUIRES_REVIEW"] });
    old.editor_state!.candidates = [
      { id: "old-12", page_no: 1, role: "uncertain", region: fill("old-12", "填空第 12 题 · 待核对", .1) },
      { id: "old-14", page_no: 1, role: "uncertain", region: fill("old-14", "填空第 14 题 · 待核对", .7) }
    ];
    const fresh = { id: "new-12", page_no: 1, role: "uncertain" as const, region: fill("new-12", "填空第 12 题 · 待核对", .4) };
    const next = commitPageAnalysis(old, commit({ retainLocalFillPages: [1], candidates: [fresh] }));
    expect(next.editor_state!.candidates.map(candidate => candidate.id)).toEqual(["old-14", "new-12"]);
    expect(next.editor_state!.candidates[1]).toBe(fresh);
  });

  it("replaces an old local fill with fresh equivalent geometry without blocking the model", () => {
    const old = layout();
    old.editor_state!.candidates = [{ id: "old-fill", page_no: 1, role: "uncertain",
      region: automatic("old-fill", { origin: "local_detector", semantic_check: "needs_review", reason_codes: ["LOCAL_FILL_REQUIRES_REVIEW"] }) }];
    const next = commitPageAnalysis(old, commit({ retainLocalFillPages: [1],
      candidates: [{ id: "new-model", page_no: 1, role: "uncertain", region: automatic("new-model") }] }));
    expect(next.editor_state!.candidates.map(candidate => candidate.id)).toEqual(["new-model"]);
  });

  it.each(["formal", "pending"] as const)("does not retain a local fill over a human %s region", kind => {
    const old = layout();
    old.editor_state!.candidates = [{ id: "old-fill", page_no: 1, role: "uncertain",
      region: automatic("old-fill", { origin: "local_detector", semantic_check: "needs_review", reason_codes: ["LOCAL_FILL_REQUIRES_REVIEW"] }) }];
    const human = automatic("human", { origin: "human", review_state: "edited" });
    if (kind === "formal") old.pages[0].question_regions.push(human);
    else old.editor_state!.candidates.push({ id: human.id, page_no: 1, role: "uncertain", region: human });
    const next = commitPageAnalysis(old, commit({ retainLocalFillPages: [1] }));
    expect(JSON.stringify(next)).not.toContain("old-fill");
    expect(JSON.stringify(next)).toContain("human");
  });

  it("does not carry old local fill evidence into another source", () => {
    const old = layout();
    old.editor_state!.candidates = [{ id: "old-fill", page_no: 1, role: "uncertain",
      region: automatic("old-fill", { origin: "local_detector", semantic_check: "needs_review", reason_codes: ["LOCAL_FILL_REQUIRES_REVIEW"] }) }];
    const next = commitPageAnalysis(old, commit({ sourceHash: "other-source", retainLocalFillPages: [1] }));
    expect(next.editor_state!.candidates).toEqual([]);
    expect(next.editor_state!.source_sha256).toBe("other-source");
  });

  it.each(["edited", "confirmed", undefined] as const)("protects %s regions and pending candidates from overlapping replacements", reviewState => {
    const old = layout();
    const protectedRegion = automatic("human-question", { review_state: reviewState, question_id: "q1" });
    const protectedPending = automatic("human-pending", { review_state: reviewState, x: .6 });
    old.pages[0].question_regions = [protectedRegion];
    old.editor_state!.candidates = [{ id: protectedPending.id, page_no: 1, role: "uncertain", region: protectedPending }];
    const next = commitPageAnalysis(old, commit({ pages: new Map([[1, [automatic("replacement-question", { question_id: "q1" })]]]),
      identities: new Map([[1, [automatic("replacement-name", { x: .6, label: "姓名" })]]]),
      candidates: [{ id: "replacement-block", page_no: 1, role: "objective_group", region: automatic("replacement-block", { x: .6 }) }] }));
    expect(next.pages[0].question_regions).toEqual([protectedRegion]);
    expect(next.pages[0].identity_regions).toEqual([]);
    expect(next.editor_state!.candidates).toEqual(old.editor_state!.candidates);
  });

  it("accepts teacher corrections without another confirmation and leaves unassigned candidates unresolved", () => {
    const old = layout();
    old.pages[0].question_regions = [automatic("question", { question_id: "q1", review_state: "edited", semantic_check: "needs_review" })];
    let history = { past: [] as TemplateLayout[], present: old, future: [] as TemplateLayout[] };
    expect(unresolvedTemplateMessage(old)).toBe("");
    history = templateLayoutHistoryReducer(history, { type: "confirmPageRegions", pageNo: 1 });
    expect(unresolvedTemplateMessage(history.present)).toBe("");
    expect(history.present.pages[0].question_regions[0].origin).toBe("model");
    const pending = { ...history.present, editor_state: { ...old.editor_state!, candidates: [{ id: "pending", page_no: 1,
      role: "uncertain" as const, region: automatic("pending") }] } };
    expect(unresolvedTemplateMessage(templateLayoutHistoryReducer({ ...history, present: pending }, { type: "confirmPageRegions", pageNo: 1 }).present))
      .toContain("需要核对");
    history = templateLayoutHistoryReducer(history, { type: "undoEditor" });
    expect(history.present.pages[0].question_regions[0].review_state).toBe("edited");
    history = templateLayoutHistoryReducer(history, { type: "redoEditor" });
    expect(history.present.pages[0].question_regions[0].review_state).toBe("confirmed");
  });

  it("preserves discard tombstones through serialization, undo, redo and retry", () => {
    const old = layout(); const candidate = automatic("unwanted");
    old.editor_state!.candidates = [{ id: candidate.id, page_no: 1, role: "uncertain", region: candidate }];
    let history = templateLayoutHistoryReducer({ past: [], present: old, future: [] }, { type: "discardCandidate", candidateId: candidate.id });
    const saved = JSON.parse(JSON.stringify(history.present)) as TemplateLayout;
    expect(unresolvedTemplateMessage(saved)).toBe("");
    expect(commitPageAnalysis(saved, commit({ candidates: [{ id: "new-id", page_no: 1, role: "uncertain", region: automatic("new-id") }] }))
      .editor_state!.candidates.map(item => item.id)).toEqual([candidate.id]);
    history = templateLayoutHistoryReducer(history, { type: "undoEditor" });
    expect(history.present.editor_state!.candidates[0].region.review_state).toBe("suggested");
    history = templateLayoutHistoryReducer(history, { type: "redoEditor" });
    expect(history.present.editor_state!.candidates[0].region.review_state).toBe("discarded");
  });

  it("uses library registration marks without demanding per-region confirmation", () => {
    const old = layout();
    old.pages[0].registration_marks = [automatic("registration", { origin: "library", semantic_check: "not_evaluated" })];
    old.pages[0].identity_regions = [automatic("name", { origin: "library", semantic_check: "not_evaluated", label: "姓名" })];
    old.pages[0].question_regions = [automatic("answer", { origin: "library", semantic_check: "not_evaluated", question_id: "q1" })];
    expect(unresolvedTemplateMessage(old)).toBe("");
    const confirmed = templateLayoutHistoryReducer({ past: [], present: old, future: [] }, { type: "confirmPageRegions", pageNo: 1 });
    expect(unresolvedTemplateMessage(confirmed.present)).toBe("");
    expect(confirmed.present.pages[0].registration_marks[0]).toMatchObject({ origin: "library", review_state: "confirmed" });
    const undone = templateLayoutHistoryReducer(confirmed, { type: "undoEditor" });
    expect(undone.present.pages[0].registration_marks[0].review_state).toBe("suggested");
  });

  it("keeps explicit conflicts in inherited library geometry unresolved", () => {
    const old = layout();
    old.pages[0].question_regions = [automatic("answer", { origin: "library", semantic_check: "needs_review", question_id: "q1" })];
    expect(unresolvedTemplateMessage(old)).toContain("需要核对");
  });

  it("uses supported model questions and identities immediately while retaining replaceable provenance", () => {
    const next = commitPageAnalysis(layout(), commit({
      pages: new Map([[1, [automatic("answer", { question_id: "q1" })]]]),
      identities: new Map([[1, [automatic("name", { label: "姓名", x: .6,
        reason_codes: ["MODEL_PROPOSED_IDENTITY", "SEMANTIC_OCR_UNAVAILABLE"] })]]])
    }));
    expect(unresolvedTemplateMessage(next)).toBe("");
    expect(next.pages[0].question_regions[0]).toMatchObject({ origin: "model", review_state: "suggested" });
    expect(next.pages[0].identity_regions[0].review_state).toBe("suggested");
    const retried = commitPageAnalysis(next, commit({ pages: new Map([[1, [automatic("corrected", { question_id: "q1" })]]]) }));
    expect(retried.pages[0].question_regions.map(region => region.id)).toEqual(["corrected"]);
  });

  it.each(["needs_review", "not_evaluated", undefined] as const)("does not accept unresolved model classification %s as a successful match", semanticCheck => {
    const old = layout();
    old.pages[0].question_regions = [automatic("answer", { question_id: "q1", semantic_check: semanticCheck })];
    expect(unresolvedTemplateMessage(old)).toContain("需要核对");
  });

  it.each<{ name: string; metadata: Partial<LayoutRegion> }>([
    { name: "model origin without review state", metadata: { origin: "model" } },
    { name: "supported model without review state", metadata: { origin: "model", semantic_check: "supported" } },
    { name: "local detector without review state", metadata: { origin: "local_detector" } },
    { name: "human origin without an explicit edit", metadata: { origin: "human" } },
    { name: "library origin without inherited review metadata", metadata: { origin: "library" } },
    { name: "suggested result without a semantic decision", metadata: { review_state: "suggested" } },
    { name: "semantic decision without review state", metadata: { semantic_check: "supported" } },
    { name: "analysis run without a review decision", metadata: { analysis_run_id: "old-run" } },
    { name: "diagnostic codes without a review decision", metadata: { reason_codes: ["SEMANTIC_OCR_UNAVAILABLE"] } },
    { name: "discarded result left in the formal layout", metadata: { review_state: "discarded", semantic_check: "supported" } }
  ])("keeps a reloaded historical region unresolved with $name", ({ metadata }) => {
    // 部分旧审核字段不能等同于完全没有元数据，否则界面会放行而服务端在锁定时拒绝。
    const old = layout();
    old.pages[0].question_regions = [{ id: "legacy-answer", x: .1, y: .1, width: .3, height: .3,
      question_id: "q1", ...metadata }];
    const saved = JSON.parse(JSON.stringify(old)) as TemplateLayout;
    expect(regionRequiresReview(saved.pages[0].question_regions[0])).toBe(true);
    expect(unresolvedTemplateMessage(saved)).toContain("需要核对");
  });

  it("keeps historical regions with no audit metadata usable after reloading", () => {
    const old = layout();
    old.pages[0].question_regions = [{ id: "legacy-answer", x: .1, y: .1, width: .3, height: .3,
      question_id: "q1", analysis_run_id: "", reason_codes: [] }];
    const saved = JSON.parse(JSON.stringify(old)) as TemplateLayout;
    expect(regionRequiresReview(saved.pages[0].question_regions[0])).toBe(false);
    expect(unresolvedTemplateMessage(saved)).toBe("");
  });

  it.each(["CONTENT_FORBIDDEN_WRITING", "LOCAL_FORBIDDEN_LABEL_VERIFIED", "MULTIMODAL_FORBIDDEN_REGION_EXCLUDED",
    "SEMANTIC_FORBIDDEN_AREA_DETECTED", "MODEL_OBS_WARNING_CATEGORY_CONFLICT", "MODEL_OBS_MULTIPLE_QUESTION_LABELS",
    "SEMANTIC_MULTIPART_UNSUPPORTED", "REGION_CONFLICT", "OBJECTIVE_QUESTION_REQUIRES_GROUP"])(
    "keeps a supported historical automatic region unresolved when it carries %s", code => {
      const old = layout();
      old.pages[0].question_regions = [automatic("answer", { question_id: "q1", reason_codes: [code] })];
      expect(unresolvedTemplateMessage(old)).toContain("需要核对");
      for (const review_state of ["edited", "confirmed"] as const) {
        expect(regionRequiresReview({ ...old.pages[0].question_regions[0], review_state })).toBe(false);
      }
    });

  it.each(["SEMANTIC_OCR_UNAVAILABLE", "SEMANTIC_OCR_TIMEOUT", "SEMANTIC_CHECK_BUDGET_EXCEEDED",
    "MODEL_OBS_IDENTITY_EVIDENCE_MISSING"])("does not turn supported diagnostics %s into a per-region review requirement", code => {
      expect(regionRequiresReview(automatic("answer", { question_id: "q1", reason_codes: [code] }))).toBe(false);
    });

  it.each([
    ["SEMANTIC_ROLE_QUESTION_TYPE_CONFLICT", "正式题型"], ["SEMANTIC_CONTENT_CONFLICT", "图像内容"],
    ["SEMANTIC_FORBIDDEN_AREA_DETECTED", "禁止作答"], ["SEMANTIC_MULTIPART_UNSUPPORTED", "一题多区域"],
    ["SEMANTIC_OBJECTIVE_ITEMS_CONFLICT", "题号或选项框"]
  ])("explains actual worker reason %s", (code, explanation) => {
    expect(regionIssueMessage(automatic("pending", { reason_codes: [code] }))).toContain(explanation);
  });

  it.each([
    ["MODEL_OBS_OBJECTIVE_QUESTION_TYPE_CONFLICT", "其他正式题型的题号"],
    ["MODEL_OBS_OBJECTIVE_ITEMS_MISSING", "尚未识别出每题的题号和选项框"],
    ["MODEL_OBS_OBJECTIVE_ITEMS_INVALID", "数据格式异常"],
    ["MODEL_OBS_OBJECTIVE_ITEMS_ROLE_CONFLICT", "其他类别的区域包含选择题配置"],
    ["MODEL_OBS_OBJECTIVE_ITEMS_QUESTION_CONFLICT", "题号重复或不在正式选择题清单中"],
    ["MODEL_OBS_OBJECTIVE_ITEMS_GEOMETRY_CONFLICT", "相互重叠或超出所属区域"],
    ["MODEL_OBS_OBJECTIVE_ITEMS_OPTIONS_CONFLICT", "选项缺失、重复或顺序不完整"],
    ["MODEL_OBS_OBJECTIVE_ITEMS_COVERAGE_CONFLICT", "未对应本区域识别到的全部题号"],
    ["MODEL_OBS_IDENTITY_OPTION_CONFLICT", "是否应为选择题区"],
    ["MODEL_OBS_WARNING_CATEGORY_CONFLICT", "是否框入说明区"],
    ["MODEL_OBS_ANSWER_LABEL_CONFLICT", "题号不一致"],
    ["MODEL_OBS_OTHER_CONTENT_CONFLICT", "已保留此框供核对"]
  ])("gives a concrete review action for model observation conflict %s", (code, explanation) => {
    const pending = automatic("pending", { semantic_check: "needs_review", reason_codes: [code, code, "PRIVATE_STUDENT_VALUE"] });
    const message = regionIssueMessage(pending);
    expect(message).toContain(explanation);
    expect(message).not.toContain("PRIVATE_STUDENT_VALUE");
    expect(message).not.toContain("；");
    expect(message).not.toContain("已验证");
  });

  it("keeps unknown model observation reasons out of the review message", () => {
    expect(regionIssueMessage(automatic("pending", { semantic_check: "needs_review",
      reason_codes: ["MODEL_OBS_PRIVATE_STUDENT_VALUE", "__proto__", "constructor"] })))
      .toBe("区域类别尚未验证，请核对后关联题目或丢弃");
  });

  it.each([
    ["LOCAL_FORBIDDEN_OCR_UNAVAILABLE", "禁区文字核验暂不可用"],
    ["LOCAL_FORBIDDEN_OCR_WARMING", "禁区文字核验正在准备，请稍后重试"],
    ["LOCAL_FORBIDDEN_OCR_TIMEOUT", "禁区文字核验超时"], ["LOCAL_FORBIDDEN_OCR_FAILED", "禁区文字核验未完成"],
    ["LOCAL_FORBIDDEN_CHECK_BUDGET_EXCEEDED", "禁区文字核验已达本页处理时限"]
  ])("explains the fixed forbidden review failure %s", (code, explanation) => {
    expect(localForbiddenReviewMessage([code, code])).toBe(explanation);
  });

  it("does not expose unknown forbidden review reasons or inherited object keys", () => {
    expect(localForbiddenReviewMessage(["PRIVATE_STUDENT_VALUE", "__proto__", "toString", "constructor",
      "LOCAL_FORBIDDEN_OCR_TIMEOUT_EXTRA", "LOCAL_FILL_RECOVERY_OCR_TIMEOUT"])).toBe("");
    expect(localForbiddenReviewMessage()).toBe("");
  });
});
