import type { FileAsset } from "../types";
import { ApiClientError, type ApiErrorPayload, type DesktopApiClient } from "./client";

export interface UploadMetadata {
  owner_type: "answer_page" | "submission" | "generic";
  owner_id?: string;
  exam_id?: string;
  submission_id?: string;
}

export function uploadFileWithProgress(
  client: DesktopApiClient,
  file: File,
  metadata: UploadMetadata,
  onProgress: (percent: number) => void
) {
  return new Promise<{ file: FileAsset }>((resolve, reject) => {
    const body = new FormData();
    body.set("file", file);
    body.set("owner_type", metadata.owner_type);
    if (metadata.owner_id) {
      body.set("owner_id", metadata.owner_id);
    }
    if (metadata.exam_id) {
      body.set("exam_id", metadata.exam_id);
    }
    if (metadata.submission_id) {
      body.set("submission_id", metadata.submission_id);
    }

    const xhr = new XMLHttpRequest();
    // 此接口需要浏览器上传进度事件，因此使用 XHR；100% 仅表示发送完成，成功仍以响应为准。
    xhr.open("POST", client.url("/api/v1/files"));
    xhr.setRequestHeader("Accept", "application/json");
    const authorization = client.authorizationHeader();
    if (authorization) {
      xhr.setRequestHeader("Authorization", authorization);
    }
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
    xhr.send(body);
  });
}

export async function downloadFileBlob(client: DesktopApiClient, fileId: string) {
  return client.requestBlob(`/api/v1/files/${encodeURIComponent(fileId)}/download`);
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
