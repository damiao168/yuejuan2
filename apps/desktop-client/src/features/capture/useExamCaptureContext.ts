import { useEffect, useRef, useState } from "react";
import type { DesktopApiClient } from "../../api/client";
import { listCaptureBatches, listExams, type CaptureBatch } from "../../api/exams";
import { getUserErrorMessage } from "../../api/userError";
import type { Exam, LocalLogEntry, WorkspaceKey } from "../../types";

type LogEvent = (level: LocalLogEntry["level"], message: string, context?: string) => Promise<void>;

export function useExamCaptureContext({
  client,
  token,
  workspace,
  logEvent
}: {
  client: DesktopApiClient;
  token: string | null;
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

  const handleLoadExams = async () => {
    setExamError(null);
    setIsLoadingExams(true);
    try {
      const result = await listExams(client, {});
      setExams(result.exams);
      setSelectedExamId((current) => current || result.exams[0]?.id || "");
      await logEvent("info", "exam list loaded for scan workstation", `${result.exams.length} exams`);
    } catch (error) {
      const message = getUserErrorMessage(error, "考试列表读取失败");
      setExamError(message);
      setExams([]);
      await logEvent("warning", "exam list load failed", message);
    } finally {
      setIsLoadingExams(false);
    }
  };

  const handleLoadCaptureBatches = async (examID = selectedExamId) => {
    const requestID = ++captureBatchLoadRef.current;
    if (!examID) { setCaptureBatches([]); return; }
    setIsLoadingCaptureBatches(true);
    try {
      const result = await listCaptureBatches(client, examID);
      if (requestID !== captureBatchLoadRef.current) return;
      const usable = result.batches.filter((batch) => batch.status !== "completed" && batch.status !== "cancelled");
      setCaptureBatches(usable);
      setCaptureBatchId((current) => usable.some((batch) => batch.id === current) ? current : usable[0]?.id ?? "");
      await logEvent("info", "capture batches loaded for scan workstation", `${usable.length} usable batches`);
    } catch (error) {
      if (requestID !== captureBatchLoadRef.current) return;
      const message = getUserErrorMessage(error, "采集批次读取失败");
      setCaptureBatches([]);
      setExamError(message);
      await logEvent("warning", "capture batch list load failed", message);
    } finally {
      if (requestID === captureBatchLoadRef.current) setIsLoadingCaptureBatches(false);
    }
  };

  useEffect(() => {
    if (workspace === "scan" && token && selectedExamId) void handleLoadCaptureBatches(selectedExamId);
  }, [workspace, token, selectedExamId]);

  return {
    exams, selectedExamId, setSelectedExamId, examError, isLoadingExams,
    scanSubmissionId, setScanSubmissionId, captureBatchId, setCaptureBatchId,
    captureBatches, setCaptureBatches, isLoadingCaptureBatches, scanStartPage, setScanStartPage,
    handleLoadExams, handleLoadCaptureBatches
  };
}
