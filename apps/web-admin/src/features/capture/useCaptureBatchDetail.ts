import { useCallback, useRef, useState, type Dispatch, type SetStateAction } from "react";
import { App } from "antd";
import { getCaptureBatch, getMatchingQueue, type CaptureBatch, type CaptureBatchDetail, type MatchingQueue, type ProcessingSummary } from "../../api/capture";
import { getUserErrorMessage } from "../../api/client";

export function useCaptureBatchDetail(
  setBatches: Dispatch<SetStateAction<CaptureBatch[]>>,
  setActiveTab: Dispatch<SetStateAction<string>>,
) {
  const { message } = App.useApp();
  const [detail, setDetail] = useState<CaptureBatchDetail>();
  const [detailLoading, setDetailLoading] = useState(false);
  const [matching, setMatching] = useState<MatchingQueue>();
  const [matchingLoading, setMatchingLoading] = useState(false);
  const [processingSummaries, setProcessingSummaries] = useState<Record<string, ProcessingSummary>>({});
  // 详情与匹配分别计代次；切批次或重置后，两类旧请求都不能回填当前面板。
  const detailRequestRef = useRef(0);
  const matchingRequestRef = useRef(0);

  const loadDetail = useCallback(
    async (batchId: string, quiet = false) => {
      const requestId = ++detailRequestRef.current;
      if (!batchId) {
        setDetail(undefined);
		setProcessingSummaries({});
        setDetailLoading(false);
        return;
      }
      if (!quiet) setDetailLoading(true);
      try {
        const result = await getCaptureBatch(batchId);
        if (requestId !== detailRequestRef.current) return;
        setDetail(result);
		if (!quiet && (result.batch.failed_count > 0 || result.pages.some((item) => ["needs_review", "quality_rejected", "failed"].includes(item.status)))) setActiveTab("issues");
		setProcessingSummaries(Object.fromEntries(result.processing_summaries.map((item) => [item.submission_id, item])));
        setBatches((current) =>
          current.map((item) =>
            item.id === result.batch.id ? result.batch : item,
          ),
        );
      } catch (currentError) {
        if (requestId !== detailRequestRef.current) return;
        if (!quiet) message.error(getUserErrorMessage(currentError, "操作失败，请重试"));
      } finally {
        if (!quiet && requestId === detailRequestRef.current) setDetailLoading(false);
      }
    },
    [message, setActiveTab, setBatches],
  );

  const loadMatching = useCallback(
    async (batchId: string) => {
      const requestId = ++matchingRequestRef.current;
      if (!batchId) {
        setMatching(undefined);
        setMatchingLoading(false);
        return;
      }
      setMatchingLoading(true);
      try {
        const result = await getMatchingQueue(batchId);
        if (requestId === matchingRequestRef.current) setMatching(result);
      } catch (currentError) {
        if (requestId !== matchingRequestRef.current) return;
        message.error(getUserErrorMessage(currentError, "操作失败，请重试"));
      } finally {
        if (requestId === matchingRequestRef.current) setMatchingLoading(false);
      }
    },
    [message],
  );

  const resetDetail = useCallback(() => {
    detailRequestRef.current += 1;
    matchingRequestRef.current += 1;
    setDetail(undefined);
    setMatching(undefined);
    setProcessingSummaries({});
  }, []);

  return { detail, detailLoading, matching, setMatching, matchingLoading, processingSummaries,
    loadDetail, loadMatching, resetDetail };
}
