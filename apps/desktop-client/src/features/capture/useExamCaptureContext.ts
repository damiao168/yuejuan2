import { useEffect, useRef, useState } from "react";
import type { DesktopApiClient } from "../../api/client";
import { listCaptureBatches, listExams, type CaptureBatch } from "../../api/exams";
import { getUserErrorMessage } from "../../api/userError";
import type { Exam, LocalLogEntry, WorkspaceKey } from "../../types";

type LogEvent = (level: LocalLogEntry["level"], message: string, context?: string) => Promise<void>;

export function useExamCaptureContext({
  client,
  token,
  durableScopeKey,
  workspace,
  logEvent
}: {
  client: DesktopApiClient;
  token: string | null;
  durableScopeKey: string;
  workspace: WorkspaceKey;
  logEvent: LogEvent;
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
  const captureBatchLoadRef = useRef(0);
  const examLoadRef = useRef(0);
  const scopeRef = useRef(durableScopeKey);
  scopeRef.current = durableScopeKey;

  useEffect(() => {
    examLoadRef.current += 1;
    captureBatchLoadRef.current += 1;
    setExams([]);
    setSelectedExamId("");
    setExamError(null);
    setIsLoadingExams(false);
    setScanSubmissionId("");
    setCaptureBatchId("");
    setCaptureBatches([]);
    setIsLoadingCaptureBatches(false);
    setScanStartPage(1);
  }, [durableScopeKey]);

  const handleLoadExams = async () => {
    const scope = durableScopeKey;
    if (!scope) return;
    const requestId = ++examLoadRef.current;
    setExamError(null);
    setIsLoadingExams(true);
    try {
      const result = await listExams(client, {});
      if (scopeRef.current !== scope || examLoadRef.current !== requestId) return;
      setExams(result.exams);
      setSelectedExamId((current) => current || result.exams[0]?.id || "");
      await logEvent("info", "exam list loaded for scan workstation", `${result.exams.length} exams`);
    } catch (error) {
      if (scopeRef.current !== scope || examLoadRef.current !== requestId) return;
      const message = getUserErrorMessage(error, "考试列表读取失败");
      setExamError(message);
      setExams([]);
      await logEvent("warning", "exam list load failed", message);
    } finally {
      if (scopeRef.current === scope && examLoadRef.current === requestId) setIsLoadingExams(false);
    }
  };

  const handleLoadCaptureBatches = async (examID = selectedExamId) => {
    const scope = durableScopeKey;
    if (!scope) return;
    const requestID = ++captureBatchLoadRef.current;
    if (!examID) {
      setCaptureBatches([]);
      setCaptureBatchId("");
      setIsLoadingCaptureBatches(false);
      return;
    }
    setIsLoadingCaptureBatches(true);
    try {
      const result = await listCaptureBatches(client, examID);
      if (scopeRef.current !== scope || requestID !== captureBatchLoadRef.current) return;
      const usable = result.batches.filter((batch) => batch.status !== "completed" && batch.status !== "cancelled");
      setCaptureBatches(usable);
      setCaptureBatchId((current) => usable.some((batch) => batch.id === current) ? current : usable[0]?.id ?? "");
      await logEvent("info", "capture batches loaded for scan workstation", `${usable.length} usable batches`);
    } catch (error) {
      if (scopeRef.current !== scope || requestID !== captureBatchLoadRef.current) return;
      const message = getUserErrorMessage(error, "采集批次读取失败");
      setCaptureBatches([]);
      setExamError(message);
      await logEvent("warning", "capture batch list load failed", message);
    } finally {
      if (scopeRef.current === scope && requestID === captureBatchLoadRef.current) setIsLoadingCaptureBatches(false);
    }
  };

  useEffect(() => {
    if (workspace === "scan" && token && selectedExamId) {
      void handleLoadCaptureBatches(selectedExamId);
      return;
    }
    captureBatchLoadRef.current += 1;
    if (!selectedExamId || !token) {
      setCaptureBatches([]);
      setCaptureBatchId("");
      setIsLoadingCaptureBatches(false);
    }
  }, [workspace, token, selectedExamId]);

  return {
    exams, selectedExamId, setSelectedExamId, examError, isLoadingExams,
    scanSubmissionId, setScanSubmissionId, captureBatchId, setCaptureBatchId,
    captureBatches, setCaptureBatches, isLoadingCaptureBatches, scanStartPage, setScanStartPage,
    handleLoadExams, handleLoadCaptureBatches
  };
}
