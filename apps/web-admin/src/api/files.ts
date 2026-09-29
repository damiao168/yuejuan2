import { apiClient, ApiClientError, type ApiErrorPayload } from "./client";

export interface FileAsset {
  id: string;
  tenant_id: string;
  school_id?: string;
  exam_id?: string;
  submission_id?: string;
  owner_type: string;
  owner_id?: string;
  original_name: string;
  content_type: string;
  size_bytes: number;
  hash_sha256: string;
  visibility: string;
  uploaded_by: string;
  created_at: string;
}

export interface FileUploadMetadata {
  owner_type: string;
  owner_id?: string;
  school_id?: string;
  exam_id?: string;
  submission_id?: string;
}

export interface FileDownload {
  blob: Blob;
  contentType: string;
  filename?: string;
  watermark?: string;
}

function fileForm(file: File, metadata: FileUploadMetadata) {
  const body = new FormData();
  body.set("file", file);
  body.set("owner_type", metadata.owner_type);
  if (metadata.owner_id) {
    body.set("owner_id", metadata.owner_id);
  }
  if (metadata.school_id) {
    body.set("school_id", metadata.school_id);
  }
  if (metadata.exam_id) {
    body.set("exam_id", metadata.exam_id);
  }
  if (metadata.submission_id) {
    body.set("submission_id", metadata.submission_id);
  }
  return body;
}

export async function uploadFile(file: File, metadata: FileUploadMetadata) {
  return apiClient.request<{ file: FileAsset }>("/api/v1/files", {
    method: "POST",
    body: fileForm(file, metadata)
  });
}

export function uploadFileWithProgress(file: File, metadata: FileUploadMetadata, onProgress: (percent: number) => void) {
  // XHR 用于报告请求体上传进度；到达 100% 仍需等待服务端响应后才算上传成功。
  return new Promise<{ file: FileAsset }>((resolve, reject) => {
    const xhr = new XMLHttpRequest();
    xhr.open("POST", apiClient.url("/api/v1/files"));
    xhr.withCredentials = true;
    xhr.setRequestHeader("Accept", "application/json");
    xhr.setRequestHeader("X-EduGrade-CSRF", "1");
    xhr.setRequestHeader("Idempotency-Key", crypto.randomUUID());
    xhr.upload.onprogress = (event) => {
      if (event.lengthComputable) {
        onProgress(Math.round((event.loaded / event.total) * 100));
      }
    };
    xhr.onload = () => {
      if (xhr.status >= 200 && xhr.status < 300) {
        try {
          resolve(JSON.parse(xhr.responseText) as { file: FileAsset });
        } catch {
          reject(new ApiClientError(xhr.status, "invalid_response", "文件上传响应无法解析"));
        }
        return;
      }
      reject(errorFromXHR(xhr));
    };
    xhr.onerror = () => reject(new ApiClientError(0, "network_error", "文件上传网络错误"));
    xhr.send(fileForm(file, metadata));
  });
}

export async function downloadFileBlob(fileId: string): Promise<FileDownload> {
  return apiClient.requestBlob(`/api/v1/files/${encodeURIComponent(fileId)}/download`);
}

function errorFromXHR(xhr: XMLHttpRequest) {
  try {
    const payload = JSON.parse(xhr.responseText) as ApiErrorPayload;
    const code = payload.error?.code ?? payload.code ?? "request_failed";
    const message = payload.error?.message ?? payload.message ?? xhr.statusText;
    return new ApiClientError(xhr.status, code, message);
  } catch {
    return new ApiClientError(xhr.status, "request_failed", xhr.statusText);
  }
}
