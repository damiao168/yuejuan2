import { useCallback, useRef, useState } from "react";
import { App } from "antd";
import { listCaptureBatches, type CaptureBatch } from "../../api/capture";
import { getUserErrorMessage } from "../../api/client";

export function useCaptureBatchList(examId: string) {
  const { message } = App.useApp();
  const [batches, setBatches] = useState<CaptureBatch[]>([]);
  const [selectedId, setSelectedId] = useState("");
  const [loading, setLoading] = useState(true);
  const [loadingMoreBatches, setLoadingMoreBatches] = useState(false);
  const [nextBatchCursor, setNextBatchCursor] = useState("");
  const [hasMoreBatches, setHasMoreBatches] = useState(false);
  const [error, setError] = useState<string>();
  const batchesRequestRef = useRef(0);

  const loadBatches = useCallback(async () => {
    const requestId = ++batchesRequestRef.current;
    setLoading(true);
    setError(undefined);
    try {
      const result = await listCaptureBatches(examId, { limit: 30 });
      if (requestId !== batchesRequestRef.current) return;
      setBatches(result.batches);
      setNextBatchCursor(result.next_cursor ?? "");
      setHasMoreBatches(Boolean(result.has_more));
      setSelectedId((current) =>
        result.batches.some((batch) => batch.id === current) ? current : result.batches[0]?.id || ""
      );
    } catch (currentError) {
      if (requestId !== batchesRequestRef.current) return;
      setError(getUserErrorMessage(currentError, "操作失败，请重试"));
      setNextBatchCursor("");
      setHasMoreBatches(false);
    } finally {
      if (requestId === batchesRequestRef.current) setLoading(false);
    }
  }, [examId]);

  const loadMoreBatches = useCallback(async () => {
    if (!hasMoreBatches || !nextBatchCursor || loadingMoreBatches) return;
    const requestId = ++batchesRequestRef.current;
    setLoadingMoreBatches(true);
    try {
      const result = await listCaptureBatches(examId, { limit: 30, cursor: nextBatchCursor });
      if (requestId !== batchesRequestRef.current) return;
      setBatches((current) => {
        const byID = new Map(current.map((item) => [item.id, item]));
        result.batches.forEach((item) => byID.set(item.id, item));
        return Array.from(byID.values());
      });
      setNextBatchCursor(result.next_cursor ?? "");
      setHasMoreBatches(Boolean(result.has_more));
    } catch (currentError) {
      if (requestId === batchesRequestRef.current) message.error(getUserErrorMessage(currentError, "操作失败，请重试"));
    } finally {
      if (requestId === batchesRequestRef.current) setLoadingMoreBatches(false);
    }
  }, [examId, hasMoreBatches, loadingMoreBatches, message, nextBatchCursor]);

  return { batches, setBatches, selectedId, setSelectedId, loading, loadingMoreBatches,
    nextBatchCursor, hasMoreBatches, error, loadBatches, loadMoreBatches };
}
