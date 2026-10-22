import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { App } from "antd";
import { downloadScoringResultImage, type ExamAutomationResults, type ScoringRunItem } from "../../../../api/review";
import { formatAnswer, formatError } from "../gradingWorkbench.model";
import type { ScoringImagePreview, ScoringResultState, ScoringResultType } from "../gradingWorkbench.types";
import { matchesScoringReviewFilter, type ScoringReviewFilter } from "../scoringReviewCategories";

// 结果筛选与影像预览只管理展示；换考试和关闭窗口都要作废仍在途的图片请求。
export function useScoringResults(initialExamId: string, runDetail: ExamAutomationResults | null) {
  const { message } = App.useApp();
  const [resultType, setResultType] = useState<ScoringResultType>("all");
  const [resultState, setResultState] = useState<ScoringResultState>("all");
  const [resultKeyword, setResultKeyword] = useState("");
  const [resultReviewFilter, setResultReviewFilter] = useState<ScoringReviewFilter>("all");
  const [image, setImage] = useState<ScoringImagePreview | null>(null);
  const [imageLoading, setImageLoading] = useState("");
  const imageRequestRef = useRef(0);
  const filteredItems = useMemo(() => {
    const text = resultKeyword.trim().toLowerCase();
    return (runDetail?.items ?? []).filter((item) => {
      const typeMatched = resultType === "all" ||
        (resultType === "choice" && ["single_choice", "multiple_choice", "true_false"].includes(item.question_type)) ||
        (resultType === "fill" && ["fill_blank", "numeric"].includes(item.question_type)) ||
        (resultType === "subjective" && !["single_choice", "multiple_choice", "true_false", "fill_blank", "numeric"].includes(item.question_type));
      const stateMatched = resultState === "all" ||
        (resultState === "processing" ? ["pending", "processing", "cancelling"].includes(item.state) : item.state === resultState);
      const keywordMatched = !text || [item.anonymous_code, item.question_no, item.recognized_answer, formatAnswer(item.standard_answer)]
        .some((value) => (value ?? "").toLowerCase().includes(text));
      return typeMatched && stateMatched && keywordMatched && matchesScoringReviewFilter(item, resultReviewFilter);
    });
  }, [resultKeyword, resultState, resultType, resultReviewFilter, runDetail?.items]);
  const resultMetrics = useMemo(() => {
    const items = runDetail?.items ?? [];
    return {
      total: items.length,
      auto: items.filter((item) => item.state === "confirmed" && item.grade_source === "rule_confirmed").length,
      review: items.filter((item) => item.state === "review").length,
      failed: items.filter((item) => item.state === "failed").length
    };
  }, [runDetail?.items]);

  const showImage = useCallback(async (item: ScoringRunItem) => {
    const requestId = ++imageRequestRef.current;
    setImageLoading(item.answer_segment_id);
    try {
      const file = await downloadScoringResultImage(item.answer_segment_id);
      if (requestId !== imageRequestRef.current) return;
      setImage((current) => {
        if (current?.url) URL.revokeObjectURL(current.url);
        return { url: URL.createObjectURL(file.blob), contentType: file.contentType, filename: file.filename, title: `第 ${item.question_no} 题 · 作答详情`, item };
      });
    } catch (error) {
      if (requestId === imageRequestRef.current) message.error(formatError(error));
    } finally {
      if (requestId === imageRequestRef.current) setImageLoading("");
    }
  }, [message]);

  const closeImage = useCallback(() => {
    imageRequestRef.current += 1;
    setImageLoading("");
    setImage(null);
  }, []);

  // 切管理视图保留筛选；换考试或会话时清空筛选并使旧图片请求失效。
  useEffect(() => { closeImage(); setResultType("all"); setResultState("all"); setResultKeyword(""); setResultReviewFilter("all"); }, [initialExamId, closeImage]);
  useEffect(() => () => {
    if (image?.url) URL.revokeObjectURL(image.url);
  }, [image?.url]);

  return { resultType, setResultType, resultState, setResultState, resultKeyword, setResultKeyword,
    resultReviewFilter, setResultReviewFilter,
    image, imageLoading, filteredItems, resultMetrics, showImage, closeImage };
}
