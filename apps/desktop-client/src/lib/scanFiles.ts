import type { ScanQualityCheck, SyncQueueItem } from "../types";

const scanQueueStorageKey = "edugrade.desktop.scan_queue";
const maxUploadBytes = 104857600;
const allowedUploadExtensions = [".pdf", ".png", ".jpg", ".jpeg", ".tif", ".tiff"];

export function formatDate(value?: string) {
  if (!value) return "未记录";
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? value : date.toLocaleString("zh-CN", { hour12: false });
}

export function dependencyTone(status: "ok" | "error" | "not_configured") {
  if (status === "ok") return "success";
  if (status === "not_configured") return "warning";
  return "error";
}

export function dependencyLabel(status: "ok" | "error" | "not_configured") {
  if (status === "ok") return "正常";
  if (status === "not_configured") return "未配置/待接入";
  return "异常";
}

export function estimateLocalCacheBytes() {
  // 这里只统计浏览器键和值的字符串长度，用于缓存提示，不代表原生扫描文件的实际磁盘占用。
  let total = 0;
  for (let index = 0; index < window.localStorage.length; index += 1) {
    const key = window.localStorage.key(index);
    if (!key || !key.startsWith("edugrade.desktop.")) continue;
    total += key.length;
    total += window.localStorage.getItem(key)?.length ?? 0;
  }
  return total;
}

export async function inspectScanFile(file: File): Promise<ScanQualityCheck[]> {
  const extension = fileExtension(file.name);
  const allowedType = allowedUploadExtensions.includes(extension);
  const checks: ScanQualityCheck[] = [
    {
      key: "file_type",
      label: "文件类型",
      status: allowedType ? "passed" : "failed",
      detail: allowedType ? `${extension} 可上传` : `${extension || "无扩展名"} 不在允许范围`
    },
    {
      key: "file_size",
      label: "文件大小",
      status: file.size <= maxUploadBytes ? "passed" : "failed",
      detail: `${formatBytes(file.size)} / 上限 ${formatBytes(maxUploadBytes)}`
    },
    {
      key: "page_count",
      label: "页数",
      status: "not_configured",
      detail: "PDF 页数解析未配置/待接入；当前以后端 submission 质量门禁为准"
    }
  ];
  if (file.type.startsWith("image/") && extension !== ".tif" && extension !== ".tiff") {
    const resolution = await readImageResolution(file);
    checks.push({
      key: "resolution",
      label: "分辨率",
      status: resolution ? "passed" : "not_configured",
      detail: resolution ?? "图片分辨率无法读取，预留人工复核"
    });
  } else {
    checks.push({
      key: "resolution",
      label: "分辨率",
      status: "not_configured",
      detail: extension === ".tif" || extension === ".tiff"
        ? "TIFF 本机分辨率解码未配置；已交由扫描样张预检和服务端质量门禁处理"
        : "PDF/非图片分辨率检测未配置/待接入"
    });
  }
  return checks;
}

function readImageResolution(file: File) {
  return new Promise<string | null>((resolve) => {
    const url = URL.createObjectURL(file);
    const image = new Image();
    image.onload = () => {
      const value = `${image.naturalWidth} x ${image.naturalHeight}`;
      URL.revokeObjectURL(url);
      resolve(value);
    };
    image.onerror = () => {
      URL.revokeObjectURL(url);
      resolve(null);
    };
    image.src = url;
  });
}

export function previewUrlForFile(file: File) {
  // 返回的 Blob 地址由使用方在替换文件或卸载时释放，不能持久化到恢复队列。
  return file.type.startsWith("image/") ? URL.createObjectURL(file) : undefined;
}

function fileExtension(filename: string) {
  const index = filename.lastIndexOf(".");
  return index >= 0 ? filename.slice(index).toLowerCase() : "";
}

export function inferContentType(filename: string) {
  const extension = fileExtension(filename);
  if (extension === ".pdf") return "application/pdf";
  if (extension === ".png") return "image/png";
  if (extension === ".jpg" || extension === ".jpeg") return "image/jpeg";
  if (extension === ".tif" || extension === ".tiff") return "image/tiff";
  return "application/octet-stream";
}

export function formatBytes(value?: number) {
  if (typeof value !== "number") return "未记录";
  if (value < 1024) return `${value} B`;
  if (value < 1024 * 1024) return `${(value / 1024).toFixed(1)} KB`;
  return `${(value / 1024 / 1024).toFixed(1)} MB`;
}

export function readPersistedScanQueue(): SyncQueueItem[] {
  try {
    const raw = window.localStorage.getItem(scanQueueStorageKey);
    if (!raw) return [];
    const parsed = JSON.parse(raw) as SyncQueueItem[];
    if (!Array.isArray(parsed)) return [];
    return parsed.map((item) => {
      // 浏览器重启后没有原文件句柄；中断上传改为失败，尚无服务端文件时要求用户重选。
      if (item.kind !== "scan_upload" || item.status === "succeeded") return item;
      return {
        ...item,
        status: item.status === "uploading" ? "failed" : item.status,
        progress: item.status === "uploading" ? 0 : item.progress,
        requiresReselect: !item.fileAssetId,
        detail: item.fileAssetId ? item.detail : "本地队列已恢复；文件句柄不可恢复，请重新选择同名文件后继续",
        previewUrl: undefined
      };
    });
  } catch {
    return [];
  }
}

export function persistScanQueue(items: SyncQueueItem[]) {
  const serializable = items.map((item) => {
    const { previewUrl: _previewUrl, ...rest } = item;
    return rest;
  });
  try {
    window.localStorage.setItem(scanQueueStorageKey, JSON.stringify(serializable.slice(0, 300)));
  } catch (error) {
    console.warn("scan queue persistence failed", error);
  }
}
