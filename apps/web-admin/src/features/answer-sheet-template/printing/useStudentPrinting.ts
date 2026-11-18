import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { App } from "antd";
import { ApiClientError, getSafeUserText, getUserErrorMessage } from "../../../api/client";
import type { AnswerSheetTemplate } from "../../../api/configuration";
import {
  downloadStudentPrintPackage,
  confirmStudentPrintLayout,
  getStudentPrintContext,
  issueStudentPrintBatch,
  reprintStudentSheet,
  type StudentPrintCandidate,
  type StudentPrintContext,
  type StudentPrintLayout
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

function printLayoutError(error: unknown): string | undefined {
  // 仅版式校验错误附带服务端预定义的处理说明，其他错误仍按通用规则翻译。
  if (!(error instanceof ApiClientError) || error.code !== "student_print_layout_unavailable") return undefined;
  return getSafeUserText(error.payload.error?.message, "答题卡底图或二维码位置无法打印，请核对模板和本批页数。");
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
  const [layoutRequest, setLayoutRequest] = useState<{ intent: "issue" | "reprint" | "download"; batchId?: string }>();
  const [layoutError, setLayoutError] = useState<string>();
  const confirmedLayoutRef = useRef<StudentPrintLayout | undefined>(undefined);
  const reprintCommandRef = useRef<{ serial: string; reason: string; key: string } | undefined>(undefined);
  const busyRef = useRef(false);
  // 每次切换模板或权限都会建立新作用域；切回同一个模板也不能接收切换前的迟到结果。
  const scope = useMemo(() => ({ templateId: selectedTemplate?.id, status: selectedTemplate?.status, canManage, active: true }),
    [selectedTemplate?.id, selectedTemplate?.status, canManage]);
  const currentScopeRef = useRef(scope);
  currentScopeRef.current = scope;
  const isCurrentScope = useCallback((requestScope: typeof scope) => requestScope.active && currentScopeRef.current === requestScope, []);
  useEffect(() => {
    scope.active = true;
    // 离开页面后也不能触发旧打印包下载；StrictMode 再次建立 effect 时恢复当前作用域。
    return () => { scope.active = false; };
  }, [scope]);

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
    const requestScope = currentScopeRef.current;
    if (!requestScope.active || requestScope.templateId !== templateId || !requestScope.canManage || requestScope.status !== "locked") return;
    const requestId = ++requestRef.current;
    setPrintContextLoading(true);
    setPrintContextError(undefined);
    try {
      const response = await getStudentPrintContext(templateId);
      if (requestId !== requestRef.current || !isCurrentScope(requestScope)) return;
      setPrintContext(response.print_context);
    } catch (error) {
      if (requestId !== requestRef.current || !isCurrentScope(requestScope)) return;
      setPrintContext(undefined);
      setPrintContextError(getUserErrorMessage(error, "操作失败，请稍后重试"));
    } finally {
      if (requestId === requestRef.current) setPrintContextLoading(false);
    }
  }, [isCurrentScope]);

  useEffect(() => {
    requestRef.current += 1;
    setPrintContext(undefined);
    setPrintContextError(undefined);
    setPrintModalOpen(false);
    setSelectedPrintClassIds([]);
    setReprintCandidate(undefined); setReprintReason(""); setReprintError(undefined);
    setDownloadPendingBatch(undefined); reprintCommandRef.current = undefined;
    setLayoutRequest(undefined); setLayoutError(undefined); confirmedLayoutRef.current = undefined;
    if (selectedTemplate?.status === "locked" && canManage) void loadPrintContext(selectedTemplate.id);
  }, [canManage, loadPrintContext, selectedTemplate?.id, selectedTemplate?.status]);

  const openPrintModal = () => {
    setSelectedPrintClassIds(printClasses.map((item) => item.id));
    setPrintModalOpen(true);
  };

  const downloadPrintBatch = async (printBatchId: string) => {
    const requestScope = currentScopeRef.current;
    if (busyRef.current || !requestScope.active || !requestScope.canManage || requestScope.status !== "locked") return;
    busyRef.current = true;
    setPrintBusy(true);
    try {
      const download = await downloadStudentPrintPackage(printBatchId);
      if (!isCurrentScope(requestScope)) return;
      saveDownload(download.blob, download.filename ?? `edugrade-answer-sheets-${printBatchId}.pdf`);
      message.success("打印包已下载，请按原始尺寸打印并保持页面顺序");
      setDownloadPendingBatch(current => current === printBatchId ? undefined : current);
    } catch (error) {
      if (!isCurrentScope(requestScope)) return;
      const layoutError = printLayoutError(error);
      if (error instanceof ApiClientError && error.code === "student_print_layout_confirmation_required") {
        setLayoutError(undefined);
        setDownloadPendingBatch(printBatchId);
        setLayoutRequest({ intent: "download", batchId: printBatchId });
      } else if (layoutError) {
        message.error(layoutError);
      } else if (error instanceof ApiClientError && error.status === 409) {
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

  const issuePrintPackage = async (selectedLayout?: StudentPrintLayout) => {
    if (!canManage || !selectedTemplate || !selectedPrintClassIds.length || busyRef.current) return false;
    const requestScope = currentScopeRef.current;
    if (!requestScope.active || requestScope.templateId !== selectedTemplate.id || !requestScope.canManage || requestScope.status !== "locked") return false;
    const selectedClasses = new Set(selectedPrintClassIds);
    const studentIds = printableCandidates.filter((item) => selectedClasses.has(item.class_id)).map((item) => item.student_id);
    if (!studentIds.length) { message.error("所选班级没有可签发的应考学生"); return false; }
    const cachedLayout = confirmedLayoutRef.current?.source_sha256 === printContext?.print_source?.sha256 ? confirmedLayoutRef.current : undefined;
    const qrLayout = selectedLayout ?? cachedLayout;
    if (printContext?.qr_layout_required && !qrLayout) {
      setLayoutError(undefined); setLayoutRequest({ intent: "issue" }); return false;
    }
    const signature = `${selectedTemplate.id}:${[...studentIds].sort((left, right) => left.localeCompare(right)).join(",")}:${JSON.stringify(qrLayout ?? null)}`;
    // 同一名单在签发结果未确认前复用请求键，避免网络失败后重试重复签发。
    if (issueRequestRef.current?.signature !== signature) {
      issueRequestRef.current = { signature, key: `web-print-${crypto.randomUUID()}` };
    }
    busyRef.current = true; setPrintBusy(true);
    let issuedBatch: string | undefined;
    try {
      const response = qrLayout
        ? await issueStudentPrintBatch(selectedTemplate.id, studentIds, issueRequestRef.current.key, qrLayout)
        : await issueStudentPrintBatch(selectedTemplate.id, studentIds, issueRequestRef.current.key);
      if (!isCurrentScope(requestScope)) return false;
      issuedBatch = response.barcodes.print_batch_id;
      setDownloadPendingBatch(issuedBatch);
      issueRequestRef.current = undefined;
      setPrintModalOpen(false);
      await loadPrintContext(selectedTemplate.id);
      if (!isCurrentScope(requestScope)) return false;
      const download = await downloadStudentPrintPackage(response.barcodes.print_batch_id);
      if (!isCurrentScope(requestScope)) return false;
      saveDownload(download.blob, download.filename ?? `edugrade-answer-sheets-${response.barcodes.print_batch_id}.pdf`);
      setDownloadPendingBatch(undefined);
      message.success(`已签发 ${response.barcodes.students.length} 份答题卡并下载打印包`);
    } catch (error) {
      if (!isCurrentScope(requestScope)) return false;
      const cause = printLayoutError(error) ?? (issuedBatch ? "答题卡已生成，下载暂未完成；请点击“下载已生成的打印包”，不要重新签发。" : getUserErrorMessage(error, "生成结果暂未确认，请刷新状态或重试同一名单"));
      message.error(cause);
      if (selectedLayout && !issuedBatch) setLayoutError(cause);
      await loadPrintContext(selectedTemplate.id);
    } finally {
      setPrintBusy(false);
      busyRef.current = false;
    }
    return Boolean(issuedBatch);
  };

  const openReprint = (candidate: StudentPrintCandidate) => {
    if (!canManage || busyRef.current || !candidate.active_sheet_serial || candidate.attendance_status !== "expected") return;
    setReprintCandidate(candidate); setReprintError(undefined);
    if (reprintCommandRef.current?.serial !== candidate.active_sheet_serial) {
      setReprintReason(""); reprintCommandRef.current = undefined;
    } else setReprintReason(reprintCommandRef.current.reason);
  };
  const reprint = async (selectedLayout?: StudentPrintLayout) => {
    const serial = reprintCandidate?.active_sheet_serial;
    const templateId = selectedTemplate?.id;
    if (!canManage || !serial || !templateId || busyRef.current) return false;
    const requestScope = currentScopeRef.current;
    if (!requestScope.active || requestScope.templateId !== templateId || !requestScope.canManage || requestScope.status !== "locked") return false;
    const reason = reprintReason.trim();
    if (!reason) { setReprintError("请填写补打原因，例如原卡遗失或打印损坏。"); return false; }
    const oldBatch = printContext?.batches.find(batch => batch.print_batch_id === reprintCandidate?.active_print_batch_id);
    const cachedLayout = confirmedLayoutRef.current?.source_sha256 === printContext?.print_source?.sha256 ? confirmedLayoutRef.current : undefined;
    const qrLayout = selectedLayout ?? oldBatch?.qr_layout ?? cachedLayout;
    if (printContext?.qr_layout_required && !qrLayout) {
      setLayoutError(undefined); setLayoutRequest({ intent: "reprint" }); return false;
    }
    // 后端在一个事务中签发新卡并作废旧卡；不能先单独作废，避免签发失败后学生无卡可用。
    // 结果不确定时冻结同一理由与请求键，重试只完成原补打，不生成另一张卡。
    reprintCommandRef.current ??= { serial, reason, key: `web-reprint-${crypto.randomUUID()}` };
    const command = reprintCommandRef.current;
    busyRef.current = true; setPrintBusy(true); setReprintError(undefined);
    let created = false;
    try {
      const response = qrLayout
        ? await reprintStudentSheet(command.serial, command.reason, command.key, qrLayout)
        : await reprintStudentSheet(command.serial, command.reason, command.key);
      if (!isCurrentScope(requestScope)) return false;
      created = true;
      const batchId = response.barcodes.print_batch_id;
      setDownloadPendingBatch(batchId); setReprintCandidate(undefined);
      reprintCommandRef.current = undefined;
      await loadPrintContext(templateId);
      if (!isCurrentScope(requestScope)) return false;
      const download = await downloadStudentPrintPackage(batchId);
      if (!isCurrentScope(requestScope)) return false;
      saveDownload(download.blob, download.filename ?? "学生补打答题卡.pdf");
      setDownloadPendingBatch(undefined);
      message.success("新答题卡已生成并下载，旧卡已作废；请收回旧卡，避免再次扫描。");
    } catch (error) {
      if (!isCurrentScope(requestScope)) return false;
      const layoutError = printLayoutError(error);
      if (layoutError) {
        if (created) message.warning(layoutError);
        else setReprintError(layoutError);
      } else if (created) message.warning("旧卡已作废、新卡已生成，下载未完成；请下载已生成的打印包。");
      else setReprintError(getUserErrorMessage(error, "结果暂未确认，请保持当前原因并重试；系统会复用本次补打请求。"));
      if (selectedLayout && !created) setLayoutError(layoutError ?? getUserErrorMessage(error, "打印位置尚未确认，请核对后重试"));
    } finally {
      busyRef.current = false; setPrintBusy(false);
    }
    return created;
  };

  const confirmLayout = async (layout: StudentPrintLayout) => {
    if (!canManage || !selectedTemplate || busyRef.current || !layoutRequest || !printContext?.print_source
      || layout.source_sha256 !== printContext.print_source.sha256) return;
    const request = layoutRequest;
    const templateId = selectedTemplate.id;
    const requestScope = currentScopeRef.current;
    if (!requestScope.active || requestScope.templateId !== templateId || !requestScope.canManage || requestScope.status !== "locked") return;
    setLayoutError(undefined);
    if (request.intent !== "download") {
      // 确认只属于这份源文件和当前模板，换模板后不能复用旧的可覆盖区域。
      const issued = request.intent === "issue" ? await issuePrintPackage(layout) : await reprint(layout);
      if (issued && isCurrentScope(requestScope)) {
        confirmedLayoutRef.current = layout;
        setLayoutRequest(undefined);
      }
      return;
    }
    if (!request.batchId) return;
    busyRef.current = true; setPrintBusy(true);
    try {
      await confirmStudentPrintLayout(request.batchId, layout);
      if (!isCurrentScope(requestScope)) return;
      setLayoutRequest(undefined);
      await loadPrintContext(templateId);
    } catch (error) {
      if (isCurrentScope(requestScope)) setLayoutError(printLayoutError(error) ?? getUserErrorMessage(error, "打印位置未保存，请重新核对后重试"));
      return;
    } finally {
      busyRef.current = false; setPrintBusy(false);
    }
    if (isCurrentScope(requestScope)) await downloadPrintBatch(request.batchId);
  };

  return {
    printContext, printContextLoading, printContextError, printModalOpen,
    setPrintModalOpen, selectedPrintClassIds, setSelectedPrintClassIds,
    printBusy, printableCandidates, printClasses, loadPrintContext,
    openPrintModal, downloadPrintBatch, issuePrintPackage,
    reprintCandidate, reprintReason, setReprintReason, reprintError,
    reprintReasonFrozen: Boolean(reprintCommandRef.current), downloadPendingBatch,
    layoutRequest, layoutError, confirmLayout,
    closeLayout: () => { if (!busyRef.current) { setLayoutRequest(undefined); setLayoutError(undefined); } },
    openReprint, reprint, closeReprint: () => { if (!busyRef.current) setReprintCandidate(undefined); }
  };
}
