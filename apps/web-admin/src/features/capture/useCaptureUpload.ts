import { useState } from "react";
import { App } from "antd";
import { registerCaptureFile, type CaptureBatchDetail } from "../../api/capture";
import { getUserErrorMessage } from "../../api/client";
import { uploadFile } from "../../api/files";

export function useCaptureUpload(detail: CaptureBatchDetail | undefined, examId: string, loadDetail: (batchId: string, quiet?: boolean) => Promise<void>) {
  const { message } = App.useApp();
  const [uploading, setUploading] = useState(false);
  async function uploadSource(file: File) {
    if (!detail) return;
    setUploading(true);
    try {
      const uploaded = await uploadFile(file, {
        owner_type: "capture_batch",
        owner_id: detail.batch.id,
        exam_id: examId,
      });
      await registerCaptureFile(
        detail.batch.id,
        uploaded.file.id,
        crypto.randomUUID(),
      );
      await loadDetail(detail.batch.id);
      message.success(`${file.name} 已加入批次`);
    } catch (currentError) {
      message.error(getUserErrorMessage(currentError, "操作失败，请重试"));
    } finally {
      setUploading(false);
    }
  }

  return { uploading, uploadSource };
}
