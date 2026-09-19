import { useState, type Dispatch, type MutableRefObject } from "react";
import { App } from "antd";
import type { PDFDocumentProxy } from "pdfjs-dist";
import { getUserErrorMessage } from "../../../api/client";
import type { LayoutRegion } from "../../../api/configuration";
import type { Question } from "../../../api/papers";
import type { TemplateLayoutAction } from "./templateLayoutReducer";

function clamp(value: number, min = 0, max = 1) { return Math.min(max, Math.max(min, value)); }
function roundCoordinate(value: number) { return Number(value.toFixed(6)); }

export function useRegionSuggestion({
  pdfRef, questions, readonly, dispatchLayout
}: {
  pdfRef: MutableRefObject<PDFDocumentProxy | undefined>;
  questions: Question[];
  readonly: boolean;
  dispatchLayout: Dispatch<TemplateLayoutAction>;
}) {
  const { message } = App.useApp();
  const [suggestingRegions, setSuggestingRegions] = useState(false);

  const suggestQuestionRegions = async () => {
    const pdf = pdfRef.current;
    if (!pdf || readonly || !questions.length) {
      message.warning("当前文件没有可分析的 PDF 页面，或模板已锁定");
      return;
    }
    setSuggestingRegions(true);
    try {
      const anchors = new Map<number, Array<{ question: Question; top: number }>>();
      const unmatched = new Set(questions.map((question) => question.id));
      const normalized = questions.map((question) => ({
        question, key: question.question_no.replace(/\s+/g, "").toLowerCase()
      })).sort((a, b) => b.key.length - a.key.length);
      for (let pageIndex = 1; pageIndex <= pdf.numPages; pageIndex += 1) {
        const page = await pdf.getPage(pageIndex);
        const viewport = page.getViewport({ scale: 1 });
        const content = await page.getTextContent();
        const pageAnchors: Array<{ question: Question; top: number }> = [];
        for (const raw of content.items) {
          if (!("str" in raw) || !("transform" in raw)) continue;
          const text = String(raw.str).replace(/\s+/g, "").toLowerCase();
          const candidate = normalized.find(({ question, key }) => unmatched.has(question.id)
            && (text === key || text.startsWith(`${key}.`)
              || text.startsWith(`${key}、`) || text.startsWith(`${key}．`)));
          if (!candidate) continue;
          const transform = raw.transform as number[];
          pageAnchors.push({
            question: candidate.question,
            top: clamp(1 - ((transform[5] || 0) + Math.abs(transform[3] || transform[0] || 12)) / viewport.height)
          });
          unmatched.delete(candidate.question.id);
        }
        pageAnchors.sort((a, b) => a.top - b.top);
        if (pageAnchors.length) anchors.set(pageIndex, pageAnchors);
      }
      if (!anchors.size) {
        message.warning("没有从 PDF 文字层识别到题号；扫描版请先完成 OCR 后再生成候选区域");
        return;
      }
      const pages = new Map<number, LayoutRegion[]>();
      for (const [pageNo, pageAnchors] of anchors) {
        pages.set(pageNo, pageAnchors.map(({ question, top }, index) => {
          const y = clamp(top - .01, .02, .94);
          return {
            id: crypto.randomUUID(), question_id: question.id, label: question.question_no,
            x: .04, y: roundCoordinate(y), width: .92,
            height: roundCoordinate(clamp((pageAnchors[index + 1]?.top ?? .96) - y - .012, .04, .42)),
            option_regions: [], suggestion_confidence: .72,
            suggestion_source: "pdf_text_anchor" as const
          };
        }));
      }
      dispatchLayout({ type: "suggestRegions", pages });
      message.success(`已生成 ${questions.length - unmatched.size} 道题的候选区域；请逐题核对并调整${unmatched.size ? `，另有 ${unmatched.size} 道题未识别` : ""}`);
    } catch (error) {
      message.error(getUserErrorMessage(error, "操作失败，请稍后重试"));
    } finally {
      setSuggestingRegions(false);
    }
  };

  return { suggestingRegions, suggestQuestionRegions };
}
