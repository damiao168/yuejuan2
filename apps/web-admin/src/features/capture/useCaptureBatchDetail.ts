import { useCallback, useRef, useState, type Dispatch, type SetStateAction } from "react";
import { getCaptureBatch, getMatchingQueue, type CaptureBatch, type CaptureBatchDetail, type MatchingQueue, type ProcessingSummary } from "../../api/capture";
import { getUserErrorMessage } from "../../api/client";

export function useCaptureBatchDetail(
  setBatches: Dispatch<SetStateAction<CaptureBatch[]>>,
  setActiveTab: Dispatch<SetStateAction<string>>,
  selectedId: string,
  preferredTab?: string,
) {
  const [detail, setDetail] = useState<CaptureBatchDetail>();
  const [detailLoading, setDetailLoading] = useState(false);
  const [matching, setMatching] = useState<MatchingQueue>();
  const [matchingLoading, setMatchingLoading] = useState(false);
  const [detailError, setDetailError] = useState<string>();
  const [matchingError, setMatchingError] = useState<string>();
  const [processingSummaries, setProcessingSummaries] = useState<Record<string, ProcessingSummary>>({});
  // 详情与匹配分别计代次；切批次或重置后，两类旧请求都不能回填当前面板。
  const detailRequestRef = useRef(0);
  const matchingRequestRef = useRef(0);
  const scope = useRef(selectedId);
  // 渲染时就切断旧对象的请求；不能等 effect 才让旧页和新批次按钮失效。
  if (scope.current !== selectedId) {
    scope.current = selectedId;
    detailRequestRef.current += 1;
    matchingRequestRef.current += 1;
  }

  const loadDetail = useCallback(
    async (batchId: string, quiet = false) => {
      if (batchId !== scope.current) return;
      const requestId = ++detailRequestRef.current;
      setDetailError(undefined);
      if (!batchId) {
        setDetail(undefined);
		setProcessingSummaries({});
        setDetailLoading(false);
        return;
      }
      if (!quiet) { setDetail(undefined); setProcessingSummaries({}); setDetailLoading(true); }
      try {
        const result = await getCaptureBatch(batchId);
        if (requestId !== detailRequestRef.current) return;
        if (result.batch.id !== batchId) throw new Error("返回的批次不匹配，请重试");
        setDetail(result);
        if (!quiet && preferredTab && ["issues", "files", "image-quality", "processing", "matching"].includes(preferredTab)) setActiveTab(preferredTab);
        else if (!quiet && (result.batch.failed_count > 0 || result.pages.some((item) => ["needs_review", "quality_rejected", "failed"].includes(item.status)))) setActiveTab("issues");
		setProcessingSummaries(Object.fromEntries(result.processing_summaries.map((item) => [item.submission_id, item])));
        setBatches((current) =>
          current.map((item) =>
            item.id === result.batch.id ? result.batch : item,
          ),
        );
      } catch (currentError) {
        if (requestId !== detailRequestRef.current) return;
        setDetailError(getUserErrorMessage(currentError, "批次加载失败，请重试"));
      } finally {
        if (!quiet && requestId === detailRequestRef.current) setDetailLoading(false);
      }
    },
    [setActiveTab, setBatches, preferredTab],
  );

  const loadMatching = useCallback(
    async (batchId: string) => {
      if (batchId !== scope.current) return;
      const requestId = ++matchingRequestRef.current;
      setMatching(undefined);
      setMatchingError(undefined);
      if (!batchId) {
        setMatching(undefined);
        setMatchingLoading(false);
        return;
      }
      setMatchingLoading(true);
      try {
        const result = await getMatchingQueue(batchId);
        if (result.batch_id !== batchId) throw new Error("返回的匹配队列不属于当前批次，请重试");
        if (requestId === matchingRequestRef.current) setMatching(result);
      } catch (currentError) {
        if (requestId !== matchingRequestRef.current) return;
        setMatchingError(getUserErrorMessage(currentError, "匹配队列加载失败，请重试"));
      } finally {
        if (requestId === matchingRequestRef.current) setMatchingLoading(false);
      }
    },
    [],
  );

  const resetDetail = useCallback(() => {
    detailRequestRef.current += 1;
    matchingRequestRef.current += 1;
    setDetail(undefined);
    setMatching(undefined);
    setProcessingSummaries({});
    setDetailError(undefined);
    setMatchingError(undefined);
    setDetailLoading(false);
    setMatchingLoading(false);
  }, []);

  return { detail: detail?.batch.id === selectedId ? detail : undefined, detailError, detailLoading,
    matching: matching?.batch_id === selectedId ? matching : undefined, matchingError, setMatching, matchingLoading,
    processingSummaries: detail?.batch.id === selectedId ? processingSummaries : {},
    loadDetail, loadMatching, resetDetail };
}
