import { useCaptureScanContext } from "./useCaptureScanContext";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import type { DesktopApiClient } from "../../api/client";
import { getCaptureBatchDetail, listCaptureBatches, listExams, type CaptureBatch, type CaptureBatchProcessingDetail } from "../../api/exams";
import { getUserErrorMessage } from "../../api/userError";
import type { Exam, LocalLogEntry, WorkspaceKey } from "../../types";

type LogEvent = (level: LocalLogEntry["level"], message: string, context?: string) => Promise<void>;

export function useExamCaptureContext({
  client, token, durableScopeKey, workspace, logEvent
}: {
  client: DesktopApiClient; token: string | null; durableScopeKey: string; workspace: WorkspaceKey; logEvent: LogEvent;
}) {
  const [exams, setExams] = useState<Exam[]>([]);
  const [selectedExamId, setSelectedExamId] = useState("");
  const [examError, setExamError] = useState<string | null>(null);
  const [isLoadingExams, setIsLoadingExams] = useState(false);
  const [scanSubmissionId, setScanSubmissionId] = useState("");
  const [captureBatchId, setCaptureBatchId] = useState("");
  const [captureBatches, setCaptureBatches] = useState<CaptureBatch[]>([]);
  const [isLoadingCaptureBatches, setIsLoadingCaptureBatches] = useState(false);
  const [scanStartPage, setScanStartPage] = useState(1);
  const [processingError, setProcessingError] = useState<string | null>(null);
  const [loadingProcessing, setLoadingProcessing] = useState(false);
  const [queueFocus, setQueueFocus] = useState<{ examId: string; batchId: string } | null>(null);
  const queueFocusRef = useRef(queueFocus);
  queueFocusRef.current = queueFocus;
  const processingRequest = useRef(0);
  const captureBatchLoadRef = useRef(0);
  const examLoadRef = useRef(0);
  const scopeRef = useRef(durableScopeKey);
  scopeRef.current = durableScopeKey;
  const selection = useMemo(() => ({ scope: durableScopeKey, token, examId: selectedExamId, batchId: captureBatchId, client }),
    [durableScopeKey, token, selectedExamId, captureBatchId, client]);
  const selectionRef = useRef(selection);
  selectionRef.current = selection;
  const { scanContext, scanContextError, loadingScanContext, handleRefreshScanContext } = useCaptureScanContext(selection, selectionRef, workspace);
  const [processingSnapshot, setProcessingSnapshot] = useState<{ owner: typeof selection; detail: CaptureBatchProcessingDetail } | null>(null);
  // 同一账号也可能切换考试或批次；渲染直接核对归属，不等待 effect 才隐藏旧答卷。
  const processingDetail = processingSnapshot?.owner === selection ? processingSnapshot.detail : null;
  const linkedSubmissionIds = [...new Set((processingDetail?.pages ?? []).map((page) => page.submission_id).filter((id): id is string => Boolean(id)))];

  const clearProcessing = useCallback(() => {
    processingRequest.current += 1;
    setProcessingSnapshot(null); setProcessingError(null); setLoadingProcessing(false); setScanSubmissionId("");
  }, []);
  const selectExam = useCallback((examId: string) => {
    if (selectionRef.current.examId === examId) return;
    queueFocusRef.current = null; setQueueFocus(null);
    // 先使异步请求失效，再更新界面；不能把上一考试的批次或答卷带进新选择。
    selectionRef.current = { ...selectionRef.current, examId, batchId: "" };
    captureBatchLoadRef.current += 1;
    setSelectedExamId(examId); setCaptureBatchId(""); setCaptureBatches([]);
    clearProcessing();
  }, [clearProcessing]);
  const selectCaptureBatch = useCallback((batchId: string) => {
    if (selectionRef.current.batchId === batchId) return;
    queueFocusRef.current = null; setQueueFocus(null);
    selectionRef.current = { ...selectionRef.current, batchId };
    setCaptureBatchId(batchId); clearProcessing();
  }, [clearProcessing]);
  const handleOpenQueueContext = (examId: string, batchId: string) => {
    const focus = { examId, batchId };
    queueFocusRef.current = focus; setQueueFocus(focus);
    // 同步队列携带原文件的考试与批次；直接读取该批次，不能悄悄跳到列表中的第一批。
    selectionRef.current = { ...selectionRef.current, examId, batchId };
    captureBatchLoadRef.current += 1;
    setSelectedExamId(examId); setCaptureBatchId(batchId); setCaptureBatches([]); clearProcessing();
    void handleLoadExams();
  };

  // biome-ignore lint/correctness/useExhaustiveDependencies: 账号租约变化必须清空考试与批次，即使重置函数不读取该键。
  useEffect(() => {
    examLoadRef.current += 1; captureBatchLoadRef.current += 1;
    setExams([]); setSelectedExamId(""); setExamError(null); setIsLoadingExams(false);
    setCaptureBatchId(""); setCaptureBatches([]); setIsLoadingCaptureBatches(false); setScanStartPage(1);
    queueFocusRef.current = null; setQueueFocus(null);
    clearProcessing();
  }, [durableScopeKey, clearProcessing]);

  const handleLoadExams = async () => {
    const scope = durableScopeKey;
    if (!scope || !token) return;
    const requestId = ++examLoadRef.current;
    setExamError(null); setIsLoadingExams(true);
    try {
      const result = await listExams(client, {});
      if (scopeRef.current !== scope || examLoadRef.current !== requestId) return;
      setExams(result.exams);
      if (!selectionRef.current.examId) selectExam(result.exams[0]?.id ?? "");
      await logEvent("info", "exam list loaded for scan workstation", `${result.exams.length} exams`);
    } catch (error) {
      if (scopeRef.current !== scope || examLoadRef.current !== requestId) return;
      const message = getUserErrorMessage(error, "考试列表读取失败");
      setExamError(message); setExams([]);
      await logEvent("warning", "exam list load failed", message);
    } finally {
      if (scopeRef.current === scope && examLoadRef.current === requestId) setIsLoadingExams(false);
    }
  };

  const handleLoadCaptureBatches = useCallback(async (examID = selectedExamId) => {
    const scope = durableScopeKey;
    if (!scope || !token) return;
    const requestID = ++captureBatchLoadRef.current;
    if (!examID) { setCaptureBatches([]); selectCaptureBatch(""); setIsLoadingCaptureBatches(false); return; }
    setIsLoadingCaptureBatches(true);
    const isCurrent = () => scopeRef.current === scope && selectionRef.current.examId === examID && requestID === captureBatchLoadRef.current;
    try {
      const result = await listCaptureBatches(client, examID);
      if (!isCurrent()) return;
      const batches = result.batches.filter((batch) => batch.exam_id === examID);
      // 历史批次仍可查看处理结果；是否可上传继续由工作区按服务端状态判断。
      setCaptureBatches((current) => {
        const focused = current.find((batch) => batch.id === queueFocusRef.current?.batchId && batch.exam_id === examID);
        return focused && !batches.some((batch) => batch.id === focused.id) ? [...batches, focused] : batches;
      });
      const current = selectionRef.current.batchId;
      const focused = queueFocusRef.current;
      if (focused?.examId !== examID || focused.batchId !== current) {
        selectCaptureBatch(batches.some((batch) => batch.id === current) ? current : batches.find((batch) => !["completed", "cancelled"].includes(batch.status))?.id ?? batches[0]?.id ?? "");
      }
      await logEvent("info", "capture batches loaded for scan workstation", `${batches.length} batches`);
    } catch (error) {
      if (!isCurrent()) return;
      if (!queueFocusRef.current) { setCaptureBatches([]); selectCaptureBatch(""); }
      const message = getUserErrorMessage(error, "采集批次读取失败"); setExamError(message);
      await logEvent("warning", "capture batch list load failed", message);
    } finally { if (isCurrent()) setIsLoadingCaptureBatches(false); }
  }, [client, durableScopeKey, logEvent, selectedExamId, selectCaptureBatch, token]);

  // biome-ignore lint/correctness/useExhaustiveDependencies: selection 变化用于撤下上一考试或批次的结果，不能删除该依赖。
  useEffect(() => {
    clearProcessing();
    return () => { processingRequest.current += 1; };
  }, [selection, clearProcessing]);
  const handleRefreshProcessing = async () => {
    const owner = selectionRef.current;
    if (!owner.token || !owner.scope || !owner.batchId || !owner.examId) return;
    // 重新读取时也撤下旧关联；读取失败不能继续把旧答卷当作当前已核对的检查目标。
    clearProcessing();
    const request = ++processingRequest.current;
    setLoadingProcessing(true);
    const isCurrent = () => selectionRef.current === owner && request === processingRequest.current;
    try {
      const result = await getCaptureBatchDetail(owner.client, owner.batchId);
      if (!isCurrent()) return;
      if (result.batch.id !== owner.batchId || result.batch.exam_id !== owner.examId) throw new Error("处理结果与当前考试或批次不一致，请重新选择批次。");
      setProcessingSnapshot({ owner, detail: result });
      setCaptureBatches((current) => current.some((batch) => batch.id === result.batch.id) ? current : [...current, result.batch]);
      const submissionIDs = [...new Set(result.pages.map((page) => page.submission_id).filter((id): id is string => Boolean(id)))];
      setScanSubmissionId(submissionIDs[0] ?? "");
    } catch (error) { if (isCurrent()) setProcessingError(getUserErrorMessage(error, "服务端批次处理状态读取失败，请重试。")); }
    finally { if (isCurrent()) setLoadingProcessing(false); }
  };
  // 只在队列入口或会话选择变化时读取；响应回填不能再次触发同一请求。
  // biome-ignore lint/correctness/useExhaustiveDependencies: 请求使用本次 selection 快照，非稳定刷新函数不能作为自动读取触发器。
  useEffect(() => {
    if (queueFocus?.examId === selection.examId && queueFocus.batchId === selection.batchId && selection.token) {
      void handleRefreshProcessing();
    }
  }, [queueFocus, selection]);

  useEffect(() => {
    if (workspace === "scan" && token && selectedExamId) { void handleLoadCaptureBatches(selectedExamId); return; }
    captureBatchLoadRef.current += 1;
    if (!selectedExamId || !token) { setCaptureBatches([]); selectCaptureBatch(""); setIsLoadingCaptureBatches(false); }
  }, [handleLoadCaptureBatches, selectedExamId, selectCaptureBatch, token, workspace]);

  // 对外仍提供受控选择入口，不能直接暴露只更新 state 的 setter。
  return {
    exams, selectedExamId, setSelectedExamId: selectExam, examError, isLoadingExams,
    scanSubmissionId, setScanSubmissionId, captureBatchId, setCaptureBatchId: selectCaptureBatch,
    captureBatches, setCaptureBatches, isLoadingCaptureBatches, scanStartPage, setScanStartPage,
    handleLoadExams, handleLoadCaptureBatches, handleOpenQueueContext, processingDetail, linkedSubmissionIds, processingError, loadingProcessing, handleRefreshProcessing,
    scanContext, scanContextError, loadingScanContext, handleRefreshScanContext
  };
}
