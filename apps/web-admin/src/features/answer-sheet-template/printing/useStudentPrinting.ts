import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { App } from "antd";
import { ApiClientError, getUserErrorMessage } from "../../../api/client";
import type { AnswerSheetTemplate } from "../../../api/configuration";
import {
  downloadStudentPrintPackage,
  getStudentPrintContext,
  issueStudentPrintBatch,
  type StudentPrintContext
} from "../../../api/printing";

function saveDownload(blob: Blob, filename: string) {
  const url = URL.createObjectURL(blob);
  const link = document.createElement("a");
  link.href = url;
  link.download = filename;
  document.body.appendChild(link);
  link.click();
  link.remove();
  window.setTimeout(() => URL.revokeObjectURL(url), 1000);
}

export function useStudentPrinting(selectedTemplate: AnswerSheetTemplate | undefined, canManage: boolean) {
  const { message } = App.useApp();
  const requestRef = useRef(0);
  const issueRequestRef = useRef<{ signature: string; key: string } | undefined>(undefined);
  const [printContext, setPrintContext] = useState<StudentPrintContext>();
  const [printContextLoading, setPrintContextLoading] = useState(false);
  const [printContextError, setPrintContextError] = useState<string>();
  const [printModalOpen, setPrintModalOpen] = useState(false);
  const [selectedPrintClassIds, setSelectedPrintClassIds] = useState<string[]>([]);
  const [printBusy, setPrintBusy] = useState(false);

  const printableCandidates = useMemo(
    () => printContext?.candidates.filter((item) => item.attendance_status === "expected" && !item.has_active_sheet) ?? [],
    [printContext]
  );
  const printClasses = useMemo(() => {
    const grouped = new Map<string, { id: string; name: string; count: number }>();
    for (const candidate of printableCandidates) {
      const current = grouped.get(candidate.class_id);
      if (current) current.count += 1;
      else grouped.set(candidate.class_id, { id: candidate.class_id, name: candidate.class_name, count: 1 });
    }
    return Array.from(grouped.values());
  }, [printableCandidates]);

  const loadPrintContext = useCallback(async (templateId: string) => {
    const requestId = ++requestRef.current;
    setPrintContextLoading(true);
    setPrintContextError(undefined);
    try {
      const response = await getStudentPrintContext(templateId);
      if (requestId !== requestRef.current) return;
      setPrintContext(response.print_context);
    } catch (error) {
      if (requestId !== requestRef.current) return;
      setPrintContext(undefined);
      setPrintContextError(getUserErrorMessage(error, "操作失败，请稍后重试"));
    } finally {
      if (requestId === requestRef.current) setPrintContextLoading(false);
    }
  }, []);

  useEffect(() => {
    requestRef.current += 1;
    setPrintContext(undefined);
    setPrintContextError(undefined);
    setPrintModalOpen(false);
    setSelectedPrintClassIds([]);
    if (selectedTemplate?.status === "locked" && canManage) void loadPrintContext(selectedTemplate.id);
  }, [canManage, loadPrintContext, selectedTemplate?.id, selectedTemplate?.status]);

  const openPrintModal = () => {
    setSelectedPrintClassIds(printClasses.map((item) => item.id));
    setPrintModalOpen(true);
  };

  const downloadPrintBatch = async (printBatchId: string) => {
    setPrintBusy(true);
    try {
      const download = await downloadStudentPrintPackage(printBatchId);
      saveDownload(download.blob, download.filename ?? `edugrade-answer-sheets-${printBatchId}.pdf`);
      message.success("打印包已下载，请按原始尺寸打印并保持页面顺序");
    } catch (error) {
      if (error instanceof ApiClientError && error.status === 409) {
        message.error("该批次已有答卷被扫描或已作废，不能再次下载；补打请走作废与重印流程");
        if (selectedTemplate) await loadPrintContext(selectedTemplate.id);
      } else {
        message.error(getUserErrorMessage(error, "操作失败，请稍后重试"));
      }
    } finally {
      setPrintBusy(false);
    }
  };

  const issuePrintPackage = async () => {
    if (!selectedTemplate || !selectedPrintClassIds.length) return;
    const selectedClasses = new Set(selectedPrintClassIds);
    const studentIds = printableCandidates.filter((item) => selectedClasses.has(item.class_id)).map((item) => item.student_id);
    if (!studentIds.length) { message.error("所选班级没有可签发的应考学生"); return; }
    const signature = [...studentIds].sort().join(",");
    if (issueRequestRef.current?.signature !== signature) {
      issueRequestRef.current = { signature, key: `web-print-${crypto.randomUUID()}` };
    }
    setPrintBusy(true);
    try {
      const response = await issueStudentPrintBatch(selectedTemplate.id, studentIds, issueRequestRef.current.key);
      issueRequestRef.current = undefined;
      setPrintModalOpen(false);
      await loadPrintContext(selectedTemplate.id);
      const download = await downloadStudentPrintPackage(response.barcodes.print_batch_id);
      saveDownload(download.blob, download.filename ?? `edugrade-answer-sheets-${response.barcodes.print_batch_id}.pdf`);
      message.success(`已签发 ${response.barcodes.students.length} 份答题卡并下载打印包`);
    } catch (error) {
      message.error(getUserErrorMessage(error, "操作失败，请稍后重试"));
      await loadPrintContext(selectedTemplate.id);
    } finally {
      setPrintBusy(false);
    }
  };

  return {
    printContext, printContextLoading, printContextError, printModalOpen,
    setPrintModalOpen, selectedPrintClassIds, setSelectedPrintClassIds,
    printBusy, printableCandidates, printClasses, loadPrintContext,
    openPrintModal, downloadPrintBatch, issuePrintPackage
  };
}
