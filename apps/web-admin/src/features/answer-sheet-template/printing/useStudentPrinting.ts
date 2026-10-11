import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { App } from "antd";
import { ApiClientError, getUserErrorMessage } from "../../../api/client";
import type { AnswerSheetTemplate } from "../../../api/configuration";
import {
  downloadStudentPrintPackage,
  getStudentPrintContext,
  issueStudentPrintBatch,
  reprintStudentSheet,
  type StudentPrintCandidate,
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
  const [reprintCandidate, setReprintCandidate] = useState<StudentPrintCandidate>();
  const [reprintReason, setReprintReason] = useState("");
  const [reprintError, setReprintError] = useState<string>();
  const [downloadPendingBatch, setDownloadPendingBatch] = useState<string>();
  const reprintCommandRef = useRef<{ serial: string; reason: string; key: string } | undefined>(undefined);
  const busyRef = useRef(false);
  const templateScopeRef = useRef(selectedTemplate?.id);
  templateScopeRef.current = selectedTemplate?.id;

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
    if (templateScopeRef.current !== templateId) return;
    const requestId = ++requestRef.current;
    setPrintContextLoading(true);
    setPrintContextError(undefined);
    try {
      const response = await getStudentPrintContext(templateId);
      if (requestId !== requestRef.current || templateScopeRef.current !== templateId) return;
      setPrintContext(response.print_context);
    } catch (error) {
      if (requestId !== requestRef.current || templateScopeRef.current !== templateId) return;
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
    setReprintCandidate(undefined); setReprintReason(""); setReprintError(undefined);
    setDownloadPendingBatch(undefined); reprintCommandRef.current = undefined;
    if (selectedTemplate?.status === "locked" && canManage) void loadPrintContext(selectedTemplate.id);
  }, [canManage, loadPrintContext, selectedTemplate?.id, selectedTemplate?.status]);

  const openPrintModal = () => {
    setSelectedPrintClassIds(printClasses.map((item) => item.id));
    setPrintModalOpen(true);
  };

  const downloadPrintBatch = async (printBatchId: string) => {
    if (busyRef.current) return;
    const templateId = selectedTemplate?.id;
    busyRef.current = true;
    setPrintBusy(true);
    try {
      const download = await downloadStudentPrintPackage(printBatchId);
      if (templateScopeRef.current !== templateId) return;
      saveDownload(download.blob, download.filename ?? `edugrade-answer-sheets-${printBatchId}.pdf`);
      message.success("打印包已下载，请按原始尺寸打印并保持页面顺序");
      setDownloadPendingBatch(current => current === printBatchId ? undefined : current);
    } catch (error) {
      if (templateScopeRef.current !== templateId) return;
      if (error instanceof ApiClientError && error.status === 409) {
        message.error("该批次已扫描或作废，不能再次下载；请在学生打印记录中选择“作废旧卡并补打”");
        if (selectedTemplate) await loadPrintContext(selectedTemplate.id);
      } else {
        message.error(getUserErrorMessage(error, "操作失败，请稍后重试"));
      }
    } finally {
      setPrintBusy(false);
      busyRef.current = false;
    }
  };

  const issuePrintPackage = async () => {
    if (!canManage || !selectedTemplate || !selectedPrintClassIds.length || busyRef.current) return;
    const selectedClasses = new Set(selectedPrintClassIds);
    const studentIds = printableCandidates.filter((item) => selectedClasses.has(item.class_id)).map((item) => item.student_id);
    if (!studentIds.length) { message.error("所选班级没有可签发的应考学生"); return; }
    const signature = `${selectedTemplate.id}:${[...studentIds].sort((left, right) => left.localeCompare(right)).join(",")}`;
    // 同一名单在签发结果未确认前复用请求键，避免网络失败后重试重复签发。
    if (issueRequestRef.current?.signature !== signature) {
      issueRequestRef.current = { signature, key: `web-print-${crypto.randomUUID()}` };
    }
    busyRef.current = true; setPrintBusy(true);
    let issuedBatch: string | undefined;
    try {
      const response = await issueStudentPrintBatch(selectedTemplate.id, studentIds, issueRequestRef.current.key);
      if (templateScopeRef.current !== selectedTemplate.id) return;
      issuedBatch = response.barcodes.print_batch_id;
      setDownloadPendingBatch(issuedBatch);
      issueRequestRef.current = undefined;
      setPrintModalOpen(false);
      await loadPrintContext(selectedTemplate.id);
      if (templateScopeRef.current !== selectedTemplate.id) return;
      const download = await downloadStudentPrintPackage(response.barcodes.print_batch_id);
      if (templateScopeRef.current !== selectedTemplate.id) return;
      saveDownload(download.blob, download.filename ?? `edugrade-answer-sheets-${response.barcodes.print_batch_id}.pdf`);
      setDownloadPendingBatch(undefined);
      message.success(`已签发 ${response.barcodes.students.length} 份答题卡并下载打印包`);
    } catch (error) {
      if (templateScopeRef.current !== selectedTemplate.id) return;
      message.error(issuedBatch ? "答题卡已生成，下载暂未完成；请点击“下载已生成的打印包”，不要重新签发。" : getUserErrorMessage(error, "生成结果暂未确认，请刷新状态或重试同一名单"));
      await loadPrintContext(selectedTemplate.id);
    } finally {
      setPrintBusy(false);
      busyRef.current = false;
    }
  };

  const openReprint = (candidate: StudentPrintCandidate) => {
    if (!canManage || busyRef.current || !candidate.active_sheet_serial || candidate.attendance_status !== "expected") return;
    setReprintCandidate(candidate); setReprintError(undefined);
    if (reprintCommandRef.current?.serial !== candidate.active_sheet_serial) {
      setReprintReason(""); reprintCommandRef.current = undefined;
    } else setReprintReason(reprintCommandRef.current.reason);
  };
  const reprint = async () => {
    const serial = reprintCandidate?.active_sheet_serial;
    const templateId = selectedTemplate?.id;
    if (!canManage || !serial || !templateId || busyRef.current) return;
    const reason = reprintReason.trim();
    if (!reason) { setReprintError("请填写补打原因，例如原卡遗失或打印损坏。"); return; }
    // 后端在一个事务中签发新卡并作废旧卡；不能先单独作废，避免签发失败后学生无卡可用。
    // 结果不确定时冻结同一理由与请求键，重试只完成原补打，不生成另一张卡。
    reprintCommandRef.current ??= { serial, reason, key: `web-reprint-${crypto.randomUUID()}` };
    const command = reprintCommandRef.current;
    busyRef.current = true; setPrintBusy(true); setReprintError(undefined);
    let created = false;
    try {
      const response = await reprintStudentSheet(command.serial, command.reason, command.key);
      if (templateScopeRef.current !== templateId) return;
      created = true;
      const batchId = response.barcodes.print_batch_id;
      setDownloadPendingBatch(batchId); setReprintCandidate(undefined);
      reprintCommandRef.current = undefined;
      await loadPrintContext(templateId);
      if (templateScopeRef.current !== templateId) return;
      const download = await downloadStudentPrintPackage(batchId);
      if (templateScopeRef.current !== templateId) return;
      saveDownload(download.blob, download.filename ?? "学生补打答题卡.pdf");
      setDownloadPendingBatch(undefined);
      message.success("新答题卡已生成并下载，旧卡已作废；请收回旧卡，避免再次扫描。");
    } catch (error) {
      if (templateScopeRef.current !== templateId) return;
      if (created) message.warning("旧卡已作废、新卡已生成，下载未完成；请下载已生成的打印包。");
      else setReprintError(getUserErrorMessage(error, "结果暂未确认，请保持当前原因并重试；系统会复用本次补打请求。"));
    } finally {
      busyRef.current = false; setPrintBusy(false);
    }
  };

  return {
    printContext, printContextLoading, printContextError, printModalOpen,
    setPrintModalOpen, selectedPrintClassIds, setSelectedPrintClassIds,
    printBusy, printableCandidates, printClasses, loadPrintContext,
    openPrintModal, downloadPrintBatch, issuePrintPackage,
    reprintCandidate, reprintReason, setReprintReason, reprintError,
    reprintReasonFrozen: Boolean(reprintCommandRef.current), downloadPendingBatch,
    openReprint, reprint, closeReprint: () => { if (!busyRef.current) setReprintCandidate(undefined); }
  };
}
