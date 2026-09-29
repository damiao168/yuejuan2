import { useEffect, useMemo, useRef, useState, type Dispatch, type PointerEvent as ReactPointerEvent, type SetStateAction } from "react";
import { App } from "antd";
import type { AnswerSheetTemplate, LayoutRegion, OptionRegion, TemplateLayout } from "../../../api/configuration";
import type { Question } from "../../../api/papers";
import type { TemplateLayoutAction } from "./templateLayoutReducer";

type Interaction =
  | { kind: "draw"; startX: number; startY: number; x: number; y: number }
  | { kind: "move" | "resize"; regionId: string; startX: number; startY: number; original: LayoutRegion };

function clamp(value: number, min = 0, max = 1) { return Math.min(max, Math.max(min, value)); }
function roundCoordinate(value: number) { return Number(value.toFixed(6)); }

export function useRegionEditor({
  layout, dispatchLayout, pageNo, setPageNo, questions, selectedTemplate, readonly
}: {
  layout: TemplateLayout;
  dispatchLayout: Dispatch<TemplateLayoutAction>;
  pageNo: number;
  setPageNo: Dispatch<SetStateAction<number>>;
  questions: Question[];
  selectedTemplate?: AnswerSheetTemplate;
  readonly: boolean;
}) {
  const { message } = App.useApp();
  const canvasRef = useRef<HTMLDivElement>(null);
  const [selectedQuestionId, setSelectedQuestionId] = useState("");
  const [zoom, setZoom] = useState(1);
  const [interaction, setInteraction] = useState<Interaction>();
  const [selectedRegionId, setSelectedRegionId] = useState("");
  const previousTemplateRef = useRef(selectedTemplate);

  useEffect(() => {
    setSelectedQuestionId((current) => questions.some((question) => question.id === current)
      ? current : questions[0]?.id || "");
  }, [questions]);
  useEffect(() => {
    if (previousTemplateRef.current === selectedTemplate) return;
    previousTemplateRef.current = selectedTemplate;
    setSelectedRegionId("");
  }, [selectedTemplate]);

  const currentPage = layout.pages.find((item) => item.page_no === pageNo);
  const selectedRegion = currentPage?.question_regions.find((item) => item.id === selectedRegionId);
  const selectedQuestion = questions.find((item) => item.id === selectedRegion?.question_id);
  const coveredQuestions = useMemo(() => new Set(layout.pages.flatMap((page) =>
    page.question_regions.map((region) => region.question_id))), [layout]);
  const draftRect = interaction?.kind === "draw" ? {
    x: Math.min(interaction.startX, interaction.x),
    y: Math.min(interaction.startY, interaction.y),
    width: Math.abs(interaction.x - interaction.startX),
    height: Math.abs(interaction.y - interaction.startY)
  } : undefined;

  const point = (event: ReactPointerEvent) => {
    // 用当前显示矩形换算 0～1 页面比例，缩放预览不会改变最终保存坐标。
    const bounds = canvasRef.current?.getBoundingClientRect();
    if (!bounds) return { x: 0, y: 0 };
    return { x: clamp((event.clientX - bounds.left) / bounds.width),
      y: clamp((event.clientY - bounds.top) / bounds.height) };
  };
  const updateRegion = (regionId: string, patch: Partial<LayoutRegion>) =>
    dispatchLayout({ type: "updateRegion", regionId, patch });
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
    if (interaction.kind === "draw") {
      setInteraction({ ...interaction, x: position.x, y: position.y });
      return;
    }
    const dx = position.x - interaction.startX;
    const dy = position.y - interaction.startY;
    // 每次移动相对按下时的原始框计算，避免连续事件累加位移造成漂移。
    updateRegion(interaction.regionId, interaction.kind === "move"
      ? { x: clamp(interaction.original.x + dx, 0, 1 - interaction.original.width),
          y: clamp(interaction.original.y + dy, 0, 1 - interaction.original.height) }
      : { width: clamp(interaction.original.width + dx, 0.01, 1 - interaction.original.x),
          height: clamp(interaction.original.height + dy, 0.01, 1 - interaction.original.y) });
  };
  const onCanvasPointerUp = () => {
    if (interaction?.kind === "draw" && currentPage && selectedQuestionId) {
      const x = Math.min(interaction.startX, interaction.x);
      const y = Math.min(interaction.startY, interaction.y);
      const width = Math.abs(interaction.x - interaction.startX);
      const height = Math.abs(interaction.y - interaction.startY);
      if (width >= 0.01 && height >= 0.01) {
        const question = questions.find((item) => item.id === selectedQuestionId);
        const region: LayoutRegion = {
          id: crypto.randomUUID(), question_id: selectedQuestionId,
          label: question?.question_no || "题目", x: roundCoordinate(x),
          y: roundCoordinate(y), width: roundCoordinate(width),
          height: roundCoordinate(height), option_regions: []
        };
        dispatchLayout({ type: "replaceQuestionRegion", questionId: selectedQuestionId, pageNo, region });
        setSelectedRegionId(region.id);
      }
    }
    setInteraction(undefined);
  };
  const createRegionWithoutDragging = () => {
    if (readonly || !currentPage || !selectedQuestionId) return;
    const existing = layout.pages.flatMap((page) => page.question_regions)
      .find((region) => region.question_id === selectedQuestionId);
    if (existing) {
      const page = layout.pages.find((item) => item.question_regions.some((region) => region.id === existing.id));
      if (page) setPageNo(page.page_no);
      setSelectedRegionId(existing.id);
      return;
    }
    const region: LayoutRegion = {
      id: crypto.randomUUID(), question_id: selectedQuestionId,
      label: questions.find((question) => question.id === selectedQuestionId)?.question_no ?? "题目",
      x: .1, y: .1, width: .8, height: .2, option_regions: []
    };
    dispatchLayout({ type: "addRegion", pageNo, region });
    setSelectedRegionId(region.id);
  };
  const removeRegion = (regionId: string) => {
    dispatchLayout({ type: "removeRegion", regionId });
    setSelectedRegionId("");
  };
  const updateOption = (optionId: string, patch: Partial<OptionRegion>) => {
    if (selectedRegionId) dispatchLayout({ type: "updateOption", regionId: selectedRegionId, optionId, patch });
  };
  const addOptionRegion = () => {
    if (!selectedRegionId || !selectedRegion) return;
    const existing = selectedRegion.option_regions ?? [];
    if (existing.length >= 12) { message.warning("每题最多配置 12 个选项区域"); return; }
    const index = existing.length;
    const option: OptionRegion = {
      id: crypto.randomUUID(), label: String.fromCharCode(65 + index),
      x: Math.min(.84, .04 + index * .16), y: .25, width: .12, height: .5
    };
    dispatchLayout({ type: "addOption", regionId: selectedRegionId, option });
  };
  const removeOptionRegion = (optionId: string) => {
    if (selectedRegionId) dispatchLayout({ type: "removeOption", regionId: selectedRegionId, optionId });
  };

  return {
    canvasRef, selectedQuestionId, setSelectedQuestionId, zoom, setZoom,
    selectedRegionId, setSelectedRegionId, currentPage, selectedRegion,
    selectedQuestion, coveredQuestions, draftRect, onCanvasPointerDown,
    beginRegionInteraction, onCanvasPointerMove, onCanvasPointerUp, updateRegion,
    createRegionWithoutDragging, removeRegion, updateOption, addOptionRegion,
    removeOptionRegion
  };
}
