import { useEffect, useRef, useState } from "react";
import { App } from "antd";
import { getUserErrorMessage } from "../../../api/client";
import { uploadFileWithProgress, type FileAsset } from "../../../api/files";
import { registerPaperFromFile, type PaperVersion } from "../../../api/papers";

export function useAnswerSheetSourceUpload({ examId, canManage, onCreated }: {
  examId: string; canManage: boolean; onCreated: (paper: PaperVersion) => void;
}) {
  const { message } = App.useApp();
  const context = useRef({ examId, canManage });
  context.current = { examId, canManage };
  const generation = useRef(0);
  const busy = useRef(false);
  const pendingAsset = useRef<FileAsset | undefined>(undefined);
  const [uploading, setUploading] = useState(false);
  const [progress, setProgress] = useState(0);
  const [error, setError] = useState<string>();
  const [canRetry, setCanRetry] = useState(false);

  useEffect(() => {
    generation.current++;
    busy.current = false;
    pendingAsset.current = undefined;
    setUploading(false); setProgress(0); setError(undefined); setCanRetry(false);
    return () => { generation.current++; };
  }, [examId]);

  async function run(file?: File) {
    if (!context.current.canManage || busy.current || (!file && !pendingAsset.current)) return;
    const requestExamId = examId;
    const requestGeneration = generation.current;
    const current = () => generation.current === requestGeneration && context.current.examId === requestExamId && context.current.canManage;
    busy.current = true;
    setUploading(true); setError(undefined); setProgress(0);
    try {
      if (file) {
        pendingAsset.current = undefined;
        setCanRetry(false);
        const result = await uploadFileWithProgress(file, { owner_type: "exam", owner_id: requestExamId, exam_id: requestExamId }, percent => {
          if (current()) setProgress(percent);
        });
        // 切换考试、账号或权限后不能继续提交旧文件，也不能把旧结果写入当前页面。
        if (!current()) return;
        pendingAsset.current = result.file;
      }
      const asset = pendingAsset.current;
      if (!asset || !current()) return;
      // 文件上传和答题卡登记分两步；登记失败保留资产 ID，重试只补登记，避免重复上传。
      const response = await registerPaperFromFile(requestExamId, asset.id, "answer_sheet");
      if (!current()) return;
      pendingAsset.current = undefined;
      setCanRetry(false);
      onCreated(response.paper);
      message.success("答题卡已上传，可以创建模板并设置答题区域");
    } catch (uploadError) {
      if (current()) {
        setError(getUserErrorMessage(uploadError, "答题卡上传失败，请重试。"));
        setCanRetry(Boolean(pendingAsset.current));
      }
    } finally {
      if (generation.current === requestGeneration) {
        busy.current = false;
        setUploading(false);
      }
    }
  }

  return { uploading, progress, error, canRetry, upload: (file: File) => run(file), retry: () => run() };
}
