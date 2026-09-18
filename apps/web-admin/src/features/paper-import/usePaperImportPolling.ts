import { useEffect } from "react";
import { getPaperImport, type PaperImportJob } from "../../api/papers";

export interface PaperImportPollingOptions {
  examId: string;
  importId?: string;
  intervalMs?: number;
  onProgress(job: PaperImportJob): void;
  onSettled(examId: string): Promise<void>;
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
        onProgress(result.import);
        if (result.import.status !== "processing") await onSettled(examId);
      } catch {
        // Transient polling failures retain the last authoritative projection;
        // the next tick retries without rewriting the workflow state.
      } finally {
        pending = false;
      }
    },
    dispose() {
      disposed = true;
    }
  };
}

export function usePaperImportPolling({ examId, importId, intervalMs = 1000, onProgress, onSettled }: PaperImportPollingOptions) {
  useEffect(() => {
    if (!examId || !importId) return;
    const controller = createPaperImportPollController({ examId, importId, onProgress, onSettled });
    void controller.poll();
    const timer = window.setInterval(() => void controller.poll(), intervalMs);
    return () => {
      controller.dispose();
      window.clearInterval(timer);
    };
  }, [examId, importId, intervalMs, onProgress, onSettled]);
}
