import type { LayoutRegion, TemplateEditorCandidate, TemplateLayout } from "../../../api/configuration";
import { sameIdentityRegion } from "./identityRegionFields";
import { objectiveBoxesOverlap } from "./objectiveGridGeometry";
import { clearInvalidAutomaticFillGroups } from "./fillGradingGroups";

/** 旧草稿无法证明未经人工核对，缺少来源或审核信息时按受保护区域处理。 */
export function isReplaceableSuggestion(region: LayoutRegion) {
  return (region.origin === "model" || region.origin === "local_detector") && region.review_state === "suggested";
}

export function markRegionEdited(region: LayoutRegion): LayoutRegion {
  return { ...region, origin: region.origin ?? "human", review_state: "edited" };
}

export function sameCandidateArea(left: LayoutRegion, right: LayoutRegion) {
  const width = Math.max(0, Math.min(left.x + left.width, right.x + right.width) - Math.max(left.x, right.x));
  const height = Math.max(0, Math.min(left.y + left.height, right.y + right.height) - Math.max(left.y, right.y));
  const intersection = width * height;
  return intersection / (left.width * left.height + right.width * right.height - intersection) >= .85;
}

export function candidateMaps(candidates: TemplateEditorCandidate[], role: "objective_group" | "unassigned") {
  const pages = new Map<number, LayoutRegion[]>();
  for (const candidate of candidates) {
    if (candidate.region.review_state === "discarded" || (candidate.role === "objective_group") !== (role === "objective_group")) continue;
    pages.set(candidate.page_no, [...(pages.get(candidate.page_no) ?? []), candidate.region]);
  }
  return pages;
}

export interface PageAnalysisCommit {
  sourceHash: string;
  successfulPages: number[];
  retainLocalFillPages?: number[];
  pages: Map<number, LayoutRegion[]>;
  identities: Map<number, LayoutRegion[]>;
  registrationMarks?: Map<number, LayoutRegion[]>;
  candidates: TemplateEditorCandidate[];
}

/** 成功页替换自动候选；局部填空检测失败时仅保留未被新结果替代的填空证据框，人工区域始终优先。 */
export function commitPageAnalysis(layout: TemplateLayout, commit: PageAnalysisCommit): TemplateLayout {
  const successful = new Set(commit.successfulPages);
  const retainLocalFill = new Set(commit.retainLocalFillPages);
  const oldCandidates = layout.editor_state?.source_sha256 === commit.sourceHash ? layout.editor_state?.candidates ?? [] : [];
  const retainedCandidates = oldCandidates.filter(candidate => !successful.has(candidate.page_no)
    || candidate.region.review_state === "discarded" || !isReplaceableSuggestion(candidate.region));
  const manualMemberOn = (pageNo: number, region: LayoutRegion) => Boolean(region.question_id && layout.pages.some(page => page.page_no === pageNo
    && page.grading_groups?.some(group => group.question_ids.includes(region.question_id!))));
  const protectedRegions = layout.pages.flatMap(page => [...page.question_regions, ...page.identity_regions]
    .filter(region => !successful.has(page.page_no) || !isReplaceableSuggestion(region) || manualMemberOn(page.page_no, region)));
  const occupied = new Set([...protectedRegions.flatMap(region => region.question_id ? [region.question_id] : []),
    ...layout.pages.flatMap(page => (page.grading_groups ?? []).flatMap(group => group.question_ids))]);
  const discardedOn = (pageNo: number, region: LayoutRegion) => oldCandidates.some(candidate => candidate.page_no === pageNo
    && candidate.region.review_state === "discarded" && sameCandidateArea(candidate.region, region));
  // 未关联候选也可能经人工调整；重试不能绕过这些候选，重新覆盖同一片区域。
  const protectedOn = (pageNo: number, region: LayoutRegion) => [
    ...layout.pages.filter(page => page.page_no === pageNo).flatMap(page => [...page.question_regions, ...page.identity_regions]),
    ...retainedCandidates.filter(candidate => candidate.page_no === pageNo && candidate.region.review_state !== "discarded")
      .map(candidate => candidate.region)
  ].some(existing => !isReplaceableSuggestion(existing)
    && (existing.question_id && existing.question_id === region.question_id || objectiveBoxesOverlap(existing, region)))
    // 人工整区已确认成员和外框；重试不能在内部重新生成候选，要求教师再次逐题对应。
    || layout.pages.some(page => page.page_no === pageNo && page.grading_groups?.some(group =>
      region.question_id && group.question_ids.includes(region.question_id) || objectiveBoxesOverlap(group.bbox, region)));
  const localFillCandidate = (candidate: TemplateEditorCandidate) => ["answer", "uncertain"].includes(candidate.role)
    && candidate.region.origin === "local_detector" && candidate.region.review_state === "suggested"
    && candidate.region.semantic_check === "needs_review" && !candidate.region.question_id
    && candidate.region.reason_codes?.includes("LOCAL_FILL_REQUIRES_REVIEW");
  for (const candidate of oldCandidates) {
    if (!successful.has(candidate.page_no) || !retainLocalFill.has(candidate.page_no) || !localFillCandidate(candidate)
      || protectedOn(candidate.page_no, candidate.region)) continue;
    // 部分补框成功时新证据优先；题号提示只用于去重，不能据此关联正式题目或增加覆盖数。
    const replaced = commit.candidates.some(fresh => fresh.page_no === candidate.page_no
      && (sameCandidateArea(fresh.region, candidate.region) || localFillCandidate(fresh)
        && candidate.region.label && candidate.region.label !== "待核对区域" && candidate.region.label === fresh.region.label))
      || [...(commit.pages.get(candidate.page_no) ?? []), ...(commit.identities.get(candidate.page_no) ?? [])]
        .some(region => sameCandidateArea(region, candidate.region));
    if (!replaced) retainedCandidates.push(candidate);
  }
  const pending = [...retainedCandidates];
  const pages = layout.pages.map(page => {
    if (!successful.has(page.page_no)) return page;
    // 人工共区保留已有实测子框；重识别既不改整区成员，也不借替换自动框删掉它们的位置。
    const questions = page.question_regions.filter(region => !isReplaceableSuggestion(region) || manualMemberOn(page.page_no, region));
    const identities = page.identity_regions.filter(region => !isReplaceableSuggestion(region));
    // 旧服务缺少定位结果时保持原点位；明确检测完成的重试只替换自动点，人工校正继续优先。
    const registrationMarks = commit.registrationMarks?.has(page.page_no)
      ? [...page.registration_marks.filter(region => !isReplaceableSuggestion(region))] : page.registration_marks;
    for (const mark of commit.registrationMarks?.get(page.page_no) ?? []) {
      if (!registrationMarks.some(existing => objectiveBoxesOverlap(existing, mark))) registrationMarks.push(mark);
    }
    for (const region of commit.pages.get(page.page_no) ?? []) {
      if (discardedOn(page.page_no, region) || protectedOn(page.page_no, region)) continue;
      if (!region.question_id || occupied.has(region.question_id)) {
        // 当前正式协议是一题一个 ROI；另一块保留并解释，不能静默丢失跨页作答区。
        const candidate = { ...region, question_id: undefined, label: "待核对区域", semantic_check: "needs_review" as const,
          reason_codes: [...new Set([...(region.reason_codes ?? []), "MULTIPLE_REGIONS_UNSUPPORTED"])] };
        pending.push({ id: candidate.id, page_no: page.page_no, role: "uncertain", region: candidate });
      } else { occupied.add(region.question_id); questions.push(region); }
    }
    for (const region of commit.identities.get(page.page_no) ?? []) {
      // 通用“身份信息”可能有多块填写区，只合并重合框；姓名等命名字段仍保持本页单例。
      if (!discardedOn(page.page_no, region) && !protectedOn(page.page_no, region)
        && !identities.some(existing => sameIdentityRegion(existing, region))) identities.push(region);
    }
    return { ...page, question_regions: clearInvalidAutomaticFillGroups(questions, identities), identity_regions: identities,
      registration_marks: registrationMarks };
  });
  for (const candidate of commit.candidates) {
    if (!successful.has(candidate.page_no) || discardedOn(candidate.page_no, candidate.region)
      || protectedOn(candidate.page_no, candidate.region)) continue;
    if (!pending.some(existing => existing.page_no === candidate.page_no && sameCandidateArea(existing.region, candidate.region))) pending.push(candidate);
  }
  return { ...layout, pages, editor_state: { version: 1, source_sha256: commit.sourceHash, candidates: pending } };
}

const ISSUE_MESSAGES: Record<string, string> = {
  // 这些原因来自模型自报观察的交叉检查，只解释待核对原因，不能描述成图像内容已被独立验证。
  MODEL_OBSERVATIONS_INVALID: "识别依据不完整，请核对区域用途和范围",
  MODEL_OBS_OBJECTIVE_QUESTION_TYPE_CONFLICT: "识别为选择题的区域包含其他正式题型的题号，请核对区域用途",
  MODEL_OBS_QUESTION_TYPE_CONFLICT: "识别依据中的题型与正式题目不一致，请核对题号和区域用途",
  MODEL_OBS_FILL_QUESTION_TYPE_CONFLICT: "识别为填空题的区域与正式题型不一致，请核对题号和范围",
  MODEL_OBS_FILL_OPTION_CONFLICT: "填空题区域同时被识别出选择选项，请核对是否框入了邻题",
  MODEL_OBS_OBJECTIVE_PATTERN_MISSING: "尚未识别出支持此选择题区域的选项结构，请核对区域用途",
  MODEL_OBS_OBJECTIVE_LABELS_MISSING: "此选择题区域尚未读出具体题号，请核对完整范围",
  MODEL_OBS_OBJECTIVE_STATIC_LABEL_CONFLICT: "此选择题区域的识别标签指向其他用途，请核对范围和类别",
  MODEL_OBS_OBJECTIVE_IDENTITY_CONFLICT: "此选择题区域同时被识别为身份填写区，请核对范围和类别",
  MODEL_OBS_OBJECTIVE_BARCODE_CONFLICT: "此选择题区域同时被识别出条码结构，请核对范围和类别",
  MODEL_OBS_OBJECTIVE_ITEMS_MISSING: "选择题区域尚未识别出每题的题号和选项框，可重新识别或补充",
  MODEL_OBS_OBJECTIVE_ITEMS_INVALID: "选择题的题号或选项数据格式异常，请重新识别或修改",
  MODEL_OBS_OBJECTIVE_ITEMS_ROLE_CONFLICT: "其他类别的区域包含选择题配置，请核对区域用途",
  MODEL_OBS_OBJECTIVE_ITEMS_QUESTION_CONFLICT: "选择题题号重复或不在正式选择题清单中，请修改题号或区域",
  MODEL_OBS_OBJECTIVE_ITEMS_GEOMETRY_CONFLICT: "选择题或选项框相互重叠或超出所属区域，请调整范围",
  MODEL_OBS_OBJECTIVE_ITEMS_OPTIONS_CONFLICT: "选择题选项缺失、重复或顺序不完整，请修改选项配置",
  MODEL_OBS_OBJECTIVE_ITEMS_COVERAGE_CONFLICT: "选择题配置未对应本区域识别到的全部题号，请补齐或修改",
  MODEL_OBS_IDENTITY_OPTION_CONFLICT: "此身份区域同时被识别出选择选项，请核对是否应为选择题区",
  MODEL_OBS_IDENTITY_QUESTION_CONFLICT: "此身份区域同时被识别出正式题号，请核对区域用途",
  MODEL_OBS_IDENTITY_STATIC_LABEL_CONFLICT: "此身份区域的识别标签指向其他用途，请核对字段和范围",
  MODEL_OBS_IDENTITY_LABEL_CONFLICT: "识别到的身份标签与所选字段不一致，请核对字段类别",
  MODEL_OBS_IDENTITY_EVIDENCE_MISSING: "尚未识别出与此身份字段对应的标签或填写结构，请核对区域用途",
  MODEL_OBS_IDENTITY_BARCODE_CONFLICT: "此身份填写区同时被识别出条码结构，请核对是否应拆分区域",
  MODEL_OBS_BARCODE_IDENTITY_CONFLICT: "此条码区域同时被识别为身份填写区，请核对是否应拆分区域",
  MODEL_OBS_BARCODE_EVIDENCE_MISSING: "尚未识别出条码图案或明确贴码说明，请核对此处是否为条码区",
  MODEL_OBS_BARCODE_TYPE_CONFLICT: "条码与二维码的识别依据不一致，请核对码的类型",
  MODEL_OBS_WARNING_CATEGORY_CONFLICT: "此区域的识别依据包含禁止作答或说明标签，请核对是否框入说明区",
  MODEL_OBS_ANSWER_STATIC_LABEL_CONFLICT: "此作答区域的识别标签指向身份或条码，请核对区域用途",
  MODEL_OBS_ANSWER_IDENTITY_CONFLICT: "此作答区域同时被识别为身份填写区，请核对范围和类别",
  MODEL_OBS_ANSWER_BARCODE_CONFLICT: "此作答区域同时被识别出条码结构，请核对范围和类别",
  MODEL_OBS_ANSWER_LABEL_CONFLICT: "用于关联的题号与区域内识别到的题号不一致，请重新核对题目",
  MODEL_OBS_MULTIPLE_QUESTION_LABELS: "同一作答区域识别出多个正式题号，请核对是否需要分开框选",
  MODEL_OBS_INSTRUCTION_CONTENT_CONFLICT: "识别为说明的区域可能包含作答或身份内容，已保留此框供核对",
  MODEL_OBS_OTHER_CONTENT_CONFLICT: "识别为无关内容的区域可能包含作答或身份内容，已保留此框供核对",
  MODEL_OBS_INSTRUCTION_EVIDENCE_MISSING: "尚未识别出明确的说明标签，请核对区域用途",
  MODEL_OBS_QUESTION_LABEL_NOT_IN_LIST: "区域内识别到的题号不在正式题目清单中，请核对题号",
  MODEL_OBS_ANCHOR_OUT_OF_PAGE: "识别依据的位置无效或超出当前页，请核对区域范围",
  MODEL_OBS_ANCHOR_OUTSIDE_REGION: "识别依据的位置离此区域较远，请核对是否引用了邻区内容",
  MODEL_OBS_CANDIDATE_GEOMETRY_INVALID: "区域位置无效，请重新核对边界",
  SEMANTIC_ROLE_QUESTION_TYPE_CONFLICT: "区域类别与正式题型不一致，请核对题号和区域类型",
  SEMANTIC_OBJECTIVE_ITEMS_CONFLICT: "选择题的题号或选项框存在缺失、重复或范围冲突，可重新识别或修改",
  SEMANTIC_ROLE_IDENTITY_CONFLICT: "身份信息与答题区域的分类相互矛盾，请核对区域用途",
  SEMANTIC_ASSOCIATION_CONFLICT: "题号归属存在矛盾，请核对正式题目后重新关联",
  SEMANTIC_CONTENT_CONFLICT: "图像内容与区域类别不一致，请重新选择区域用途",
  SEMANTIC_FORBIDDEN_AREA_DETECTED: "此处检测到禁止作答说明，不能直接作为条码或答题区域",
  SEMANTIC_MULTIPART_UNSUPPORTED: "此题存在另一块作答区，当前版本暂不支持一题多区域，请核对后处理",
  SEMANTIC_REGION_OVERLAP_CONFLICT: "区域与其他区域明显重叠，请核对边界和类别",
  SEMANTIC_CHECK_BUDGET_EXCEEDED: "此区域尚未完成内容核验，请人工核对",
  SEMANTIC_OCR_FAILED: "区域文字核验未完成，请人工核对",
  SEMANTIC_OCR_TIMEOUT: "本页文字核验超时，请人工核对",
  SEMANTIC_OCR_PROCESS_FAILED: "本页文字核验执行失败，请人工核对",
  SEMANTIC_OCR_OUTPUT_TOO_LARGE: "本页文字核验结果过大，请人工核对",
  SEMANTIC_OCR_PARSE_FAILED: "本页文字核验结果格式异常，请人工核对",
  SEMANTIC_OCR_UNAVAILABLE: "区域文字核验暂不可用，请人工核对",
  SEMANTIC_BARCODE_PATTERN_ONLY: "仅检测到疑似条纹，尚不能确定是条码，请核对",
  ROLE_QUESTION_TYPE_CONFLICT: "区域类别与正式题型不一致，请核对题号和区域类型",
  QUESTION_TYPE_CONFLICT: "区域类别与正式题型不一致，请核对题号和区域类型",
  IDENTITY_EVIDENCE_MISSING: "未找到与此身份字段相符的印刷标签",
  IDENTITY_LABEL_UNSUPPORTED: "身份字段缺少可信文字证据",
  OBJECTIVE_EVIDENCE_MISSING: "未找到可靠的选项排列或填涂结构",
  BARCODE_EVIDENCE_MISSING: "尚未验证条码图案或贴码说明",
  NON_ANSWER_CONTENT: "此处可能是禁止作答或说明区域",
  REGION_CONFLICT: "区域与其他区域重叠，请核对边界和类别",
  MULTIPLE_REGIONS_UNSUPPORTED: "此题存在另一块作答区，当前版本暂不支持一题多区域，请核对后处理",
  SEMANTIC_EVIDENCE_INSUFFICIENT: "类别证据不足，请确认区域用途后再关联",
  LOCAL_FILL_REQUIRES_REVIEW: "已依据填空标题、题号和答题横线补充此区域，请核对范围后关联",
  LOCAL_FILL_PARENT_REGION_REQUIRES_REVIEW: "此填空区域位于另一待核对大框内，请核对是否保留大框",
  LOCAL_FILL_SUBREGIONS_DETECTED: "同一区域内已发现独立填空题，请核对是否需要保留此框",
  LOCAL_FILL_RECOVERY_BUDGET_EXCEEDED: "本页局部填空检测已达处理时限，剩余题目请人工核对",
  LOCAL_FILL_RECOVERY_OCR_UNAVAILABLE: "局部填空文字识别暂不可用，请人工补框",
  LOCAL_FILL_RECOVERY_OCR_TIMEOUT: "局部填空文字识别超时，请人工补框",
  LOCAL_FILL_RECOVERY_OCR_FAILED: "局部填空文字识别未完成，请人工补框",
  LOCAL_FORBIDDEN_OCR_UNAVAILABLE: "禁区文字核验暂不可用",
  LOCAL_FORBIDDEN_OCR_WARMING: "禁区文字核验正在准备，请稍后重试",
  LOCAL_FORBIDDEN_OCR_TIMEOUT: "禁区文字核验超时",
  LOCAL_FORBIDDEN_OCR_FAILED: "禁区文字核验未完成",
  LOCAL_FORBIDDEN_CHECK_BUDGET_EXCEEDED: "禁区文字核验已达本页处理时限",
};

/** 页级错误只显示固定类别；未知上游文本不能作为教师提示，也不能触发保留旧候选。 */
export function localFillRecoveryMessage(reasonCodes: readonly string[] = []) {
  const allowed = new Set(["LOCAL_FILL_RECOVERY_BUDGET_EXCEEDED", "LOCAL_FILL_RECOVERY_OCR_UNAVAILABLE",
    "LOCAL_FILL_RECOVERY_OCR_TIMEOUT", "LOCAL_FILL_RECOVERY_OCR_FAILED"]);
  return [...new Set(reasonCodes.filter(code => allowed.has(code)).map(code => ISSUE_MESSAGES[code]))].join("；");
}

/** 禁区复核失败只解释本次保留原因；不得用未知上游原文作为教师提示或改变填空候选保留规则。 */
export function localForbiddenReviewMessage(reasonCodes: readonly string[] = []) {
  const allowed = new Set(["LOCAL_FORBIDDEN_OCR_UNAVAILABLE", "LOCAL_FORBIDDEN_OCR_WARMING", "LOCAL_FORBIDDEN_OCR_TIMEOUT",
    "LOCAL_FORBIDDEN_OCR_FAILED", "LOCAL_FORBIDDEN_CHECK_BUDGET_EXCEEDED"]);
  return [...new Set(reasonCodes.filter(code => allowed.has(code)).map(code => ISSUE_MESSAGES[code]))].join("；");
}

/** 正式区域中的明确反证不能被 supported 掩盖；OCR 未读出、不可用或预算不足本身不属于反证。 */
export function automaticRegionConflictReason(reasonCodes: readonly string[] = []) {
  for (const code of reasonCodes) {
    if (["CONTENT_FORBIDDEN_WRITING", "SEMANTIC_FORBIDDEN_AREA_DETECTED", "LOCAL_FORBIDDEN_LABEL_VERIFIED",
      "MULTIMODAL_FORBIDDEN_REGION_EXCLUDED"].includes(code)) return "SEMANTIC_FORBIDDEN_AREA_DETECTED";
    if (["REGION_CONFLICT", "SEMANTIC_MULTIPART_UNSUPPORTED", "MODEL_OBS_MULTIPLE_QUESTION_LABELS",
      "OBJECTIVE_QUESTION_REQUIRES_GROUP"].includes(code)
      || (code.startsWith("SEMANTIC_") || code.startsWith("MODEL_OBS_")) && code.endsWith("CONFLICT")) return code;
  }
  return "";
}

export function regionIssueMessage(region: LayoutRegion) {
  return [...new Set([automaticRegionConflictReason(region.reason_codes), ...(region.reason_codes ?? [])].map(code => Object.prototype.hasOwnProperty.call(ISSUE_MESSAGES, code)
    ? ISSUE_MESSAGES[code] : "").filter(Boolean))].join("；")
    || (region.semantic_check === "needs_review" ? "区域类别尚未验证，请核对后关联题目或丢弃" : "请核对区域范围及题号归属");
}

/** 自动结果通过语义校验即可使用；教师修改或重新关联后，不再要求额外逐框确认。 */
export function regionRequiresReview(region: LayoutRegion) {
  if (region.review_state === "edited" || region.review_state === "confirmed") return false;
  if (automaticRegionConflictReason(region.reason_codes)) return true;
  if (region.review_state === "suggested" && region.semantic_check === "supported") return false;
  if (region.origin === "library" && region.review_state === "suggested" && region.semantic_check === "not_evaluated") return false;
  // 与服务端锁定门禁一致：只兼容完全没有审核元数据的旧区域，部分旧字段不能当作已通过核验。
  return Boolean(region.origin || region.review_state || region.semantic_check || region.analysis_run_id || region.reason_codes?.length);
}

export function unresolvedTemplateMessage(layout: TemplateLayout) {
  const candidates = layout.editor_state?.candidates.filter(candidate => candidate.region.review_state !== "discarded") ?? [];
  if (candidates.length) return `还有 ${candidates.length} 个区域需要核对：${regionIssueMessage(candidates[0].region)}。可先保存草稿`;
  const regions = layout.pages.flatMap(page => [...page.registration_marks, ...page.question_regions, ...page.identity_regions]);
  const conflicts = regions.filter(regionRequiresReview);
  if (conflicts.length) return `还有 ${conflicts.length} 个区域需要核对：${regionIssueMessage(conflicts[0])}`;
  return "";
}
