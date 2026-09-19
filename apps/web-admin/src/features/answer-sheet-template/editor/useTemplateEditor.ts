import { useCallback, useEffect, useReducer, type Dispatch, type MutableRefObject, type SetStateAction } from "react";
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
  setSelectedTemplateId, loadData, onChanged, pdfRef, preview, pageNo, setPageNo
}: {
  examId: string;
  canManage: boolean;
  selectedPaper?: PaperVersion;
  selectedTemplate?: AnswerSheetTemplate;
  questions: Question[];
  setTemplates: Dispatch<SetStateAction<AnswerSheetTemplate[]>>;
  setSelectedTemplateId: Dispatch<SetStateAction<string>>;
  loadData: () => Promise<void>;
  onChanged?: () => void;
  pdfRef: MutableRefObject<PDFDocumentProxy | undefined>;
  preview: PreviewState;
  pageNo: number;
  setPageNo: Dispatch<SetStateAction<number>>;
}) {
  const [layout, dispatchLayout] = useReducer(templateLayoutReducer, { pages: [] } as TemplateLayout);
  const setLayout = useCallback((value: SetStateAction<TemplateLayout>) => {
    dispatchLayout({ type: "set", value });
  }, []);
  const readonly = !canManage || selectedTemplate?.status === "locked";
  const region = useRegionEditor({
    layout, dispatchLayout, pageNo, setPageNo, questions, selectedTemplate, readonly
  });
  const suggestion = useRegionSuggestion({ pdfRef, questions, readonly, dispatchLayout });
  const lifecycle = useTemplateLifecycle({
    examId, selectedPaper, selectedTemplate, questions, layout,
    coveredQuestions: region.coveredQuestions, readonly, setTemplates,
    setSelectedTemplateId, loadData, onChanged, preview
  });

  useEffect(() => {
    if (!selectedTemplate) return;
    setLayout(structuredClone(selectedTemplate.layout));
    setPageNo(1);
  }, [selectedTemplate, setLayout, setPageNo]);

  const setOMRProfile = (mode: "manual_only" | "template_difference") =>
    dispatchLayout({ type: "setOMRProfile", mode });

  return {
    ...region, ...suggestion, ...lifecycle,
    layout, setLayout, pageNo, setPageNo, readonly, setOMRProfile
  };
}
