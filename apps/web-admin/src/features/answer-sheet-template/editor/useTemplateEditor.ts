import { useEffect, useMemo, useRef, useState, type Dispatch, type MutableRefObject, type PointerEvent as ReactPointerEvent, type SetStateAction } from "react";
import { App } from "antd";
import type { PDFDocumentProxy } from "pdfjs-dist";
import { getUserErrorMessage } from "../../../api/client";
import {
  cloneAnswerSheetTemplate,
  createAnswerSheetTemplate,
  lockAnswerSheetTemplate,
  type AnswerSheetTemplate,
  type LayoutRegion,
  type OptionRegion,
  type TemplateLayout
} from "../../../api/configuration";
import type { PaperVersion, Question } from "../../../api/papers";
import { saveTemplateDraft } from "./saveTemplateDraft";
import type { PreviewState } from "./useTemplatePreview";

type Interaction =
  | { kind: "draw"; startX: number; startY: number; x: number; y: number }
  | { kind: "move" | "resize"; regionId: string; startX: number; startY: number; original: LayoutRegion };

function clamp(value: number, min = 0, max = 1) { return Math.min(max, Math.max(min, value)); }
function roundCoordinate(value: number) { return Number(value.toFixed(6)); }
function emptyLayout(pageCount: number, width: number, height: number): TemplateLayout {
  return {
    omr_profile: { mode: "manual_only", version: "opencv-fill-v1" },
    pages: Array.from({ length: pageCount }, (_, index) => ({
      page_no: index + 1,
      width,
      height,
      registration_marks: [],
      identity_regions: [],
      question_regions: []
    }))
  };
}

export function useTemplateEditor({
  examId,
  canManage,
  selectedPaper,
  selectedTemplate,
  questions,
  setTemplates,
  setSelectedTemplateId,
  loadData,
  onChanged,
  pdfRef,
  preview,
  pageNo,
  setPageNo
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
  const { message, modal } = App.useApp();
  const canvasRef = useRef<HTMLDivElement>(null);
  const [selectedQuestionId, setSelectedQuestionId] = useState("");
  const [name, setName] = useState("答题卡模板");
  const [layout, setLayout] = useState<TemplateLayout>({ pages: [] });
  const [revision, setRevision] = useState(0);
  const [zoom, setZoom] = useState(1);
  const [interaction, setInteraction] = useState<Interaction>();
  const [selectedRegionId, setSelectedRegionId] = useState("");
  const [saving, setSaving] = useState(false);
  const [suggestingRegions, setSuggestingRegions] = useState(false);

  useEffect(() => {
    setSelectedQuestionId((current) => questions.some((question) => question.id === current)
      ? current
      : questions[0]?.id || "");
  }, [questions]);

  useEffect(() => {
    if (!selectedTemplate) return;
    setName(selectedTemplate.name);
    setLayout(structuredClone(selectedTemplate.layout));
    setRevision(selectedTemplate.revision);
    setPageNo(1);
    setSelectedRegionId("");
  }, [selectedTemplate]);

  const currentPage = layout.pages.find((item) => item.page_no === pageNo);
  const selectedRegion = currentPage?.question_regions.find((item) => item.id === selectedRegionId);
  const selectedQuestion = questions.find((item) => item.id === selectedRegion?.question_id);
  const readonly = !canManage || selectedTemplate?.status === "locked";
  const coveredQuestions = useMemo(() => new Set(layout.pages.flatMap((page) => page.question_regions.map((region) => region.question_id))), [layout]);
  const draftRect = interaction?.kind === "draw" ? {
    x: Math.min(interaction.startX, interaction.x),
    y: Math.min(interaction.startY, interaction.y),
    width: Math.abs(interaction.x - interaction.startX),
    height: Math.abs(interaction.y - interaction.startY)
  } : undefined;

  const point = (event: ReactPointerEvent) => {
    const bounds = canvasRef.current?.getBoundingClientRect();
    if (!bounds) return { x: 0, y: 0 };
    return { x: clamp((event.clientX - bounds.left) / bounds.width), y: clamp((event.clientY - bounds.top) / bounds.height) };
  };
  const updateRegion = (regionId: string, patch: Partial<LayoutRegion>) => {
    setLayout((current) => ({ pages: current.pages.map((page) => ({ ...page, question_regions: page.question_regions.map((region) => region.id === regionId ? { ...region, ...patch } : region) })) }));
  };
  const onCanvasPointerDown = (event: ReactPointerEvent<HTMLDivElement>) => {
    if (readonly || !selectedQuestionId || !currentPage) return;
    const position = point(event);
    event.currentTarget.setPointerCapture(event.pointerId);
    setInteraction({ kind: "draw", startX: position.x, startY: position.y, x: position.x, y: position.y });
    setSelectedRegionId("");
  };
  const beginRegionInteraction = (event: ReactPointerEvent, region: LayoutRegion, kind: "move" | "resize") => {
    if (readonly) return;
    event.stopPropagation();
    const position = point(event);
    canvasRef.current?.setPointerCapture(event.pointerId);
    setSelectedRegionId(region.id);
    setInteraction({ kind, regionId: region.id, startX: position.x, startY: position.y, original: { ...region } });
  };
  const onCanvasPointerMove = (event: ReactPointerEvent<HTMLDivElement>) => {
    if (!interaction || !currentPage) return;
    const position = point(event);
    if (interaction.kind === "draw") { setInteraction({ ...interaction, x: position.x, y: position.y }); return; }
    const dx = position.x - interaction.startX;
    const dy = position.y - interaction.startY;
    updateRegion(interaction.regionId, interaction.kind === "move"
      ? { x: clamp(interaction.original.x + dx, 0, 1 - interaction.original.width), y: clamp(interaction.original.y + dy, 0, 1 - interaction.original.height) }
      : { width: clamp(interaction.original.width + dx, 0.01, 1 - interaction.original.x), height: clamp(interaction.original.height + dy, 0.01, 1 - interaction.original.y) });
  };
  const onCanvasPointerUp = () => {
    if (interaction?.kind === "draw" && currentPage && selectedQuestionId) {
      const x = Math.min(interaction.startX, interaction.x);
      const y = Math.min(interaction.startY, interaction.y);
      const width = Math.abs(interaction.x - interaction.startX);
      const height = Math.abs(interaction.y - interaction.startY);
      if (width >= 0.01 && height >= 0.01) {
        const question = questions.find((item) => item.id === selectedQuestionId);
        const region: LayoutRegion = { id: crypto.randomUUID(), question_id: selectedQuestionId, label: question?.question_no || "题目", x: roundCoordinate(x), y: roundCoordinate(y), width: roundCoordinate(width), height: roundCoordinate(height), option_regions: [] };
        setLayout((current) => ({ pages: current.pages.map((page) => ({ ...page, question_regions: page.question_regions.filter((item) => item.question_id !== selectedQuestionId).concat(page.page_no === pageNo ? [region] : []) })) }));
        setSelectedRegionId(region.id);
      }
    }
    setInteraction(undefined);
  };
  const createRegionWithoutDragging = () => {
    if (readonly || !currentPage || !selectedQuestionId) return;
    const existing = layout.pages.flatMap((page) => page.question_regions).find((region) => region.question_id === selectedQuestionId);
    if (existing) {
      const page = layout.pages.find((item) => item.question_regions.some((region) => region.id === existing.id));
      if (page) setPageNo(page.page_no);
      setSelectedRegionId(existing.id);
      return;
    }
    const region: LayoutRegion = { id: crypto.randomUUID(), question_id: selectedQuestionId, label: questions.find((question) => question.id === selectedQuestionId)?.question_no ?? "题目", x: .1, y: .1, width: .8, height: .2, option_regions: [] };
    setLayout((current) => ({ ...current, pages: current.pages.map((page) => page.page_no === pageNo ? { ...page, question_regions: [...page.question_regions, region] } : page) }));
    setSelectedRegionId(region.id);
  };
  const removeRegion = (regionId: string) => {
    setLayout((current) => ({ pages: current.pages.map((page) => ({ ...page, question_regions: page.question_regions.filter((region) => region.id !== regionId) })) }));
    setSelectedRegionId("");
  };
  const updateOption = (optionId: string, patch: Partial<OptionRegion>) => {
    if (!selectedRegionId) return;
    setLayout((current) => ({ pages: current.pages.map((page) => ({ ...page, question_regions: page.question_regions.map((region) => region.id === selectedRegionId ? { ...region, option_regions: (region.option_regions ?? []).map((option) => option.id === optionId ? { ...option, ...patch } : option) } : region) })) }));
  };
  const addOptionRegion = () => {
    if (!selectedRegionId || !selectedRegion) return;
    const existing = selectedRegion.option_regions ?? [];
    if (existing.length >= 12) { message.warning("每题最多配置 12 个选项区域"); return; }
    const index = existing.length;
    const option: OptionRegion = { id: crypto.randomUUID(), label: String.fromCharCode(65 + index), x: Math.min(.84, .04 + index * .16), y: .25, width: .12, height: .5 };
    updateRegion(selectedRegionId, { option_regions: existing.concat(option) });
  };
  const removeOptionRegion = (optionId: string) => {
    if (!selectedRegionId || !selectedRegion) return;
    updateRegion(selectedRegionId, { option_regions: (selectedRegion.option_regions ?? []).filter((option) => option.id !== optionId) });
  };

  const suggestQuestionRegions = async () => {
    const pdf = pdfRef.current;
    if (!pdf || readonly || !questions.length) { message.warning("当前文件没有可分析的 PDF 页面，或模板已锁定"); return; }
    setSuggestingRegions(true);
    try {
      const anchors = new Map<number, Array<{ question: Question; top: number }>>();
      const unmatched = new Set(questions.map((question) => question.id));
      const normalized = questions.map((question) => ({ question, key: question.question_no.replace(/\s+/g, "").toLowerCase() })).sort((a, b) => b.key.length - a.key.length);
      for (let pageIndex = 1; pageIndex <= pdf.numPages; pageIndex += 1) {
        const page = await pdf.getPage(pageIndex);
        const viewport = page.getViewport({ scale: 1 });
        const content = await page.getTextContent();
        const pageAnchors: Array<{ question: Question; top: number }> = [];
        for (const raw of content.items) {
          if (!("str" in raw) || !("transform" in raw)) continue;
          const text = String(raw.str).replace(/\s+/g, "").toLowerCase();
          const candidate = normalized.find(({ question, key }) => unmatched.has(question.id) && (text === key || text.startsWith(`${key}.`) || text.startsWith(`${key}、`) || text.startsWith(`${key}．`)));
          if (!candidate) continue;
          const transform = raw.transform as number[];
          pageAnchors.push({ question: candidate.question, top: clamp(1 - ((transform[5] || 0) + Math.abs(transform[3] || transform[0] || 12)) / viewport.height) });
          unmatched.delete(candidate.question.id);
        }
        pageAnchors.sort((a, b) => a.top - b.top);
        if (pageAnchors.length) anchors.set(pageIndex, pageAnchors);
      }
      if (!anchors.size) { message.warning("没有从 PDF 文字层识别到题号；扫描版请先完成 OCR 后再生成候选区域"); return; }
      setLayout((current) => ({ ...current, pages: current.pages.map((page) => {
        const pageAnchors = anchors.get(page.page_no) ?? [];
        if (!pageAnchors.length) return page;
        const matchedIDs = new Set(pageAnchors.map(({ question }) => question.id));
        const suggestions = pageAnchors.map(({ question, top }, index) => {
          const y = clamp(top - .01, .02, .94);
          return { id: crypto.randomUUID(), question_id: question.id, label: question.question_no, x: .04, y: roundCoordinate(y), width: .92, height: roundCoordinate(clamp((pageAnchors[index + 1]?.top ?? .96) - y - .012, .04, .42)), option_regions: [], suggestion_confidence: .72, suggestion_source: "pdf_text_anchor" as const };
        });
        return { ...page, question_regions: page.question_regions.filter((region) => !region.question_id || !matchedIDs.has(region.question_id)).concat(suggestions) };
      }) }));
      message.success(`已生成 ${questions.length - unmatched.size} 道题的候选区域；请逐题核对并调整${unmatched.size ? `，另有 ${unmatched.size} 道题未识别` : ""}`);
    } catch (error) { message.error(getUserErrorMessage(error, "操作失败，请稍后重试")); }
    finally { setSuggestingRegions(false); }
  };

  const setOMRProfile = (mode: "manual_only" | "template_difference") => setLayout((current) => ({ ...current, omr_profile: mode === "template_difference" ? { mode, version: "opencv-template-difference-bubble-v1" } : { mode, version: "opencv-fill-v1" } }));
  const createDraft = async () => {
    if (!selectedPaper) { message.error("请先上传并选择试卷版本"); return; }
    setSaving(true);
    try {
      const response = await createAnswerSheetTemplate(examId, { exam_paper_id: selectedPaper.id, name: `${selectedPaper.file.original_name || "试卷"}答题卡模板`, page_count: preview.pageCount || 1, layout: emptyLayout(preview.pageCount || 1, preview.width, preview.height) });
      setTemplates((current) => [response.template, ...current]);
      setSelectedTemplateId(response.template.id);
      onChanged?.();
      message.success("模板草稿已创建，可以开始框选题目区域");
    } catch (error) { message.error(getUserErrorMessage(error, "操作失败，请稍后重试")); }
    finally { setSaving(false); }
  };
  const saveDraft = async () => {
    if (!selectedTemplate || readonly) return;
    setSaving(true);
    try {
      const result = await saveTemplateDraft({ templateId: selectedTemplate.id, payload: { exam_paper_id: selectedTemplate.exam_paper_id, name, page_count: layout.pages.length, layout }, expectedRevision: revision });
      if (result.status === "saved") {
        setTemplates((current) => current.map((item) => item.id === result.template.id ? result.template : item));
        setRevision(result.template.revision);
        onChanged?.();
        message.success("模板已保存");
      } else {
        message.error(getUserErrorMessage(result.error, "操作失败，请稍后重试"));
        if (result.status === "revision_conflict") void loadData();
      }
    } finally { setSaving(false); }
  };
  const confirmLock = () => {
    if (!selectedTemplate || readonly) return;
    const missing = questions.filter((question) => !coveredQuestions.has(question.id));
    if (missing.length) { message.error(`还有 ${missing.length} 道题未配置区域`); return; }
    modal.confirm({ title: "锁定答题卡模板", content: "锁定后不能原地修改；如需调整必须克隆新版本。", okText: "确认锁定", cancelText: "取消", onOk: async () => {
      const response = await lockAnswerSheetTemplate(selectedTemplate.id);
      setTemplates((current) => current.map((item) => item.id === response.template.id ? response.template : item));
      onChanged?.();
      message.success("模板已锁定");
    }});
  };
  const cloneTemplate = async () => {
    if (!selectedTemplate) return;
    setSaving(true);
    try {
      const response = await cloneAnswerSheetTemplate(selectedTemplate.id);
      setTemplates((current) => [response.template, ...current]);
      setSelectedTemplateId(response.template.id);
      onChanged?.();
      message.success("已创建可编辑的新版本");
    } catch (error) { message.error(getUserErrorMessage(error, "操作失败，请稍后重试")); }
    finally { setSaving(false); }
  };

  return {
    canvasRef, selectedQuestionId, setSelectedQuestionId, name, setName, layout, setLayout,
    revision, pageNo, setPageNo, zoom, setZoom, selectedRegionId,
    setSelectedRegionId, saving, suggestingRegions, currentPage, selectedRegion,
    selectedQuestion, readonly, coveredQuestions, draftRect, onCanvasPointerDown,
    beginRegionInteraction, onCanvasPointerMove, onCanvasPointerUp, updateRegion,
    createRegionWithoutDragging, removeRegion, updateOption, addOptionRegion,
    removeOptionRegion, suggestQuestionRegions, setOMRProfile, createDraft,
    saveDraft, confirmLock, cloneTemplate
  };
}
