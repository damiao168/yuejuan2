import type { CaptureUploadSource } from "../../api/captureUploads";
import type { SyncQueueItem } from "../../types";

// 预览 URL 与内存句柄随队列显示释放；本机加密原件是否归档仍由调用方先执行耐久存储命令。
export function releaseQueueFiles(items: SyncQueueItem[], fileBuffer: Map<string, File | CaptureUploadSource>) {
  for (const item of items) {
    if (item.previewUrl) URL.revokeObjectURL(item.previewUrl);
    fileBuffer.delete(item.id);
  }
}
