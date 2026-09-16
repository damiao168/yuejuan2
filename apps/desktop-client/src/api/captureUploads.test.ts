import { createHash } from "node:crypto";
import { describe, expect, it, vi } from "vitest";
import { DesktopApiClient } from "./client";
import { resumeCaptureUpload, sha256ForFile } from "./captureUploads";

function response(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" }
  });
}

function scanFile(bytes: number[]) {
  return Object.assign(new Blob([new Uint8Array(bytes)], { type: "application/pdf" }), {
    name: "answer-sheet.pdf"
  }) as File;
}

describe("resumable capture upload", () => {
  it("retries completion of a finalizing checkpoint without resending chunks", async () => {
    const file = scanFile([1, 2, 3]);
    const sha256 = await sha256ForFile(file);
    const upload = {remote_upload_id:"saved-upload", exam:"exam", batch:"batch", sha256,
      size:file.size, mime:file.type, chunk_size:4, confirmed_offset:file.size, status:"finalizing"};
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(response({command_id:"stable-command", status:"processing", upload}))
      .mockResolvedValueOnce(response({...upload, status:"completed", capture_file_id:"capture"}));
    vi.stubGlobal("fetch", fetchMock);
    const completed = await resumeCaptureUpload(new DesktopApiClient({baseUrl:"https://grading.example.edu"}), {
      file, exam:"exam", batch:"batch", idempotency_key:"stable-command", remoteUploadId:"saved-upload"
    }, () => {});
    expect(completed.status).toBe("completed");
    expect(fetchMock).toHaveBeenCalledTimes(2);
    expect(fetchMock.mock.calls[1][0]).toContain("/saved-upload/complete");
  });
  it("recovers a completed durable upload without init or byte transfer", async () => {
    const file = scanFile([1,2,3]);
    const sha256 = await sha256ForFile(file);
    const fetchMock = vi.fn().mockResolvedValue(response({ command_id: "stable-command", status: "succeeded", upload: {
      remote_upload_id: "saved-upload", exam: "exam", batch: "batch", sha256, size: file.size,
      mime: file.type, chunk_size: 4, confirmed_offset: file.size, status: "completed",
      file_asset_id: "asset", capture_file_id: "capture", created_at: "2026-09-08T00:00:00Z"
    }}));
    vi.stubGlobal("fetch",fetchMock);
    const completed = await resumeCaptureUpload(new DesktopApiClient({baseUrl:"https://grading.example.edu"}), {
      file, exam:"exam", batch:"batch", idempotency_key:"stable-command", remoteUploadId:"saved-upload"
    },()=>{});
    expect(completed.capture_file_id).toBe("capture");
    expect(fetchMock).toHaveBeenCalledTimes(1);
    expect(fetchMock.mock.calls[0][0]).toBe("https://grading.example.edu/api/v1/capture/uploads/saved-upload");
  });

  it("rejects a recovered checkpoint belonging to another immutable input", async () => {
    vi.stubGlobal("fetch",vi.fn().mockResolvedValue(response({command_id:"other-command",status:"processing",upload:{}})));
    await expect(resumeCaptureUpload(new DesktopApiClient({baseUrl:"https://grading.example.edu"}), {
      file:scanFile([1]),exam:"exam",batch:"batch",idempotency_key:"stable-command",remoteUploadId:"saved-upload"
    },()=>{})).rejects.toMatchObject({code:"capture_upload_conflict"});
  });
  it("hashes large files incrementally without reading the whole Blob", async () => {
    const bytes = new Uint8Array(9 * 1024 * 1024 + 17);
    for (let index = 0; index < bytes.length; index += 1) bytes[index] = (index * 31 + 7) & 0xff;
    const source = new Blob([bytes]);
    let largestSlice = 0;
    let sliceCount = 0;
    const tracked = {
      size: source.size,
      type: source.type,
      slice(start = 0, end = source.size) {
        largestSlice = Math.max(largestSlice, end - start);
        sliceCount += 1;
        return source.slice(start, end);
      },
      arrayBuffer() {
        throw new Error("whole-file arrayBuffer must not be used");
      }
    } as unknown as Blob;

    const digest = await sha256ForFile(tracked, 1024 * 1024);
    expect(digest).toBe(createHash("sha256").update(bytes).digest("hex"));
    expect(largestSlice).toBeLessThanOrEqual(1024 * 1024);
    expect(sliceCount).toBe(10);
  });

  it("starts from the server-confirmed offset after an interrupted upload", async () => {
    const fetchMock = vi.fn<typeof fetch>()
      .mockResolvedValueOnce(response({
        remote_upload_id: "upload-1",
        chunk_size: 3,
        confirmed_offset: 3,
        already_exists: false,
        status: "uploading"
      }))
      .mockResolvedValueOnce(response({ remote_upload_id: "upload-1", confirmed_offset: 6, status: "uploading" }))
      .mockResolvedValueOnce(response({ remote_upload_id: "upload-1", confirmed_offset: 8, status: "uploading" }))
      .mockResolvedValueOnce(response({
        remote_upload_id: "upload-1",
        confirmed_offset: 8,
        status: "completed",
        file_asset_id: "asset-1",
        capture_file_id: "capture-file-1"
      }));
    vi.stubGlobal("fetch", fetchMock);

    const progress: number[] = [];
    const completed = await resumeCaptureUpload(
      new DesktopApiClient({ baseUrl: "https://grading.example.edu", getToken: () => "test-token" }),
      {
        file: scanFile([0, 1, 2, 3, 4, 5, 6, 7]),
        exam: "exam-1",
        batch: "batch-1",
        idempotency_key: "stable-spool-key"
      },
      (update) => {
        progress.push(update.confirmedOffset);
      }
    );

    expect(completed.status).toBe("completed");
    expect(progress).toEqual([3, 6, 8, 8]);
    expect(fetchMock).toHaveBeenCalledTimes(4);
    expect(fetchMock.mock.calls.slice(1, 3).map(([url, init]) => ({
      url: String(url),
      offset: new Headers(init?.headers).get("Upload-Offset")
    }))).toEqual([
      { url: "https://grading.example.edu/api/v1/capture/uploads/upload-1/chunks", offset: "3" },
      { url: "https://grading.example.edu/api/v1/capture/uploads/upload-1/chunks", offset: "6" }
    ]);
  });

  it("does not send chunks or finalize again when the same idempotency key is already confirmed", async () => {
    const fetchMock = vi.fn<typeof fetch>().mockResolvedValue(response({
      remote_upload_id: "upload-1",
      chunk_size: 3,
      confirmed_offset: 8,
      already_exists: true,
      status: "completed",
      file_asset_id: "asset-1",
      capture_file_id: "capture-file-1"
    }));
    vi.stubGlobal("fetch", fetchMock);

    const progress: Array<{ offset: number; status: string }> = [];
    const completed = await resumeCaptureUpload(
      new DesktopApiClient({ baseUrl: "https://grading.example.edu" }),
      {
        file: scanFile([0, 1, 2, 3, 4, 5, 6, 7]),
        exam: "exam-1",
        batch: "batch-1",
        idempotency_key: "stable-spool-key"
      },
      (update) => {
        progress.push({ offset: update.confirmedOffset, status: update.status });
      }
    );

    expect(completed.capture_file_id).toBe("capture-file-1");
    expect(fetchMock).toHaveBeenCalledTimes(1);
    expect(progress).toEqual([{ offset: 8, status: "completed" }]);
  });

  it("recovers page 173 of a 500-page durable fixture without replaying confirmed chunks or registering twice", async () => {
    // The native store owns the actual 500-item recovery state (covered by
    // durable_store.rs).  This fixture drives the web/API side of the same
    // recovery point: page 173 was killed after offset 3 was confirmed, then
    // a 5xx is returned after finalization.  A later init is authoritative.
    const pages = Array.from({ length: 500 }, (_, index) => ({
      pageNo: index + 1,
      status: index < 172 ? "succeeded" : "pending"
    }));
    const interruptedPage = pages[172];
    if (!interruptedPage) throw new Error("fixture must include page 173");
    expect(interruptedPage).toEqual({ pageNo: 173, status: "pending" });

    const fetchMock = vi.fn<typeof fetch>()
      // First runtime: init, confirm bytes [0, 3), then the connection dies
      // before bytes [3, 6) can be accepted.
      .mockResolvedValueOnce(response({
        remote_upload_id: "upload-page-173",
        chunk_size: 3,
        confirmed_offset: 0,
        already_exists: false,
        status: "uploading"
      }))
      .mockResolvedValueOnce(response({ remote_upload_id: "upload-page-173", confirmed_offset: 3, status: "uploading" }))
      .mockRejectedValueOnce(new TypeError("network disconnected"))
      // After process restart, server-confirmed offset 3 skips the first
      // chunk.  A 5xx after complete is treated as an unknown result; the
      // subsequent init reports the server's already-created capture file.
      .mockResolvedValueOnce(response({
        remote_upload_id: "upload-page-173",
        chunk_size: 3,
        confirmed_offset: 3,
        already_exists: true,
        status: "uploading"
      }))
      .mockResolvedValueOnce(response({ remote_upload_id: "upload-page-173", confirmed_offset: 6, status: "uploading" }))
      .mockResolvedValueOnce(response({ remote_upload_id: "upload-page-173", confirmed_offset: 9, status: "uploading" }))
      .mockResolvedValueOnce(response({ remote_upload_id: "upload-page-173", confirmed_offset: 12, status: "uploading" }))
      .mockResolvedValueOnce(response({ error: { code: "upstream_unavailable", message: "temporary 5xx" } }, 503))
      .mockResolvedValueOnce(response({
        remote_upload_id: "upload-page-173",
        chunk_size: 3,
        confirmed_offset: 12,
        already_exists: true,
        status: "completed",
        file_asset_id: "asset-page-173",
        capture_file_id: "capture-page-173"
      }));
    vi.stubGlobal("fetch", fetchMock);

    const client = new DesktopApiClient({ baseUrl: "https://grading.example.edu" });
    const input = {
      file: scanFile(Array.from({ length: 12 }, (_, index) => index)),
      exam: "exam-500-pages",
      batch: "batch-500-pages",
      idempotency_key: "stable-page-173"
    };
    const firstRuntimeProgress: number[] = [];
    await expect(resumeCaptureUpload(client, input, (progress) => {
      firstRuntimeProgress.push(progress.confirmedOffset);
    })).rejects.toThrow("network disconnected");
    expect(firstRuntimeProgress).toEqual([0, 3]);

    const restartedProgress: number[] = [];
    await expect(resumeCaptureUpload(client, input, (progress) => {
      restartedProgress.push(progress.confirmedOffset);
    })).rejects.toMatchObject({ status: 503, code: "upstream_unavailable" });
    expect(restartedProgress).toEqual([3, 6, 9, 12]);

    const recovered = await resumeCaptureUpload(client, input, () => undefined);
    expect(recovered).toMatchObject({
      status: "completed",
      file_asset_id: "asset-page-173",
      capture_file_id: "capture-page-173"
    });

    const chunkOffsets = fetchMock.mock.calls
      .filter(([url]) => String(url).endsWith("/chunks"))
      .map(([, init]) => new Headers(init?.headers).get("Upload-Offset"));
    // Offset 0 is sent once only.  Offset 3 is the failed, unconfirmed
    // request and is correctly retried after recovery.
    expect(chunkOffsets).toEqual(["0", "3", "3", "6", "9"]);
    expect(fetchMock.mock.calls.filter(([url]) => String(url).endsWith("/complete"))).toHaveLength(1);
    expect(fetchMock.mock.calls).toHaveLength(9);
  });
});
