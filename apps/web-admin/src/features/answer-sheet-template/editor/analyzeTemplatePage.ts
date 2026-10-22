import { analyzeAnswerSheetTemplatePage, type AnalysisBox, type TemplateFillGradingGroup, type TemplatePageAnalysis } from "../../../api/answerSheetTemplateAnalysis";
import { ApiClientError } from "../../../api/client";
import type { LayoutRegion, TemplateEditorCandidate } from "../../../api/configuration";
import type { Question } from "../../../api/papers";
import { normalizeIdentityRegionLabel } from "./identityRegionFields";
import { containsObjectiveBox, isObjectiveQuestion } from "./objectiveGridGeometry";
import { automaticRegionConflictReason, localFillRecoveryMessage, localForbiddenReviewMessage, regionIssueMessage } from "./regionLifecycle";
import { buildMultimodalObjectiveRegions } from "./multimodalObjectiveItems";
import { applyAutomaticFillGradingGroups } from "./fillGradingGroups";

export type RemotePageAnalyzer = (input: {
  canvas: HTMLCanvasElement; pageNo: number; frames: AnalysisBox[]; signal: AbortSignal;
}) => Promise<{ regions: LayoutRegion[]; unassigned: LayoutRegion[]; identityRegions?: LayoutRegion[];
  registrationMarks?: LayoutRegion[];
  fillGradingGroups?: TemplateFillGradingGroup[];
  unassignedCandidates?: TemplateEditorCandidate[];
  objectiveGroups?: LayoutRegion[]; notice?: string; modelName?: string; processingSucceeded?: boolean; retainLocalFallback?: boolean;
  retainLocalFillCandidates?: boolean }>;

function validBox(box: AnalysisBox | null): box is AnalysisBox {
  return Boolean(box && [box.x, box.y, box.width, box.height].every(Number.isFinite)
    && box.x >= 0 && box.y >= 0 && box.width > 0 && box.height > 0
    && box.x + box.width <= 1 && box.y + box.height <= 1);
}

function semanticFailureReason(error: TemplatePageAnalysis["analysis"]["semantic_error"]) {
  const code = error?.code;
  if (!code) return "";
  if (code === "model_request_rejected") {
    const categories: Record<string, string> = {
      model_not_found: "供应商未找到所选模型，请核对模型名称及接口地址",
      unsupported_model: "供应商接口不支持所选模型",
      invalid_api_key: "模型接口密钥无效",
      insufficient_quota: "模型接口额度不足",
      invalid_parameter: "供应商未接受当前请求参数",
      unsupported_parameter: "供应商不支持当前请求参数",
      context_length_exceeded: "模型请求超过上下文长度限制",
      rate_limit_exceeded: "模型接口请求频率受限",
      unsupported_image: "供应商未接受当前图片格式或内容",
      model_not_support_image: "供应商接口提示所选模型不支持图片"
    };
    // 只显示状态码及已知类别；供应商原文和未知错误码可能含凭据、接口细节或任意内容。
    const status = error?.upstream_http_status;
    const upstreamCode = error?.upstream_error_code ?? "";
    const details = [typeof status === "number" && Number.isInteger(status) && status >= 100 && status <= 599 ? `HTTP ${status}` : "",
      Object.prototype.hasOwnProperty.call(categories, upstreamCode) ? categories[upstreamCode] : ""].filter(Boolean).join("；");
    return `图像请求被拒绝${details ? `（${details}）` : ""}，请核对模型接口配置`;
  }
  if (code === "model_output_invalid") return "模型返回的题号或区域格式无法通过校验";
  if (code === "model_unavailable") return "模型服务暂时不可用，请稍后重试";
  return `失败原因：${code}`;
}

/** 语义校验通过且题号唯一的识别结果自动写入草稿；保存、锁定和考试启用仍由教师操作。 */
export function createTemplatePageAnalyzer({ templateId, revision, sourceHash, questions }: {
  templateId: string; revision: number; sourceHash: string; questions: Question[];
}): RemotePageAnalyzer {
  return async ({ canvas, pageNo, frames, signal }) => {
    // 后台请求体有大小上限；缩放保持整页比例，候选仍用 0～1 页面坐标，无需二次变换。
    const image = document.createElement("canvas");
    const scale = Math.min(1, 1600 / Math.max(canvas.width, canvas.height));
    image.width = Math.max(1, Math.round(canvas.width * scale)); image.height = Math.max(1, Math.round(canvas.height * scale));
    const context = image.getContext("2d");
    if (!context) throw new Error("浏览器无法准备后台分析图像");
    context.fillStyle = "white"; context.fillRect(0, 0, image.width, image.height);
    context.drawImage(canvas, 0, 0, image.width, image.height);
    let data = "";
    // 小题号和填涂边缘依赖细节；优先保留高质量，超过请求预算时才降低压缩质量。
    for (const quality of [.92, .85, .7, .55, .4]) {
      const dataUrl = image.toDataURL("image/jpeg", quality);
      if (!dataUrl.startsWith("data:image/jpeg;base64,")) throw new Error("浏览器不支持分析图像压缩");
      data = dataUrl.split(",")[1] ?? "";
      if (data.length * .75 <= 1024 * 1024) break;
    }
    if (!data || data.length * .75 > 1024 * 1024) throw new Error("单页分析图像超过大小限制，请手工核对区域");
    const bytes = Uint8Array.from(atob(data), character => character.charCodeAt(0));
    const digest = await crypto.subtle.digest("SHA-256", bytes);
    if (signal.aborted) throw new DOMException("已取消", "AbortError");
    const sha256 = Array.from(new Uint8Array(digest), byte => byte.toString(16).padStart(2, "0")).join("");
    const response = await analyzeAnswerSheetTemplatePage(templateId, {
      expected_revision: revision, expected_source_sha256: sourceHash, page_no: pageNo, width: image.width, height: image.height,
      media_type: "image/jpeg", data_base64: data, sha256, writing_state: "unknown",
      // 本地框还带 confidence 等检测信息；接口只接受四个坐标字段，不能直接序列化检测对象。
      candidates: frames.map(({ x, y, width, height }, index) => ({ candidate_id: `page-${pageNo}-frame-${index + 1}`,
        bbox: { x, y, width, height }, evidence_refs: [] }))
    }, signal).catch(error => {
      // 此入口没有必填表单；请求契约错误应说明分析请求失败，避免误导教师反复检查模板字段。
      if (error instanceof ApiClientError && error.status === 400 && error.code === "invalid_request") {
        throw new Error("答题卡分析请求格式未通过校验，请刷新页面后重试；已有区域已保留");
      }
      throw error;
    });
    if (response.template_id !== templateId || response.template_revision !== revision || response.source_sha256 !== sourceHash
      || response.analysis.status !== "review_required" || response.analysis.schema_version !== "answer-sheet-analysis-v1"
      || response.analysis.coordinate_space !== "source_display_normalized" || response.analysis.page_no !== pageNo
      || response.analysis.source_sha256 !== sha256) throw new Error("分析所用模板或来源版本已变化，请重新读取后识别");
    const normalize = (label: string) => label.replace(/\s+/g, "").toLowerCase();
    // 疑似均分的框没有可靠版面依据，不画入本次候选，也不参与正常候选的题号冲突判断；已有人工框仍保留。
    const candidates = response.analysis.candidates.filter(candidate => candidate.page_no === pageNo
      && ["answer", "objective_group", "uncertain", "identity"].includes(candidate.role) && validBox(candidate.proposed_bbox)
      && !candidate.reason_codes.includes("SUSPECTED_SYNTHETIC_PAGE_PARTITION"))
      .map(candidate => {
        const conflict = ["answer", "objective_group", "identity"].includes(candidate.role)
          ? automaticRegionConflictReason(candidate.reason_codes) : "";
        // 旧网关可能仍返回 supported 加冲突码；不让这份矛盾结果直接关联题目或作为身份框。
        return !conflict ? candidate : { ...candidate, role: "uncertain" as const, association_status: "unassociated" as const,
          semantic_check: "needs_review" as const, reason_codes: [...new Set([...candidate.reason_codes, conflict])] };
      });
    // 整批伪框被版面校验拒绝，不能伪装成合法空页，从而在重试时清掉上一轮候选。
    const rejectedPartitionBatch = !candidates.length && response.analysis.candidates.some(candidate =>
      candidate.reason_codes.includes("SUSPECTED_SYNTHETIC_PAGE_PARTITION"));
    const regions: LayoutRegion[] = [];
    const unassigned: LayoutRegion[] = [];
    const identityRegions: LayoutRegion[] = [];
    const registration = response.analysis.registration;
    // 定位点由像素算法独立检测，仍使用来源页坐标；不能把透视校正图中的坐标直接保存到模板。
    const registrationMarks = registration?.coordinate_space === "source_normalized"
      ? registration.status === "not_found" ? [] : registration.registration_marks
        .filter(mark => validBox(mark)).map(mark => ({ ...mark, id: crypto.randomUUID(),
          origin: "local_detector" as const, review_state: "suggested" as const, semantic_check: "supported" as const,
          analysis_run_id: response.analysis.request_id })) : undefined;
    const metadata = (candidate: typeof candidates[number]): Partial<LayoutRegion> => ({ origin: candidate.origin ?? "model", review_state: "suggested",
      semantic_check: candidate.semantic_check ?? "not_evaluated", reason_codes: [...candidate.reason_codes],
      analysis_run_id: response.analysis.request_id });
    const unassignedCandidates: TemplateEditorCandidate[] = [];
    const addUnassigned = (region: LayoutRegion, candidate: typeof candidates[number]) => {
      unassigned.push(region);
      // 题号只是待核对线索，不能写成正式 question_id；类别仍需保留，避免共区确认吞掉身份或选择题候选。
      const original = response.analysis.candidates.find(item => item.candidate_id === candidate.candidate_id) ?? candidate;
      unassignedCandidates.push({ id: region.id, page_no: pageNo, role: original.role, region,
        ...(original.printed_question_label ? { question_no: original.printed_question_label } : {}) });
    };
    const detectedObjectiveGroups = response.analysis.semantic_status === "completed" ? candidates
      .filter(candidate => candidate.role === "objective_group" && candidate.semantic_check === "supported") : [];
    const objectiveGroups: LayoutRegion[] = [];
    const allObjectiveBoxes: LayoutRegion[] = detectedObjectiveGroups
      .map(candidate => ({ id: crypto.randomUUID(), ...candidate.proposed_bbox!, ...metadata(candidate), label: "选择题区域", suggestion_source: "multimodal_layout" }));
    detectedObjectiveGroups.forEach((candidate, index) => {
      const matched = buildMultimodalObjectiveRegions(candidate, questions, candidates);
      if (matched?.length) regions.push(...matched.map(region => ({ ...region, ...metadata(candidate) })));
      else objectiveGroups.push(allObjectiveBoxes[index]);
    });
    let ignoredObjectiveRows = false;
    for (const candidate of candidates) {
      // 只把存在语义问题或缺少结论的结果留作异常框；通过校验的模型结果直接关联，无需教师逐框确认。
      if (response.analysis.semantic_status === "completed" && candidate.semantic_check !== "supported") {
        // 网关会把待核对 answer 降为 uncertain；仍可显示本地正式题号线索，但不写入 question_id。
        const hinted = candidate.origin === "local_detector" && ["answer", "uncertain"].includes(candidate.role)
          ? questions.filter(question => question.question_type === "fill_blank"
            && normalize(question.question_no) === normalize(candidate.printed_question_label ?? "")) : [];
        addUnassigned({ id: crypto.randomUUID(), ...candidate.proposed_bbox!, ...metadata(candidate),
          label: hinted.length === 1 ? `填空第 ${hinted[0].question_no} 题 · 待核对` : "待核对区域",
          suggestion_source: candidate.origin === "local_detector" ? "ocr_layout" : "multimodal_layout", semantic_check: "needs_review" }, candidate);
        continue;
      }
      // 选择题已在上方按实测题号和选项处理；大框本身不算一道题，缺少完整子项时才留作纠错入口。
      if (candidate.role === "objective_group") continue;
      // 姓名、学号等属于独立身份区域，不参与题号匹配，也不能降级为待关联题目框。
      if (candidate.role === "identity") {
        if (response.analysis.semantic_status === "completed") identityRegions.push({
          id: crypto.randomUUID(), ...candidate.proposed_bbox!, ...metadata(candidate), label: normalizeIdentityRegionLabel(candidate.identity_label),
          suggestion_source: "multimodal_layout"
        });
        continue;
      }
      const label = candidate.printed_question_label ? normalize(candidate.printed_question_label) : undefined;
      const matching = label ? questions.filter(question => normalize(question.question_no) === label) : [];
      if (matching.some(isObjectiveQuestion) || candidate.reason_codes.includes("OBJECTIVE_QUESTION_REQUIRES_GROUP")) {
        ignoredObjectiveRows = true;
        continue;
      }
      if (allObjectiveBoxes.some(group => containsObjectiveBox(group, candidate.proposed_bbox!))) continue;
      const unique = matching.length === 1 && candidates.filter(item => item.role === "answer" && item.printed_question_label
        && normalize(item.printed_question_label) === label && item.association_status === "associated").length === 1;
      const region: LayoutRegion = { id: crypto.randomUUID(), ...candidate.proposed_bbox!, ...metadata(candidate), option_regions: [], suggestion_source: "multimodal_layout" };
      // 冲突题号、未知题号和未确认归属只显示待关联框，不能用模型提供的字符串当正式题目 ID。
      if (response.analysis.semantic_status === "completed" && candidate.role === "answer" && candidate.association_status === "associated"
        && !candidate.reason_codes.includes("REGION_CONFLICT") && unique) {
        regions.push({ ...region, question_id: matching[0].id, label: matching[0].question_no });
      } else addUnassigned(region, candidate);
    }
    const failureReason = semanticFailureReason(response.analysis.semantic_error);
    // 局部补框可单独失败；主模型成功仍更新其结果，但不能据此清除上一轮有填空证据的候选。
    const localFillFailure = localFillRecoveryMessage(response.analysis.reason_codes);
    const localForbiddenFailure = localForbiddenReviewMessage(response.analysis.reason_codes);
    // 从原响应保留拒绝原因；过滤掉伪框后仍要说明发生了什么，重叠提示不能掩盖版面失真。
    const primaryNotice = response.analysis.candidates.some(candidate => candidate.reason_codes.includes("SUSPECTED_SYNTHETIC_PAGE_PARTITION"))
      ? `第 ${pageNo} 页模型返回疑似均分的区域，未通过版面校验，已忽略这批候选；请核对模型图像理解能力或手工框选。`
      : response.analysis.semantic_status !== "completed"
        ? `第 ${pageNo} 页多模态语义分析${response.analysis.semantic_status === "not_configured" ? "尚未配置" : "未完成"}${failureReason ? `：${failureReason}` : ""}，几何候选仍需人工关联和核对。`
        : candidates.some(candidate => candidate.reason_codes.includes("REGION_CONFLICT"))
          ? `第 ${pageNo} 页候选区域存在重叠冲突，请调整边界后再关联题号。`
        : !candidates.length ? `第 ${pageNo} 页未找到可靠的答题区域，可重新识别或补充区域。` : undefined;
    const objectiveNotice = ignoredObjectiveRows && !detectedObjectiveGroups.length
      ? `第 ${pageNo} 页返回的选择题单行框已忽略，请点击“识别选择题”，框住完整选择题区域后核对题号和选项。` : undefined;
    const needsReview = unassigned.filter(region => region.semantic_check === "needs_review");
    const reviewNotice = needsReview.length ? `第 ${pageNo} 页识别已结束，${needsReview.length} 个区域需要核对：${regionIssueMessage(needsReview[0])}。` : undefined;
    const localFillNotice = localFillFailure ? `第 ${pageNo} 页${localFillFailure}；已自动对应的题目可继续使用，未对应区域可重试或修改。` : undefined;
    const localForbiddenNotice = localForbiddenFailure ? `第 ${pageNo} 页${localForbiddenFailure}；已自动对应的题目可继续使用，未对应区域可重试或修改。` : undefined;
    const notice = [primaryNotice, objectiveNotice, reviewNotice, localFillNotice, localForbiddenNotice].filter(Boolean).join(" ") || undefined;
    const modelName = response.analysis.semantic_status === "completed" ? response.analysis.model_identity?.model_name : undefined;
    const fillGradingGroups = response.analysis.semantic_status === "completed" ? response.analysis.fill_grading_groups ?? [] : [];
    const groupedRegions = applyAutomaticFillGradingGroups(fillGradingGroups, regions, identityRegions, questions);
    return { regions: groupedRegions, unassigned, unassignedCandidates, identityRegions, registrationMarks, fillGradingGroups, objectiveGroups, notice, modelName,
      processingSucceeded: response.analysis.semantic_status === "completed" && !rejectedPartitionBatch, retainLocalFallback: false,
      retainLocalFillCandidates: Boolean(localFillFailure) };
  };
}
