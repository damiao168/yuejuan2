// @vitest-environment jsdom
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";
import type { WorkbenchContext } from "../gradingWorkbench.types";
import { AnswerEvidencePane, type AnswerEvidencePaneProps } from "./AnswerEvidencePane";

vi.mock("../../../../components/features/review-annotation", () => ({ ReviewAnnotationWorkspace: () => null }));

const context = {
  task: { id: "task-1", question_no: "1", anonymous_code: "A001", answer_segment_id: "segment-1", source: "manual", status: "in_progress" },
  ocrText: "",
  ocrResults: [],
  segmentImageUrl: ""
} as unknown as WorkbenchContext;
const imagePreview = { url: "blob:answer-image", contentType: "image/png" };
const mathSelection = { segmentId: "segment-1", binding: "artifact:1", stepId: "S1", label: "代入公式", bbox: { x: 0.1, y: 0.2, width: 0.3, height: 0.4 } };
const noop = () => undefined;
const baseProps: AnswerEvidencePaneProps = {
  context, preview: imagePreview, previewLoading: false, viewerMode: "segment", scale: 2, autoFit: false,
  imageSize: { width: 100, height: 80 }, rotation: 90, offset: { x: 7, y: -3 }, dragging: false,
  viewportRef: { current: null }, canViewOriginalImage: true, canEditDraft: false, rubricPoints: [],
  onChangeViewerMode: noop, onZoom: noop, onRotate: noop, onFit: noop,
  onPointerDown: noop, onPointerMove: noop, onPointerUp: noop, onKeyDown: noop, onImageLoad: noop
};

function renderPane(overrides: Partial<AnswerEvidencePaneProps> = {}) {
  const host = document.createElement("div");
  host.innerHTML = renderToStaticMarkup(<AnswerEvidencePane {...baseProps} {...overrides} />);
  return host;
}

function ocrResult(id: string, text: string, bbox: number[]): WorkbenchContext["ocrResults"][number] {
  return { id, text, bbox } as WorkbenchContext["ocrResults"][number];
}

describe("AnswerEvidencePane batch13 rendering", () => {
  it("共区模板显示整区/单题切换并明确当前录入题号", () => {
    const grouped = renderPane({ viewerMode: "group", context: { ...context, gradingGroupImageUrl: "/shared" }, mathSelection });
    expect(grouped.textContent).toContain("整区");
    expect(grouped.textContent).toContain("单题");
    expect(grouped.textContent).toContain("当前录入第 1 题得分");
    expect(grouped.querySelector(".math-step-highlight")).toBeNull();
    const individual = renderPane();
    expect(individual.textContent).not.toContain("当前录入第");
    expect(individual.textContent).toContain("答题区域");
  });
  it("加载优先于无文件，OCR 无文件仍能显示文本", () => {
    const loading = renderPane({ preview: null, previewLoading: true, viewerMode: "ocr" });
    expect(loading.textContent).toContain("正在读取答卷页面");
    expect(loading.querySelector(".answer-viewer")).toBeNull();
    const empty = renderPane({ preview: null });
    expect(empty.textContent).toContain("暂无答卷页面");
    const ocr = renderPane({ preview: null, viewerMode: "ocr" });
    expect(ocr.querySelector("pre")?.textContent).toBe("本题尚无识别文本，请查看答题区域。");
    expect(ocr.querySelector<HTMLElement>(".answer-viewer-stage")?.style.transform).toBe("translate(7px, -3px) scale(2) rotate(90deg)");
  });

  it.each([
    ["image/png", "img", "alt", "答卷页面"],
    ["application/pdf", "iframe", "title", "答卷 PDF"],
    ["text/plain", "a[download]", "download", "答卷文件"]
  ])("%s 保留原生查看区域及文件预览边界", (contentType, selector, attribute, fallback) => {
    const host = renderPane({ preview: { url: "blob:answer-file", contentType } });
    const viewport = host.querySelector(".answer-viewer")!;
    expect(viewport.tagName).toBe("SECTION");
    expect(viewport.getAttribute("aria-label")).toBe("1 答题图查看区");
    expect(viewport.getAttribute("tabindex")).toBeNull();
    const keyboardControl = host.querySelector('button[aria-label="键盘平移答题图"]')!;
    expect(keyboardControl).not.toBeNull();
    expect(keyboardControl.getAttribute("aria-describedby")).toBe(viewport.getAttribute("aria-describedby"));
    const help = host.querySelector(`[id="${viewport.getAttribute("aria-describedby")}"]`);
    expect(help?.textContent).toContain("Home 适配窗口");
    const preview = viewport.querySelector(selector)!;
    expect(preview.getAttribute(attribute)).toBe(fallback);
    expect(preview.closest("button")).toBeNull();
    expect(viewport.getAttribute("role")).toBeNull();
  });

  it("图片按真实尺寸缩放，PDF 使用 transform scale", () => {
    const image = renderPane({ dragging: true }).querySelector<HTMLElement>(".answer-viewer-stage")!;
    expect(image.style.width).toBe("200px");
    expect(image.style.height).toBe("160px");
    expect(image.style.transform).toBe("translate(7px, -3px) rotate(90deg)");
    expect(image.parentElement?.className).toBe("answer-viewer dragging");
    const pdf = renderPane({ preview: { url: "blob:answer-pdf", contentType: "application/pdf", filename: "answer.pdf" } });
    expect(pdf.querySelector("iframe")?.getAttribute("title")).toBe("answer.pdf");
    const stage = pdf.querySelector<HTMLElement>(".answer-viewer-stage")!;
    expect(stage.style.width).toBe("");
    expect(stage.style.transform).toBe("translate(7px, -3px) scale(2) rotate(90deg)");
  });

  it("OCR 先截取十二条再跳过无效框，保留原像素和最小边长", () => {
    const ocrResults = Array.from({ length: 13 }, (_, index) => ocrResult(String(index), `文本${index}`, index === 0 ? [1, 2, 3] : [20, 30, 25, 35]));
    const host = renderPane({ viewerMode: "ocr", context: { ...context, ocrResults } });
    const boxes = host.querySelectorAll<HTMLElement>(".ocr-bbox");
    expect(boxes).toHaveLength(11);
    expect(host.querySelector('.ocr-bbox[title="文本12"]')).toBeNull();
    expect([boxes[0].style.left, boxes[0].style.top, boxes[0].style.width, boxes[0].style.height]).toEqual(["20px", "30px", "12px", "12px"]);
    expect(host.querySelector("pre")?.textContent).toBe(ocrResults.map((result) => result.text).join("\n"));
    expect(renderPane({ viewerMode: "ocr", context: { ...context, ocrResults, ocrText: "已合并文本" } }).querySelector("pre")?.textContent).toBe("已合并文本");
    expect(host.querySelector("img")).toBeNull();
  });

  it("数学框仅覆盖当前答题区域图片，定位提示仍允许当前区域的 PDF", () => {
    const image = renderPane({ mathSelection });
    const highlight = image.querySelector<HTMLElement>(".math-step-highlight")!;
    expect([highlight.style.left, highlight.style.top, highlight.style.width, highlight.style.height]).toEqual(["10%", "20%", "30%", "40%"]);
    expect(highlight.getAttribute("aria-label")).toBe("步骤 S1 的答题位置");
    const status = image.querySelector<HTMLOutputElement>("output.math-image-selection")!;
    expect(status.textContent).toBe("已定位 S1 · 代入公式");
    expect(status.style.display).toBe("block");
    const pdf = renderPane({ mathSelection, preview: { url: "blob:answer-pdf", contentType: "application/pdf" } });
    expect(pdf.querySelector(".math-step-highlight")).toBeNull();
    expect(pdf.querySelector("output")).not.toBeNull();
    for (const overrides of [{ viewerMode: "original" as const }, { mathSelection: { ...mathSelection, segmentId: "segment-2" } }]) {
      const other = renderPane({ mathSelection, ...overrides });
      expect(other.querySelector(".math-step-highlight")).toBeNull();
      expect(other.querySelector("output")).toBeNull();
    }
  });
});
