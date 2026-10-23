import { useEffect, useRef, useState } from "react";
import { App } from "antd";
import { registerCaptureFile, type CaptureBatchDetail } from "../../api/capture";
import { getUserErrorMessage } from "../../api/client";
import { uploadFile } from "../../api/files";

interface UploadGroup {
  id: string;
  batchId: string;
  examId: string;
  pagesPerStudent: number;
  files: Array<{ file: File; commandId: string; assetId?: string; registeredId?: string }>;
  nextIndex: number;
  error?: string;
  onRegistered?: (fileIds: string[]) => Promise<void>;
}

export interface PendingCaptureUpload {
  totalFiles: number;
  completedFiles: number;
  pagesPerStudent: number;
  error?: string;
}

export function useCaptureUpload(detail: CaptureBatchDetail | undefined, examId: string, loadDetail: (batchId: string, quiet?: boolean) => Promise<void>, selectedId = detail?.batch.id ?? "") {
  const { message } = App.useApp();
  const [uploading, setUploading] = useState(false);
  const [, setRevision] = useState(0);
  const busy = useRef(false);
  const mounted = useRef(true);
  const scope = `${examId}:${selectedId}`;
  const activeScope = useRef(scope);
  activeScope.current = scope;
  // 每个批次保留本次选择及已成功步骤；切回后重试仍沿用原组和幂等键，不能拿新选择补齐旧组。
  const pendingGroups = useRef(new Map<string, UploadGroup>());
  useEffect(() => {
    mounted.current = true;
    return () => { mounted.current = false; };
  }, []);

  async function runGroup(group: UploadGroup): Promise<string[]> {
    if (busy.current) return [];
    busy.current = true;
    setUploading(true);
    group.error = undefined;
    const groupScope = `${group.examId}:${group.batchId}`;
    const isCurrent = () => mounted.current && activeScope.current === groupScope;
    try {
      while (group.nextIndex < group.files.length) {
        if (!isCurrent()) return [];
        const item = group.files[group.nextIndex];
        if (!item.assetId) {
          const uploaded = await uploadFile(item.file, { owner_type: "capture_batch", owner_id: group.batchId, exam_id: group.examId });
          item.assetId = uploaded.file.id;
        }
        if (!isCurrent()) return [];
        // 上传成功而登记响应丢失时复用 asset 和命令键，避免重试制造第二个文件位置。
        const registered = await registerCaptureFile(group.batchId, item.assetId, item.commandId, {
          upload_group_id: group.id,
          upload_group_file_count: group.files.length,
          upload_group_file_index: group.nextIndex + 1,
          pages_per_student: group.pagesPerStudent,
        });
        if (!registered.file?.id) throw new Error("未收到文件登记结果，请重试本次上传");
        item.registeredId = registered.file.id;
        group.nextIndex += 1;
        if (isCurrent()) setRevision(value => value + 1);
      }
      const fileIds = group.files.map(item => item.registeredId!);
      if (!isCurrent()) return [];
      // 替代材料先完整登记，再退出旧组；后续绑定失败时保留原组和新文件标识，重试不再重传。
      if (group.onRegistered) await group.onRegistered(fileIds);
      pendingGroups.current.delete(groupScope);
      if (isCurrent()) message.success(`${group.files.length} 个文件已加入批次，每 ${group.pagesPerStudent} 页组成一份学生答卷，等待拆页校验`);
      return fileIds;
    } catch (currentError) {
      group.error = getUserErrorMessage(currentError, "上传失败，请重试本次上传");
      if (isCurrent()) message.error(group.error);
      return [];
    } finally {
      try {
        if (isCurrent()) await loadDetail(group.batchId, true);
      } catch (refreshError) {
        if (isCurrent()) message.error(getUserErrorMessage(refreshError, "批次刷新失败，请重试读取批次"));
      } finally {
        // 刷新失败也必须释放上传锁；已登记的文件和同组重试进度仍保留。
        busy.current = false;
        if (mounted.current) { setUploading(false); setRevision(value => value + 1); }
      }
    }
  }

  async function uploadSources(files: readonly File[], pagesPerStudent: number, onRegistered?: (fileIds: string[]) => Promise<void>): Promise<string[]> {
    if (!detail || detail.batch.id !== selectedId || busy.current || !files.length) return [];
    if (pendingGroups.current.has(scope)) { message.error("请先重试本次上传，或结束本次上传后再选择完整答卷"); return []; }
    if (!Number.isInteger(pagesPerStudent) || pagesPerStudent < 1 || pagesPerStudent > 20) { message.error("请先选择每名学生答题卡页数（1–20 页）"); return []; }
    const onlySinglePageImages = files.every(file => /\.(png|jpe?g)$/i.test(file.name) || ["image/png", "image/jpeg"].includes(file.type));
    if (onlySinglePageImages && files.length % pagesPerStudent !== 0) {
      message.error(`本次选择 ${files.length} 页，每名学生 ${pagesPerStudent} 页，缺少 ${pagesPerStudent - files.length % pagesPerStudent} 页；请一次选择完整答卷`);
      return [];
    }
    const group: UploadGroup = {
      id: crypto.randomUUID(), batchId: selectedId, examId, pagesPerStudent,
      files: files.map(file => ({ file, commandId: crypto.randomUUID() })), nextIndex: 0,
      onRegistered,
    };
    pendingGroups.current.set(scope, group);
    return runGroup(group);
  }

  async function retryUpload() {
    const group = pendingGroups.current.get(scope);
    if (detail?.batch.id === selectedId && group) await runGroup(group);
  }

  function discardUpload() {
    if (busy.current) return;
    pendingGroups.current.delete(scope);
    setRevision(value => value + 1);
    message.info("已结束本次上传。已登记的旧组保留原记录，新选择会独立分组，不会补入旧组");
  }

  const group = pendingGroups.current.get(scope);
  const pendingUpload: PendingCaptureUpload | undefined = group ? {
    totalFiles: group.files.length, completedFiles: group.nextIndex, pagesPerStudent: group.pagesPerStudent, error: group.error,
  } : undefined;
  return { uploading, pendingUpload, uploadSources, retryUpload, discardUpload };
}
