import { useEffect } from "react";
import { getPaperImport, type PaperImportJob } from "../../api/papers";

export interface PaperImportPollingOptions {
  examId: string;
  importId?: string;
  intervalMs?: number;
  onProgress(job: PaperImportJob): void;
  onSettled(examId: string): Promise<void>;
  onConnectionError?: (error?: unknown) => void;
  retryKey?: number;
}

export interface PaperImportPollControllerOptions extends PaperImportPollingOptions {
  getImport?: typeof getPaperImport;
}

export interface PaperImportPollController {
  poll(): Promise<void>;
  dispose(): void;
}

export function createPaperImportPollController({
  examId,
  importId,
  onProgress,
  onSettled,
  onConnectionError,
  getImport = getPaperImport
}: PaperImportPollControllerOptions): PaperImportPollController {
  let pending = false;
  let disposed = false;

  return {
    async poll() {
      if (!examId || !importId || pending || disposed) return;
      pending = true;
      try {
        const result = await getImport(importId);
        if (disposed) return;
        onConnectionError?.();
        onProgress(result.import);
        if (result.import.status !== "processing") await onSettled(examId);
      } catch (error) {
        // 暂时的轮询失败保留最后一次权威投影；
        // 下一轮继续重试，不改写流程状态；同时显示断线事实，旧进度不能冒充持续处理。
        if (!disposed) onConnectionError?.(error);
      } finally {
        pending = false;
      }
    },
    dispose() {
      disposed = true;
    }
  };
}

export function usePaperImportPolling({ examId, importId, intervalMs = 1000, onProgress, onSettled, onConnectionError, retryKey }: PaperImportPollingOptions) {
  useEffect(() => {
    if (!examId || !importId) return;
    const controller = createPaperImportPollController({ examId, importId, onProgress, onSettled, onConnectionError });
    void controller.poll();
    const timer = window.setInterval(() => void controller.poll(), intervalMs);
    return () => {
      controller.dispose();
      window.clearInterval(timer);
    };
  }, [examId, importId, intervalMs, onProgress, onSettled, onConnectionError, retryKey]);
}
