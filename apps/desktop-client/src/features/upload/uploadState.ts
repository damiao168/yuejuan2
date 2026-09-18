import { transitionScanQueueItem } from "../../lib/scanQueueWorkflow";
import type { SyncQueueItem, UploadState } from "../../types";

export function uploadStateFromQueueItem(item: SyncQueueItem): UploadState {
  switch (item.status) {
    case "uploading":
      return { status: "uploading", progress: item.progress };
    case "failed":
      return { status: "failed", error: item.detail };
    case "conflict":
      return { status: "conflict", reason: item.detail };
    case "succeeded":
      return { status: "succeeded", fileAssetId: item.fileAssetId ?? "" };
    case "pending":
    case "not_configured":
      return { status: "pending" };
  }
}

export function applyUploadState(item: SyncQueueItem, state: UploadState, now = new Date()): SyncQueueItem {
  switch (state.status) {
    case "pending":
      return transitionScanQueueItem(item, { status: "pending", progress: 0 }, now);
    case "uploading":
      return transitionScanQueueItem(item, { status: "uploading", progress: state.progress }, now);
    case "failed":
      return transitionScanQueueItem(item, { status: "failed", detail: state.error }, now);
    case "conflict":
      return transitionScanQueueItem(item, { status: "conflict", detail: state.reason }, now);
    case "succeeded":
      return transitionScanQueueItem(item, {
        status: "succeeded", progress: 100, fileAssetId: state.fileAssetId
      }, now);
  }
}
