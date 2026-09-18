import { describe, expect, it, vi } from "vitest";
import type { PaperImportJob } from "../../api/papers";
import { createPaperImportPollController } from "./usePaperImportPolling";

function job(status: PaperImportJob["status"]): PaperImportJob {
  return { id: "import-1", exam_id: "exam-1", status } as PaperImportJob;
}

describe("paper import poll controller", () => {
  it("publishes the initial processing projection without settling", async () => {
    const onProgress = vi.fn();
    const onSettled = vi.fn();
    const controller = createPaperImportPollController({
      examId: "exam-1", importId: "import-1", onProgress, onSettled,
      getImport: vi.fn().mockResolvedValue({ import: job("processing") })
    });

    await controller.poll();

    expect(onProgress).toHaveBeenCalledWith(expect.objectContaining({ status: "processing" }));
    expect(onSettled).not.toHaveBeenCalled();
  });

  it("retains state after an API failure and retries on the next tick", async () => {
    const onProgress = vi.fn();
    const getImport = vi.fn()
      .mockRejectedValueOnce(new Error("temporary"))
      .mockResolvedValueOnce({ import: job("review_required") });
    const onSettled = vi.fn().mockResolvedValue(undefined);
    const controller = createPaperImportPollController({ examId: "exam-1", importId: "import-1", onProgress, onSettled, getImport });

    await controller.poll();
    expect(onProgress).not.toHaveBeenCalled();
    await controller.poll();

    expect(getImport).toHaveBeenCalledTimes(2);
    expect(onProgress).toHaveBeenCalledWith(expect.objectContaining({ status: "review_required" }));
    expect(onSettled).toHaveBeenCalledWith("exam-1");
  });

  it("prevents overlapping commands and ignores a stale response after disposal", async () => {
    let resolve!: (value: { import: PaperImportJob }) => void;
    const getImport = vi.fn(() => new Promise<{ import: PaperImportJob }>((done) => { resolve = done; }));
    const onProgress = vi.fn();
    const controller = createPaperImportPollController({
      examId: "exam-1", importId: "import-1", onProgress,
      onSettled: vi.fn().mockResolvedValue(undefined), getImport
    });

    const first = controller.poll();
    await controller.poll();
    expect(getImport).toHaveBeenCalledTimes(1);
    controller.dispose();
    resolve({ import: job("applied") });
    await first;

    expect(onProgress).not.toHaveBeenCalled();
  });
});
