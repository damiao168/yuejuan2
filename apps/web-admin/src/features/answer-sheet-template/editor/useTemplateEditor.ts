import { useCallback, useEffect, useMemo, useReducer, type Dispatch, type RefObject, type SetStateAction } from "react";
import type { PDFDocumentProxy } from "pdfjs-dist";
import type { AnswerSheetTemplate, TemplateLayout } from "../../../api/configuration";
import type { PaperVersion, Question } from "../../../api/papers";
import type { TemplateLayoutAction } from "./templateLayoutReducer";
import { templateLayoutHistoryReducer } from "./templateLayoutHistory";
import { unresolvedTemplateMessage } from "./regionLifecycle";
import { useRegionEditor } from "./useRegionEditor";
import { useRegionSuggestion } from "./useRegionSuggestion";
import { useTemplateLifecycle } from "./useTemplateLifecycle";
import type { PreviewState } from "./useTemplatePreview";
import type { RemotePageAnalyzer } from "./analyzeTemplatePage";
import { useObjectiveGrid } from "./useObjectiveGrid";
import { containsObjectiveBox } from "./objectiveGridGeometry";
import { App } from "antd";
import { useFillGradingGroups } from "./useFillGradingGroups";

export function useTemplateEditor({
  examId, canManage, selectedPaper, selectedTemplate, questions, setTemplates,
  setSelectedTemplateId, onChanged, onActivate, pdfRef, preview, pageNo, setPageNo, analyzePage
}: {
  examId: string;
  canManage: boolean;
  selectedPaper?: PaperVersion;
  selectedTemplate?: AnswerSheetTemplate;
  questions: Question[];
  setTemplates: Dispatch<SetStateAction<AnswerSheetTemplate[]>>;
  setSelectedTemplateId: Dispatch<SetStateAction<string>>;
  onChanged?: () => void;
  onActivate?: (template: AnswerSheetTemplate) => Promise<unknown>;
  pdfRef: RefObject<PDFDocumentProxy | undefined>;
  preview: PreviewState;
  pageNo: number;
  setPageNo: Dispatch<SetStateAction<number>>;
  analyzePage?: RemotePageAnalyzer;
}) {
  const { message } = App.useApp();
  const [history, dispatchHistory] = useReducer(templateLayoutHistoryReducer,
    { past: [], present: { pages: [] } as TemplateLayout, future: [] });
  const layout = history.present;
  const dispatchLayout = useCallback((action: TemplateLayoutAction) => dispatchHistory(action), []);
  const setLayout = useCallback((value: SetStateAction<TemplateLayout>) => {
    dispatchLayout({ type: "set", value });
  }, []);
  const templateReadonly = !canManage || selectedTemplate?.status === "locked"
    || Boolean(selectedPaper && (preview.sourceId !== selectedPaper.id || preview.error));
  const coveredQuestions = useMemo(() => new Set(layout.pages.flatMap((page) =>
    [...page.question_regions.flatMap((region) => region.question_id ? [region.question_id] : []),
      ...(page.grading_groups ?? []).flatMap(group => group.question_ids)])), [layout]);
  // 身份框也占据页面几何范围；独立整区的成员参与覆盖统计，但不能冒充逐题几何框。
  const coveredRegions = useMemo(() => new Map(layout.pages.map(page => [page.page_no, [...page.question_regions, ...page.identity_regions]])), [layout]);
  const lifecycle = useTemplateLifecycle({
    examId, selectedPaper, selectedTemplate, questions, layout,
    coveredQuestions, readonly: templateReadonly, canManage, setTemplates,
    setSelectedTemplateId, onChanged, onActivate, preview
  });
  // 保存响应会用服务端模板重置编辑器；等待期间暂停输入，避免覆盖后续修改。
  const readonly = templateReadonly || lifecycle.saving;
  const suggestion = useRegionSuggestion({ pdfRef, questions, readonly, dispatchLayout,
    sourceKey: selectedPaper && selectedTemplate ? `${examId}:${selectedPaper.id}:${selectedPaper.file.hash_sha256}:${selectedTemplate.id}` : undefined,
    analysisRevision: lifecycle.revision, imageUrl: preview.imageUrl, coveredQuestionIDs: coveredQuestions, coveredRegions, analyzePage,
    editorLayout: layout, sourceHash: selectedPaper?.file.hash_sha256 });
  // 当前预览翻页只暂停拖框，不取消独立遍历全部页的识别任务。
  const editingReadonly = readonly || preview.loading;
  const region = useRegionEditor({
    layout, dispatchLayout, pageNo, setPageNo, questions, selectedTemplate, readonly: editingReadonly
  });
  const fillGrading = useFillGradingGroups({ scopeKey: `${selectedPaper?.id}:${selectedPaper?.file.hash_sha256}:${selectedTemplate?.id}`,
    layout, suggestions: suggestion.fillGradingGroups, selectedRegion: region.selectedRegion,
    selectedQuestionId: region.selectedQuestionId, pageNo, questions,
    readonly: editingReadonly, dispatchLayout });
  const objectiveGrid = useObjectiveGrid({ selectedTemplate, revision: lifecycle.revision, selectedPaper, pageNo,
    imageURL: preview.imageUrl, readonly: editingReadonly, layout, questions, pdfRef, dispatchLayout,
    onApplied: (block, appliedPageNo) => {
      for (const candidate of suggestion.unassignedRegions.get(appliedPageNo) ?? []) {
        if (containsObjectiveBox(block, candidate)) suggestion.discardSuggestedRegion(candidate.id);
      }
      for (const candidate of suggestion.objectiveGroups.get(appliedPageNo) ?? []) {
        if (containsObjectiveBox(block, candidate)) suggestion.discardObjectiveGroup(candidate.id);
      }
    } });
  const optionalGrid: { objectiveGrid?: ReturnType<typeof useObjectiveGrid> } = { objectiveGrid };

  useEffect(() => {
    if (!selectedTemplate) return;
    // 编辑器持有独立草稿，不能让区域拖动直接修改已加载模板或其查询缓存。
    const loaded = structuredClone(selectedTemplate.layout);
    if (selectedTemplate.status === "draft" && selectedPaper?.file.hash_sha256 && !loaded.editor_state) loaded.editor_state = {
      version: 1, source_sha256: selectedPaper.file.hash_sha256, candidates: [] };
    dispatchHistory({ type: "resetEditor", layout: loaded });
    setPageNo(1);
  }, [selectedTemplate, setLayout, setPageNo]);

  useEffect(() => {
    if (!canManage || selectedTemplate?.status !== "draft" || !selectedPaper
      || selectedTemplate.exam_paper_id !== selectedPaper.id || preview.sourceId !== selectedPaper.id
      || preview.loading || preview.error || !preview.pageCount || lifecycle.saving || suggestion.suggestingRegions) return;
    // 来源页数决定正反面是否存在；历史草稿漏页时只补空页，保留原页人工区域和各页实际尺寸。
    setLayout(current => {
      if (!current.pages.length) return current;
      const existingPages = new Set(current.pages.map(page => page.page_no));
      const missing = Array.from({ length: preview.pageCount }, (_, index) => index + 1).filter(page => !existingPages.has(page));
      if (!missing.length) return current;
      return { ...current, pages: [...current.pages, ...missing.map(pageNo => ({
        page_no: pageNo, width: preview.pageSizes?.[pageNo - 1]?.width ?? preview.width,
        height: preview.pageSizes?.[pageNo - 1]?.height ?? preview.height,
        registration_marks: [], identity_regions: [], question_regions: []
      }))].sort((left, right) => left.page_no - right.page_no) };
    });
  }, [canManage, selectedTemplate, selectedPaper, preview.sourceId, preview.pageCount, preview.pageSizes,
    preview.width, preview.height, preview.loading, preview.error, lifecycle.saving, suggestion.suggestingRegions, setLayout]);

  const setOMRProfile = (mode: "manual_only" | "template_difference") => {
    if (!readonly) dispatchLayout({ type: "setOMRProfile", mode });
  };
  const reviewMessage = unresolvedTemplateMessage(layout);
  const guardReview = () => {
    if (!reviewMessage) return true;
    message.warning(reviewMessage); return false;
  };

  return {
    ...region, ...suggestion, ...lifecycle, ...optionalGrid, ...fillGrading, coveredQuestions,
    layout, setLayout, pageNo, setPageNo, readonly: editingReadonly, setOMRProfile,
    saveDraft: lifecycle.saveDraft,
    reviewMessage,
    confirmRegion: (regionId: string) => { if (!editingReadonly) dispatchLayout({ type: "confirmRegion", regionId }); },
    confirmPageRegions: () => { if (!editingReadonly) dispatchLayout({ type: "confirmPageRegions", pageNo }); },
    canUndo: history.past.length > 0, canRedo: history.future.length > 0,
    undo: () => { if (!editingReadonly) dispatchHistory({ type: "undoEditor" }); },
    redo: () => { if (!editingReadonly) dispatchHistory({ type: "redoEditor" }); },
    confirmLock: () => { if (guardReview()) lifecycle.confirmLock(); },
    confirmActivate: () => { if (guardReview()) lifecycle.confirmActivate(); }
  };
}
