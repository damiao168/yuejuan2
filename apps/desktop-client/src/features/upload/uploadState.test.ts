import { describe, expect, it } from "vitest";
import type { SyncQueueItem } from "../../types";
import { applyUploadState, uploadStateFromQueueItem } from "./uploadState";

function item(status: SyncQueueItem["status"] = "pending"): SyncQueueItem {
  return {
    id: "scan-1", title: "page.tif", kind: "scan_upload", status,
    progress: 0, detail: "queued", updatedAt: "2026-01-01T00:00:00.000Z"
  };
}

describe("upload state", () => {
  it("models upload progress and a successful durable projection", () => {
    const uploading = applyUploadState(item(), { status: "uploading", progress: 42 });
    const succeeded = applyUploadState(uploading, { status: "succeeded", fileAssetId: "asset-1" });

    expect(uploadStateFromQueueItem(uploading)).toEqual({ status: "uploading", progress: 42 });
    expect(succeeded).toMatchObject({ status: "succeeded", progress: 100, fileAssetId: "asset-1" });
  });

  it("keeps failure and conflict reasons explicit", () => {
    expect(uploadStateFromQueueItem(applyUploadState(item(), { status: "failed", error: "network" })))
      .toEqual({ status: "failed", error: "network" });
    expect(uploadStateFromQueueItem(applyUploadState(item(), { status: "conflict", reason: "revision" })))
      .toEqual({ status: "conflict", reason: "revision" });
  });

  it("allows retry from failed and rejects reopening success", () => {
    const failed = applyUploadState(item(), { status: "failed", error: "offline" });
    expect(applyUploadState(failed, { status: "pending" }).status).toBe("pending");
    const succeeded = applyUploadState(item("uploading"), { status: "succeeded", fileAssetId: "asset-1" });
    expect(() => applyUploadState(succeeded, { status: "uploading", progress: 1 })).toThrow("succeeded -> uploading");
  });
});
