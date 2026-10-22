import { useCallback, useEffect, useMemo, useRef, useState, type Dispatch, type PointerEvent as ReactPointerEvent, type SetStateAction } from "react";
import { App } from "antd";
import type { AnswerSheetTemplate, LayoutRegion, OptionRegion, TemplateLayout } from "../../../api/configuration";
import type { Question } from "../../../api/papers";
import type { TemplateLayoutAction } from "./templateLayoutReducer";
import { normalizeIdentityRegionLabel } from "./identityRegionFields";

export type RegionResizeHandle = "nw" | "n" | "ne" | "e" | "se" | "s" | "sw" | "w";
type Interaction =
  | { kind: "draw"; startX: number; startY: number; x: number; y: number }
  | { kind: "move" | "resize"; regionId: string; startX: number; startY: number; original: LayoutRegion; handle?: RegionResizeHandle };

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
  const capturedPointerRef = useRef<number | undefined>(undefined);
  const releasePointer = useCallback(() => {
    const pointerId = capturedPointerRef.current;
    capturedPointerRef.current = undefined;
    if (pointerId === undefined) return;
    const canvas = canvasRef.current;
    if (canvas?.hasPointerCapture?.(pointerId)) canvas.releasePointerCapture(pointerId);
  }, []);

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
  const selectedRegion = [...(currentPage?.question_regions ?? []), ...(currentPage?.identity_regions ?? [])]
    .find((item) => item.id === selectedRegionId);
  const selectedRegionKind = selectedRegion
    ? currentPage?.identity_regions.some(region => region.id === selectedRegion.id) ? "identity" as const : "question" as const : undefined;
  const selectedQuestion = questions.find((item) => item.id === selectedRegion?.question_id);
  const coveredQuestions = useMemo(() => new Set(layout.pages.flatMap((page) =>
    page.question_regions.map((region) => region.question_id))), [layout]);
  const draftRect = interaction?.kind === "draw" ? {
    x: Math.min(interaction.startX, interaction.x),
    y: Math.min(interaction.startY, interaction.y),
    width: Math.abs(interaction.x - interaction.startX),
    height: Math.abs(interaction.y - interaction.startY)
  } : undefined;

  // 保存或锁定后取消尚未结束的拖动，松开指针不能再补写旧区域。
  useEffect(() => { if (readonly) { setInteraction(undefined); releasePointer(); } }, [readonly, releasePointer]);
  const interactionScope = `${selectedTemplate?.id ?? ""}:${pageNo}`;
  const previousInteractionScope = useRef(interactionScope);
  useEffect(() => {
    // 翻页或切换模板后，旧指针不能继续修改另一页；已完成的草稿区域仍由 layout 持有。
    if (previousInteractionScope.current === interactionScope) return;
    previousInteractionScope.current = interactionScope;
    setInteraction(undefined); releasePointer();
  }, [interactionScope, releasePointer]);
  useEffect(() => () => releasePointer(), [releasePointer]);

  const point = (event: ReactPointerEvent) => {
    // 用当前显示矩形换算 0～1 页面比例，缩放预览不会改变最终保存坐标。
    const bounds = canvasRef.current?.getBoundingClientRect();
    if (!bounds) return { x: 0, y: 0 };
    return { x: clamp((event.clientX - bounds.left) / bounds.width),
      y: clamp((event.clientY - bounds.top) / bounds.height) };
  };
  const updateRegion = (regionId: string, patch: Partial<LayoutRegion>) => {
    if (!readonly) dispatchLayout({ type: "updateRegion", regionId, patch });
  };
  const onCanvasPointerDown = (event: ReactPointerEvent<HTMLDivElement>) => {
    if (readonly || (event.button != null && event.button !== 0) || !selectedQuestionId || !currentPage) return;
    const position = point(event);
    event.currentTarget.setPointerCapture(event.pointerId);
    capturedPointerRef.current = event.pointerId;
    setInteraction({ kind: "draw", startX: position.x, startY: position.y, x: position.x, y: position.y });
    setSelectedRegionId("");
  };
  const beginRegionInteraction = (event: ReactPointerEvent, region: LayoutRegion, kind: "move" | "resize", handle: RegionResizeHandle = "se") => {
    if (readonly || (event.button != null && event.button !== 0)) return;
    event.preventDefault();
    event.stopPropagation();
    const position = point(event);
    canvasRef.current?.setPointerCapture(event.pointerId);
    capturedPointerRef.current = event.pointerId;
    setSelectedRegionId(region.id);
    if (region.question_id) setSelectedQuestionId(region.question_id);
    setInteraction({ kind, handle, regionId: region.id, startX: position.x, startY: position.y, original: { ...region } });
  };
  const onCanvasPointerMove = (event: ReactPointerEvent<HTMLDivElement>) => {
    if (readonly || !interaction || !currentPage) return;
    const position = point(event);
    if (interaction.kind === "draw") {
      setInteraction({ ...interaction, x: position.x, y: position.y });
      return;
    }
    const dx = position.x - interaction.startX;
    const dy = position.y - interaction.startY;
    // 每次移动相对按下时的原始框计算，避免连续事件累加位移造成漂移。
    if (interaction.kind === "move") {
      updateRegion(interaction.regionId, { x: clamp(interaction.original.x + dx, 0, 1 - interaction.original.width),
        y: clamp(interaction.original.y + dy, 0, 1 - interaction.original.height) });
    } else {
      const original = interaction.original, handle = interaction.handle ?? "se";
      // 被拖动的边跟随指针，对边保持原位；最小尺寸和页边共同限制，避免翻转或拖出纸张。
      const right = original.x + original.width, bottom = original.y + original.height;
      const x = handle.includes("w") ? clamp(original.x + dx, 0, right - .01) : original.x;
      const y = handle.includes("n") ? clamp(original.y + dy, 0, bottom - .01) : original.y;
      updateRegion(interaction.regionId, { x, y,
        width: handle.includes("e") ? clamp(original.width + dx, .01, 1 - x) : right - x,
        height: handle.includes("s") ? clamp(original.height + dy, .01, 1 - y) : bottom - y });
    }
  };
  const onCanvasPointerUp = () => {
    if (!readonly && interaction?.kind === "draw" && currentPage && selectedQuestionId) {
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
    releasePointer();
  };
  const cancelInteraction = () => {
    // 拖动实时显示草稿坐标；Esc 恢复按下时的区域，不能借用 pointerup 提交未完成框选。
    if (!readonly && interaction && interaction.kind !== "draw") {
      updateRegion(interaction.regionId, interaction.original);
    }
    setInteraction(undefined);
    setSelectedRegionId("");
    releasePointer();
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
    if (readonly) return;
    dispatchLayout({ type: "removeRegion", regionId });
    setSelectedRegionId("");
  };
  const createIdentityRegion = (label = "学号") => {
    if (readonly || !currentPage) return;
    const region: LayoutRegion = { id: crypto.randomUUID(), label: normalizeIdentityRegionLabel(label),
      x: .1, y: .05, width: .3, height: .08 };
    dispatchLayout({ type: "addIdentityRegion", pageNo, region });
    setSelectedRegionId(region.id);
  };
  const updateOption = (optionId: string, patch: Partial<OptionRegion>) => {
    if (readonly || selectedRegionKind !== "question") return;
    if (selectedRegionId) dispatchLayout({ type: "updateOption", regionId: selectedRegionId, optionId, patch });
  };
  const addOptionRegion = () => {
    if (readonly || selectedRegionKind !== "question" || !selectedRegionId || !selectedRegion) return;
    const existing = selectedRegion.option_regions ?? [];
    if (existing.length >= 12) { message.warning("每题最多配置 12 个选项区域"); return; }
    const index = existing.length;
    // 标签用于答案映射，删除中间选项后也不能按数组长度复用仍占用的标签。
    const labels = new Set(existing.map(option => option.label.trim().toUpperCase()));
    const label = Array.from({ length: 12 }, (_, number) => String.fromCodePoint(65 + number)).find(value => !labels.has(value))!;
    const option: OptionRegion = {
      id: crypto.randomUUID(), label,
      x: Math.min(.84, .04 + index * .16), y: .25, width: .12, height: .5
    };
    dispatchLayout({ type: "addOption", regionId: selectedRegionId, option });
    // 手工新增的选项以本题为坐标原点；旧模板未改动时保留其既有坐标含义。
    updateRegion(selectedRegionId, { option_coordinate_space: "question_relative" });
  };
  const removeOptionRegion = (optionId: string) => {
    if (readonly || selectedRegionKind !== "question") return;
    if (selectedRegionId) dispatchLayout({ type: "removeOption", regionId: selectedRegionId, optionId });
  };

  return {
    canvasRef, selectedQuestionId, setSelectedQuestionId, zoom, setZoom,
    selectedRegionId, setSelectedRegionId, currentPage, selectedRegion, selectedRegionKind,
    selectedQuestion, coveredQuestions, draftRect, onCanvasPointerDown,
    beginRegionInteraction, onCanvasPointerMove, onCanvasPointerUp, updateRegion,
    createRegionWithoutDragging, createIdentityRegion, removeRegion, updateOption, addOptionRegion,
    removeOptionRegion, cancelInteraction
  };
}
