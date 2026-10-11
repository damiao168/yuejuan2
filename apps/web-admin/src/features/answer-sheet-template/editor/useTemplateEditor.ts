import { useCallback, useEffect, useMemo, useReducer, type Dispatch, type RefObject, type SetStateAction } from "react";
import type { PDFDocumentProxy } from "pdfjs-dist";
import type { AnswerSheetTemplate, TemplateLayout } from "../../../api/configuration";
import type { PaperVersion, Question } from "../../../api/papers";
import { templateLayoutReducer } from "./templateLayoutReducer";
import { useRegionEditor } from "./useRegionEditor";
import { useRegionSuggestion } from "./useRegionSuggestion";
import { useTemplateLifecycle } from "./useTemplateLifecycle";
import type { PreviewState } from "./useTemplatePreview";

export function useTemplateEditor({
  examId, canManage, selectedPaper, selectedTemplate, questions, setTemplates,
  setSelectedTemplateId, onChanged, onActivate, pdfRef, preview, pageNo, setPageNo
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
}) {
  const [layout, dispatchLayout] = useReducer(templateLayoutReducer, { pages: [] } as TemplateLayout);
  const setLayout = useCallback((value: SetStateAction<TemplateLayout>) => {
    dispatchLayout({ type: "set", value });
  }, []);
  const templateReadonly = !canManage || selectedTemplate?.status === "locked";
  const coveredQuestions = useMemo(() => new Set(layout.pages.flatMap((page) =>
    page.question_regions.map((region) => region.question_id))), [layout]);
  const lifecycle = useTemplateLifecycle({
    examId, selectedPaper, selectedTemplate, questions, layout,
    coveredQuestions, readonly: templateReadonly, setTemplates,
    setSelectedTemplateId, onChanged, onActivate, preview
  });
  // 保存响应会用服务端模板重置编辑器；等待期间暂停输入，避免覆盖后续修改。
  const readonly = templateReadonly || lifecycle.saving;
  const region = useRegionEditor({
    layout, dispatchLayout, pageNo, setPageNo, questions, selectedTemplate, readonly
  });
  const suggestion = useRegionSuggestion({ pdfRef, questions, readonly, dispatchLayout });

  useEffect(() => {
    if (!selectedTemplate) return;
    // 编辑器持有独立草稿，不能让区域拖动直接修改已加载模板或其查询缓存。
    setLayout(structuredClone(selectedTemplate.layout));
    setPageNo(1);
  }, [selectedTemplate, setLayout, setPageNo]);

  const setOMRProfile = (mode: "manual_only" | "template_difference") => {
    if (!readonly) dispatchLayout({ type: "setOMRProfile", mode });
  };

  return {
    ...region, ...suggestion, ...lifecycle,
    layout, setLayout, pageNo, setPageNo, readonly, setOMRProfile
  };
}
