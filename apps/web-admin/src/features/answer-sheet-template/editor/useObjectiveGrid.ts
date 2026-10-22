import { useEffect, useRef, useState, type Dispatch, type RefObject } from "react";
import { App } from "antd";
import type { PDFDocumentProxy } from "pdfjs-dist";
import { analyzeAnswerSheetObjectiveGrid, type ObjectiveGridAnalysis, type ObjectiveOptionOrientation } from "../../../api/answerSheetObjectiveGrid";
import type { AnalysisBox } from "../../../api/answerSheetTemplateAnalysis";
import { ApiClientError, getUserErrorMessage } from "../../../api/client";
import type { AnswerSheetTemplate, LayoutRegion, TemplateLayout } from "../../../api/configuration";
import type { PaperVersion, Question } from "../../../api/papers";
import type { TemplateLayoutAction } from "./templateLayoutReducer";
import { buildObjectiveGridRegions, isObjectiveQuestion, objectiveBoxesOverlap, orderObjectiveGroups, validObjectiveBox, type ObjectiveReadOrder } from "./objectiveGridGeometry";
import { objectiveGridPageImage } from "./objectiveGridPageImage";

interface GridDraft {
  open: boolean; busy: boolean; ocrBusy: boolean; applying: boolean;
  block?: AnalysisBox; optionCount: number; orientation: ObjectiveOptionOrientation;
  readOrder: ObjectiveReadOrder; reverse: boolean;
  analysis?: ObjectiveGridAnalysis; assignments: Record<string, string>; questionIds: string[];
  reviewed: boolean; partialReviewed: boolean; error?: string; numberHints?: string;
}
const emptyDraft = (): GridDraft => ({ open: false, busy: false, ocrBusy: false, applying: false, optionCount: 4,
  orientation: "horizontal", readOrder: "column_first", reverse: false, assignments: {}, questionIds: [], reviewed: false, partialReviewed: false });

const partialCodes = ["OPTION_COUNT_OR_GROUPING_UNCERTAIN", "OPTION_SPACING_OR_SIZE_UNCERTAIN"];
const blockingCodes = ["CLIPPED_OPTION_CANDIDATES", "OPTION_GROUP_CONFLICT"];
function detectionState(result?: ObjectiveGridAnalysis, expectedCount = 0) {
  const globalReasons = result?.analysis.reason_codes ?? [];
  const groupReasons = result?.analysis.groups.flatMap(group => group.reason_codes) ?? [];
  // 全区漏格只表示有题未检测到；单个题组自身不完整或存在裁断、冲突时，不能把该批作为完整题组应用。
  const blocked = [...globalReasons, ...groupReasons].some(code => blockingCodes.includes(code))
    || groupReasons.some(code => partialCodes.includes(code));
  // 即使检测器没有返回漏检原因，教师选定的大区题数也能发现整组消失；不能把剩余组顺次冒充全部题目。
  const countMismatch = Boolean(result && expectedCount > 0 && result.analysis.groups.length !== expectedCount);
  return { blocked, partial: !blocked && (countMismatch || globalReasons.some(code => partialCodes.includes(code))) };
}

export function useObjectiveGrid({ selectedTemplate, revision, selectedPaper, pageNo, imageURL, readonly, layout, questions, pdfRef, dispatchLayout, onApplied }: {
  selectedTemplate?: AnswerSheetTemplate; revision: number; selectedPaper?: PaperVersion; pageNo: number; imageURL?: string;
  readonly: boolean; layout: TemplateLayout; questions: Question[]; pdfRef: RefObject<PDFDocumentProxy | undefined>;
  dispatchLayout: Dispatch<TemplateLayoutAction>;
  onApplied?: (block: AnalysisBox, pageNo: number) => void;
}) {
  const { message, modal } = App.useApp();
  const [draft, setDraft] = useState<GridDraft>(emptyDraft);
  const draftRef = useRef(draft);
  const latestLayout = useRef(layout); latestLayout.current = layout;
  const onAppliedRef = useRef(onApplied); onAppliedRef.current = onApplied;
  const pendingApply = useRef<{ identity: string; previousLayout: TemplateLayout; regionIDs: string[]; cleanupBlocks: AnalysisBox[]; pageNo: number; partial: boolean } | undefined>(undefined);
  const generation = useRef(0);
  const mounted = useRef(true);
  const abort = useRef<AbortController | undefined>(undefined);
  const stopOCR = useRef<(() => Promise<void>) | undefined>(undefined);
  const identity = `${selectedTemplate?.exam_id}:${selectedPaper?.id}:${selectedPaper?.file.hash_sha256}:${selectedTemplate?.id}:${revision}:${pageNo}:${readonly}:${questions.map(q => `${q.id}:${q.question_type}:${q.options?.length ?? 0}`).join("|")}`;
  const scope = useRef(identity);
  if (scope.current !== identity) { scope.current = identity; generation.current++; }
  const update = (patch: Partial<GridDraft>) => {
    draftRef.current = { ...draftRef.current, ...patch };
    setDraft(draftRef.current);
  };
  const cancel = () => { generation.current++; abort.current?.abort(); void stopOCR.current?.(); pendingApply.current = undefined; };
  // 页码、来源、版本或权限变化后，本次框选和异步候选都不能套到另一张页面。
  // biome-ignore lint/correctness/useExhaustiveDependencies: identity 是完整的分析作用域，变化时清空候选并取消请求。
  useEffect(() => {
    mounted.current = true;
    cancel(); draftRef.current = emptyDraft(); setDraft(draftRef.current);
    return () => { mounted.current = false; cancel(); };
  }, [identity]);
  const active = () => mounted.current && !readonly && scope.current === identity && Boolean(selectedTemplate?.status === "draft" && selectedPaper && imageURL);
  const invalidate = (patch: Partial<GridDraft>) => {
    if (!active()) return;
    cancel(); update({ ...patch, busy: false, ocrBusy: false, analysis: undefined, assignments: {}, reviewed: false, partialReviewed: false, error: undefined, numberHints: undefined });
  };
  const open = (block?: AnalysisBox) => {
    if (!active()) return;
    if (block !== undefined) {
      if (!validObjectiveBox(block)) return;
      // 主画布的大框仍是建议；打开另一块必须作废旧网格、题号映射和人工勾选，不能借用上次确认。
      invalidate({ open: true, block: { x: block.x, y: block.y, width: block.width, height: block.height } });
    } else update({ open: true });
  };
  const close = () => { cancel(); draftRef.current = emptyDraft(); setDraft(draftRef.current); };
  const changeBlock = (block?: AnalysisBox) => invalidate({ block });
  const changeOptionCount = (optionCount: number) => invalidate({ optionCount });
  const changeOrientation = (orientation: ObjectiveOptionOrientation) => invalidate({ orientation });
  const changeReadOrder = (readOrder: ObjectiveReadOrder) => { if (active()) update({ readOrder, reviewed: false, partialReviewed: false }); };
  const changeReverse = (reverse: boolean) => { if (active()) update({ reverse, reviewed: false, partialReviewed: false }); };
  const changeQuestionIds = (questionIds: string[]) => {
    if (!active()) return;
    const selected = new Set(questionIds);
    update({ questionIds, assignments: Object.fromEntries(Object.entries(draftRef.current.assignments).filter(([, questionID]) => selected.has(questionID))),
      reviewed: false, partialReviewed: false, error: undefined });
  };
  const assignQuestion = (groupID: string, questionID: string) => {
    if (active()) update({ assignments: { ...draftRef.current.assignments, [groupID]: questionID }, reviewed: false, partialReviewed: false });
  };
  const setReviewed = (reviewed: boolean) => { if (active()) update({ reviewed }); };
  const setPartialReviewed = (partialReviewed: boolean) => { if (active()) update({ partialReviewed }); };
  const mapSelectedQuestions = () => {
    if (!active()) return;
    const value = draftRef.current;
    // 有漏检时按位置连续分配题号会让后续题全部错位，只允许逐组选择正式题号。
    if (detectionState(value.analysis, value.questionIds.length).partial) { update({ error: "存在漏检或数量不符，请逐组核对原图并选择题号，不能按阅读顺序批量映射" }); return; }
    const groups = orderObjectiveGroups(value.analysis?.analysis.groups ?? [], value.readOrder);
    if (!groups.length || value.questionIds.length !== groups.length) { update({ error: "所选题目数量须与完整题组数量一致" }); return; }
    update({ assignments: Object.fromEntries(groups.map((group, index) => [group.group_id, value.questionIds[index]])), reviewed: false, partialReviewed: false, error: undefined });
  };
  const analyze = async () => {
    if (!active() || draftRef.current.busy || draftRef.current.ocrBusy) return;
    const value = draftRef.current;
    if (!validObjectiveBox(value.block)) { update({ error: "请先在预览中框选选择题大区" }); return; }
    const block = value.block;
    const requestGeneration = ++generation.current;
    const controller = new AbortController(); abort.current = controller;
    const current = () => scope.current === identity && generation.current === requestGeneration && draftRef.current.open;
    update({ busy: true, error: undefined, analysis: undefined, assignments: {}, reviewed: false, partialReviewed: false });
    try {
      const image = await objectiveGridPageImage(pdfRef.current, pageNo, imageURL);
      if (!current()) return;
      const { canvas: _canvas, ...pixels } = image;
      const result = await analyzeAnswerSheetObjectiveGrid(selectedTemplate!.id, {
        ...pixels, expected_revision: revision, expected_source_sha256: selectedPaper!.file.hash_sha256,
        page_no: pageNo, block_bbox: block, option_count: value.optionCount, option_orientation: value.orientation
      }, controller.signal);
      if (!current()) return;
      if (result.template_id !== selectedTemplate!.id || result.template_revision !== revision || result.source_sha256 !== selectedPaper!.file.hash_sha256
        || result.analysis.schema_version !== "answer-sheet-objective-grid-v1" || result.analysis.status !== "review_required"
        || result.analysis.coordinate_space !== "source_display_normalized" || result.analysis.page_no !== pageNo || result.analysis.source_sha256 !== pixels.sha256
        || (["x", "y", "width", "height"] as const).some(key => Math.abs(result.analysis.block_bbox[key] - block[key]) > .000001)) throw new Error("网格分析的页面或模板版本已变化，请重新解析");
      update({ analysis: result });
    } catch (error) {
      if (current()) update({ error: error instanceof ApiClientError && error.code === "invalid_request"
        ? "选择题网格分析请求未通过校验，请刷新页面后重试"
        : getUserErrorMessage(error, "选择题网格解析未完成，请调整范围后重试") });
    } finally {
      if (current()) update({ busy: false });
    }
  };
  const recognizeNumberHints = async () => {
    if (!active() || draftRef.current.busy || draftRef.current.ocrBusy || !validObjectiveBox(draftRef.current.block)) return;
    const block = draftRef.current.block;
    const requestGeneration = ++generation.current;
    const controller = new AbortController(); abort.current = controller;
    const current = () => scope.current === identity && generation.current === requestGeneration && draftRef.current.open;
    update({ ocrBusy: true, error: undefined, numberHints: undefined });
    let ocr: Awaited<ReturnType<typeof import("./answerSheetOCR")["createAnswerSheetOCR"]>> | undefined;
    try {
      const image = await objectiveGridPageImage(pdfRef.current, pageNo, imageURL);
      if (!current()) return;
      const crop = document.createElement("canvas");
      crop.width = Math.max(1, Math.round(block.width * image.width)); crop.height = Math.max(1, Math.round(block.height * image.height));
      const context = crop.getContext("2d");
      if (!context) throw new Error("浏览器无法读取选择题局部图像");
      context.drawImage(image.canvas, block.x * image.width, block.y * image.height, block.width * image.width, block.height * image.height, 0, 0, crop.width, crop.height);
      const { createAnswerSheetOCR } = await import("./answerSheetOCR");
      ocr = await createAnswerSheetOCR(controller.signal);
      if (!current()) return;
      stopOCR.current = ocr.terminate;
      const words = await ocr.recognize(crop);
      if (!current()) return;
      const numbers = [...new Set(words.filter(word => word.confidence >= 55).flatMap(word => {
        const matched = /^\(?(\d{1,3})\)?[.、:]?$/.exec(word.text.normalize("NFKC").trim());
        return matched ? [matched[1]] : [];
      }))];
      update({ numberHints: numbers.length ? `读到题号提示：${numbers.join("、")}。仅供核对，不会定位填涂格或自动关联。` : "未读到可靠题号。此操作只读取数字，不会修复漏掉的填涂格；请对照原图选择题号。" });
    } catch (error) {
      if (current()) update({ numberHints: getUserErrorMessage(error, "题号辅助识别未完成，可继续手动关联题目") });
    } finally {
      await ocr?.terminate().catch(() => undefined);
      if (current()) { stopOCR.current = undefined; update({ ocrBusy: false }); }
    }
  };
  const groups = orderObjectiveGroups(draft.analysis?.analysis.groups ?? [], draft.readOrder);
  const detection = detectionState(draft.analysis, draft.questionIds.length);
  const selectionError = !draft.questionIds.length ? "请先选择本大区包含的全部题目，含未检测到的题目" :
    Object.values(draft.assignments).some(questionID => questionID && !draft.questionIds.includes(questionID)) ? "题组关联的题号不在本大区所选题目中，请重新核对" : undefined;
  const built = draft.block ? buildObjectiveGridRegions({ groups, block: draft.block, assignments: draft.assignments,
    questions, optionCount: draft.optionCount, orientation: draft.orientation, reverse: draft.reverse }) : { regions: [] as LayoutRegion[], error: "请先框选选择题大区" };
  const apply = () => {
    if (!active() || !draftRef.current.open || draftRef.current.busy || draftRef.current.ocrBusy || draftRef.current.applying || !draftRef.current.reviewed) return;
    const value = draftRef.current;
    const state = detectionState(value.analysis, value.questionIds.length);
    // 先声明原图实际包含哪些题，再核对实测组；缺少这个范围时无法识别静默漏组，也不能宣称整区完整。
    if (!value.questionIds.length || Object.values(value.assignments).some(questionID => questionID && !value.questionIds.includes(questionID))) return;
    if (!value.block || !value.analysis || state.blocked || (state.partial && !value.partialReviewed)) return;
    const output = buildObjectiveGridRegions({ groups: value.analysis.analysis.groups, block: value.block, assignments: value.assignments,
      questions, optionCount: value.optionCount, orientation: value.orientation, reverse: value.reverse });
    if (output.error) { update({ error: output.error }); return; }
    const originalLayout = latestLayout.current;
    const ids = new Set(output.regions.map(region => region.question_id));
    const affected = originalLayout.pages.flatMap(page => page.question_regions.filter(region => ids.has(region.question_id)));
    const targetPage = originalLayout.pages.find(page => page.page_no === pageNo);
    const collisions = [...(targetPage?.question_regions.filter(region => !ids.has(region.question_id)) ?? []), ...(targetPage?.identity_regions ?? [])]
      .filter(region => output.regions.some(next => objectiveBoxesOverlap(region, next)));
    if (collisions.length) { update({ error: "新题组与范围外已有题区重叠，请调整范围或先核对旧框" }); return; }
    const commit = () => {
      // 覆盖确认弹窗可能晚于改题、换页或其他编辑；只有同一候选和草稿快照仍有效才允许整体提交。
      if (!active() || draftRef.current !== value || latestLayout.current !== originalLayout) {
        message.warning("当前草稿或候选已变化，请重新核对后应用"); return;
      }
      pendingApply.current = { identity, previousLayout: originalLayout, regionIDs: output.regions.map(region => region.id),
        cleanupBlocks: state.partial ? output.regions : [value.block!], pageNo, partial: state.partial };
      update({ applying: true });
      dispatchLayout({ type: "applyObjectiveGrid", pageNo, regions: output.regions, expectedLayout: originalLayout });
    };
    if (affected.length) modal.confirm({ title: "覆盖已有选择题区域？",
      content: `将替换第 ${affected.map(region => region.label ?? questions.find(q => q.id === region.question_id)?.question_no ?? "未知").join("、")} 题的原题区和选项框；其他题区保留。`,
      okText: "确认覆盖并应用", cancelText: "继续核对", onOk: commit });
    else commit();
  };
  // reducer 可能因并发草稿变化拒绝整批写入；只有新题区实际出现后才能删旧候选并显示成功。
  // biome-ignore lint/correctness/useExhaustiveDependencies: 只核对当前布局与待应用收据，操作方法经 ref 读取本次作用域。
  useEffect(() => {
    const pending = pendingApply.current;
    if (!pending || pending.identity !== identity || layout === pending.previousLayout) return;
    pendingApply.current = undefined;
    const accepted = layout.pages.find(page => page.page_no === pending.pageNo)?.question_regions
      .filter(region => pending.regionIDs.includes(region.id) && region.option_coordinate_space === "question_relative").length === pending.regionIDs.length;
    if (!accepted) { update({ applying: false, error: "草稿已变化，本批题区未应用，请重新核对" }); return; }
    // 部分应用只清理实测题组内部的旧候选，漏检位置的候选仍需保留给教师补框。
    for (const block of pending.cleanupBlocks) onAppliedRef.current?.(block, pending.pageNo);
    message.success(`已将 ${pending.regionIDs.length} 道选择题及选项框应用到草稿，${pending.partial ? "漏检题目请另行补框，" : ""}请保存并逐题核对`);
    close();
  }, [layout, identity]);
  return { ...draft, groups, partialDetection: detection.partial,
    validationError: detection.blocked ? "大区内存在裁断、题组冲突或不完整题组，请重新框选完整的小区后定位；本次不会补造选项" : selectionError ?? built.error,
    choiceQuestions: questions.filter(isObjectiveQuestion),
    openGrid: open, closeGrid: close, changeBlock, changeOptionCount, changeOrientation, changeReadOrder, changeReverse,
    changeQuestionIds, assignQuestion, setReviewed, setPartialReviewed, mapSelectedQuestions, analyze, recognizeNumberHints, apply };
}
