import { ApiClientError, apiClientErrorFromResponse, type DesktopApiClient } from "./client";
import { IncrementalSha256 } from "../lib/incrementalSha256";
import type { CaptureUploadInitRequest, CaptureUploadInitResponse as GeneratedCaptureUploadInitResponse, CaptureUploadSession, CaptureUploadRecoveryResponse } from "@edugrade/sdk";

export type CaptureUploadInitInput = CaptureUploadInitRequest;

export type CaptureUploadInitResponse = GeneratedCaptureUploadInitResponse;

export type CaptureUploadCompleteResponse = Pick<CaptureUploadSession,
  "remote_upload_id" | "confirmed_offset" | "status" | "file_asset_id" | "capture_file_id" | "error_code">;

export async function sha256ForFile(file: Blob, chunkSize = 4 * 1024 * 1024) {
  if (!Number.isSafeInteger(chunkSize) || chunkSize <= 0) throw new Error("SHA-256 chunk size must be a positive safe integer");
  const digest = new IncrementalSha256();
  for (let offset = 0; offset < file.size; offset += chunkSize) {
    const chunk = file.slice(offset, Math.min(offset + chunkSize, file.size));
    digest.update(new Uint8Array(await chunk.arrayBuffer()));
  }
  return digest.hex();
}

export function initCaptureUpload(client: DesktopApiClient, input: CaptureUploadInitInput, signal?: AbortSignal) {
  return client.request<CaptureUploadInitResponse>("/api/v1/capture/uploads:init", {
    method: "POST",
    signal,
    body: JSON.stringify(input)
  });
}

// The server advances confirmed_offset atomically.  Retrying the same chunk
// is safe; callers must persist the returned offset before scheduling another
// chunk so a process crash cannot produce duplicate answer-sheet pages.
export async function putCaptureUploadChunk(client: DesktopApiClient, uploadId: string, offset: number, chunk: Blob, signal?: AbortSignal) {
  const bytes = new Uint8Array(await chunk.arrayBuffer());
  const hash = await sha256ForBytes(bytes);
  const headers = new Headers({
    Accept: "application/json",
    "Content-Type": "application/octet-stream",
    "Upload-Offset": String(offset),
    "X-Chunk-SHA256": hash
  });
  const authorization = client.authorizationHeader();
  if (authorization) {
    headers.set("Authorization", authorization);
  }
  const response = await fetch(client.url(`/api/v1/capture/uploads/${encodeURIComponent(uploadId)}/chunks`), {
    method: "PUT",
    signal,
    headers,
    body: bytes
  });
  if (!response.ok) {
    throw await apiClientErrorFromResponse(response);
  }
  return (await response.json()) as Pick<CaptureUploadInitResponse, "remote_upload_id" | "confirmed_offset" | "status">;
}

export function completeCaptureUpload(client: DesktopApiClient, uploadId: string, sha256: string, signal?: AbortSignal) {
  return client.request<CaptureUploadCompleteResponse>(`/api/v1/capture/uploads/${encodeURIComponent(uploadId)}/complete`, {
    method: "POST",
    signal,
    body: JSON.stringify({ sha256 })
  });
}

export interface ResumeCaptureUploadInput extends Omit<CaptureUploadInitInput, "sha256" | "size" | "mime" | "filename"> {
  file: File | CaptureUploadSource;
  remoteUploadId?: string;
}

export function recoverCaptureUpload(client: DesktopApiClient, uploadId: string, signal?: AbortSignal) {
  return client.request<CaptureUploadRecoveryResponse>(`/api/v1/capture/uploads/${encodeURIComponent(uploadId)}`, { signal });
}

export interface CaptureUploadProgress {
  remoteUploadId: string;
  confirmedOffset: number;
  totalBytes: number;
  status: CaptureUploadInitResponse["status"];
  fileAssetId?: string;
  captureFileId?: string;
}

export interface CaptureUploadSource {
  name: string;
  type: string;
  size: number;
  sha256: string;
  slice(start: number, end: number): Blob | Promise<Blob>;
}

export async function resumeCaptureUpload(
  client: DesktopApiClient,
  input: ResumeCaptureUploadInput,
  onProgress: (progress: CaptureUploadProgress) => Promise<void> | void,
  signal?: AbortSignal
) {
  signal?.throwIfAborted();
  const sha256 = "sha256" in input.file ? input.file.sha256 : await sha256ForFile(input.file);
  let recovered: CaptureUploadInitResponse | undefined;
  if (input.remoteUploadId) {
    const result = await recoverCaptureUpload(client, input.remoteUploadId, signal);
    const upload = result.upload;
    if (result.command_id !== input.idempotency_key || upload.exam !== input.exam || upload.batch !== input.batch || upload.sha256 !== sha256 || upload.size !== input.file.size) {
      throw new ApiClientError(409, "capture_upload_conflict", "saved upload identity does not match the local original");
    }
    recovered = { ...upload, already_exists: true };
  }
  const initialized = recovered ?? await initCaptureUpload(client, {
    sha256,
    size: input.file.size,
    mime: input.file.type || inferCaptureMime(input.file.name),
    exam: input.exam,
    batch: input.batch,
    idempotency_key: input.idempotency_key,
    filename: input.file.name
  }, signal);
  let confirmedOffset = initialized.confirmed_offset;
  await onProgress({
    remoteUploadId: initialized.remote_upload_id,
    confirmedOffset,
    totalBytes: input.file.size,
    status: initialized.status,
    fileAssetId: initialized.file_asset_id,
    captureFileId: initialized.capture_file_id
  });
  if (initialized.status === "completed") {
    return initialized;
  }
  if (initialized.status !== "uploading" && initialized.status !== "finalizing") {
    throw new ApiClientError(409, "capture_upload_not_resumable", `capture upload is ${initialized.status}`);
  }
  while (confirmedOffset < input.file.size) {
    signal?.throwIfAborted();
    const end = Math.min(confirmedOffset + initialized.chunk_size, input.file.size);
    const chunk = await input.file.slice(confirmedOffset, end);
    const chunkResult = await putCaptureUploadChunk(client, initialized.remote_upload_id, confirmedOffset, chunk, signal);
    if (chunkResult.confirmed_offset <= confirmedOffset || chunkResult.confirmed_offset > input.file.size) {
      throw new ApiClientError(409, "capture_upload_invalid_offset", "server returned an invalid confirmed offset");
    }
    confirmedOffset = chunkResult.confirmed_offset;
    await onProgress({ remoteUploadId: initialized.remote_upload_id, confirmedOffset, totalBytes: input.file.size, status: chunkResult.status });
  }
  signal?.throwIfAborted();
  const completed = await completeCaptureUpload(client, initialized.remote_upload_id, sha256, signal);
  await onProgress({
    remoteUploadId: completed.remote_upload_id,
    confirmedOffset: completed.confirmed_offset,
    totalBytes: input.file.size,
    status: completed.status,
    fileAssetId: completed.file_asset_id,
    captureFileId: completed.capture_file_id
  });
  return completed;
}

async function sha256ForBytes(bytes: Uint8Array) {
  return new IncrementalSha256().update(bytes).hex();
}

function inferCaptureMime(name: string) {
  const extension = name.slice(name.lastIndexOf(".")).toLowerCase();
  if (extension === ".pdf") return "application/pdf";
  if (extension === ".png") return "image/png";
  if (extension === ".jpg" || extension === ".jpeg") return "image/jpeg";
  if (extension === ".tif" || extension === ".tiff") return "image/tiff";
  return "application/octet-stream";
}
