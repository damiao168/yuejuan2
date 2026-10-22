import { useEffect, useRef, useState, type RefObject } from "react";
import { App } from "antd";
import { fillPaperImportContent, type PaperImportJob } from "../../api/papers";
import { getUserErrorMessage } from "../../api/client";
import { reviewJobKey } from "./paperImportReview.model";
import type { PaperImportWorkflowOptions } from "./paperImportWorkflow.types";

// 填入已识别内容不会确认复核草稿；返回结果只能显示在发起时的资料代次中。
export function usePaperImportContentFill({ canManage, selectedExam, loadConfig, onChanged, activeReviewJobKey, activeReviewJobKeyRef }:
  Pick<PaperImportWorkflowOptions, "canManage" | "selectedExam" | "loadConfig" | "onChanged"> & {
    activeReviewJobKey: string | null; activeReviewJobKeyRef: RefObject<string | null>;
  }) {
  const { message } = App.useApp();
  const formatError = (error: unknown) => getUserErrorMessage(error, "操作失败，请稍后重试");
  const [fillingImportContent, setFillingImportContent] = useState(false);
  const fillingContentRef = useRef(false);
  const [contentFillResult, setContentFillResult] = useState<Awaited<ReturnType<typeof fillPaperImportContent>> | null>(null);

  // biome-ignore lint/correctness/useExhaustiveDependencies: 同任务的资料代次变化后不能展示旧的填入回执。
  useEffect(() => { setContentFillResult(null); }, [activeReviewJobKey]);
  const fillRecognizedContent = async (job: PaperImportJob, quiet = false) => {
    if (!canManage || !selectedExam || fillingContentRef.current) return;
    const jobKey = reviewJobKey(job);
    if (activeReviewJobKeyRef.current !== jobKey) return;
    fillingContentRef.current = true;
    setFillingImportContent(true);
    try {
      const result = await fillPaperImportContent(job.id, job.generation);
      // 旧资料请求可完成服务端写入，但不能把结果或提示放到新资料的编辑页面。
      if (activeReviewJobKeyRef.current !== jobKey) return;
      setContentFillResult(result);
      if (result.filled_questions.length) {
        message.success(`已自动填入 ${result.filled_questions.length} 道题的识别内容，请核对答案与评分配置`);
        await loadConfig(selectedExam.id, { silent: true });
        onChanged?.();
      } else if (!quiet) message.info("现有内容已保留；未能自动匹配的题目请查看提示");
    } catch (error) {
      if (activeReviewJobKeyRef.current === jobKey) message.error(formatError(error));
    } finally {
      fillingContentRef.current = false;
      setFillingImportContent(false);
    }
  };

  return { fillingImportContent, fillRecognizedContent, contentFillResult };
}
