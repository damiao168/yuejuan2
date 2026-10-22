import { useEffect, useRef, useState, type RefObject } from "react";
import { getCaptureScanContext, type CaptureScanContext } from "../../api/exams";
import type { DesktopApiClient } from "../../api/client";
import { getUserErrorMessage } from "../../api/userError";
import type { WorkspaceKey } from "../../types";

interface CaptureSelection {
  scope: string;
  token: string | null;
  examId: string;
  batchId: string;
  client: DesktopApiClient;
}

// 模板读取按整次选择快照归属；同账号 A→B→A 切换也不能复用较早请求。
export function useCaptureScanContext(selection: CaptureSelection, selectionRef: RefObject<CaptureSelection>, workspace: WorkspaceKey) {
  const scanContextRequest = useRef(0);
  const [scanContextSnapshot, setScanContextSnapshot] = useState<{ owner: CaptureSelection; context: CaptureScanContext } | null>(null);
  const [scanContextError, setScanContextError] = useState<string | null>(null);
  const [loadingScanContext, setLoadingScanContext] = useState(false);
  const scanContext = scanContextSnapshot?.owner === selection ? scanContextSnapshot.context : null;
  const handleRefreshScanContext = async () => {
    const owner = selectionRef.current;
    if (!owner.token || !owner.scope || !owner.batchId || !owner.examId) return;
    const request = ++scanContextRequest.current;
    setScanContextSnapshot(null); setScanContextError(null); setLoadingScanContext(true);
    const isCurrent = () => selectionRef.current === owner && request === scanContextRequest.current;
    try {
      const context = await getCaptureScanContext(owner.client, owner.batchId);
      if (!isCurrent()) return;
      if (context.batch.id !== owner.batchId || context.batch.exam_id !== owner.examId || context.exam.id !== owner.examId) throw new Error("模板信息与当前考试或批次不一致，请重新读取。");
      setScanContextSnapshot({ owner, context });
    } catch (error) {
      if (isCurrent()) setScanContextError(getUserErrorMessage(error, "当前批次答题卡模板读取失败，请在本页重新读取。"));
    } finally { if (isCurrent()) setLoadingScanContext(false); }
  };
  // 选择变化即撤下旧模板；A→B→A 的迟到响应也不能沿用，归属使用整次选择快照。
  // biome-ignore lint/correctness/useExhaustiveDependencies: 自动读取由 selection 驱动，刷新函数读取当前 ref。
  useEffect(() => {
    scanContextRequest.current += 1;
    setScanContextSnapshot(null); setScanContextError(null); setLoadingScanContext(false);
    if (workspace === "scan") void handleRefreshScanContext();
    return () => { scanContextRequest.current += 1; };
  }, [selection, workspace]);
  return { scanContext, scanContextError, loadingScanContext, handleRefreshScanContext };
}
