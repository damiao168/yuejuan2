// @vitest-environment jsdom
import { webcrypto } from "node:crypto";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { OfflineDraftRecord, OfflineTaskPackage } from "../types";
import { MAX_OFFLINE_ANSWER_BYTES, prepareOfflineAnswerMaterial, restoreOfflineAnswerMaterial } from "./offlineAnswerMaterial";
import { loadOfflineDraft, saveOfflineDraft } from "./offlineStore";

vi.mock("./durableStore", () => ({ hasDurableDesktopStore: () => false }));
const pkg = (): OfflineTaskPackage => ({ task: { id: "task", revision: 2, exam_id: "exam", question_id: "question", question_no: "1", answer_segment_id: "segment", submission_id: "submission", anonymous_code: "anonymous", source: "test", status: "pending", priority: 1, created_at: "" }, ocrText: "", aiGrades: [], warnings: [], downloadedAt: "" });
const bytes = new Uint8Array([137, 80, 78, 71, 13, 10, 26, 10, 55, 66, 99]);
describe("encrypted offline answer originals", () => {
  beforeEach(() => { vi.stubGlobal("crypto", webcrypto); localStorage.clear(); });
  afterEach(() => vi.unstubAllGlobals());
  it("restores exact original bytes after an encrypted draft reload without storing image plaintext", async () => {
    const value = pkg(); value.answerMaterial = await prepareOfflineAnswerMaterial(value, new Blob([bytes]), "image/png", "匿名答卷.png");
    const record: OfflineDraftRecord = { taskId: "task", anonymousCode: "anonymous", savedAt: "2026-10-28", expiresAt: "2026-11-01", syncStatus: "draft", packageSnapshot: value,
      draft: { taskId: "task", score: 7, rubricSelections: {}, comments: "人工评语", studentFeedback: "", privateNote: "", reason: "" } };
    await saveOfflineDraft(record, "secret-key");
    const persisted = localStorage.getItem("edugrade.desktop.offline_drafts")!;
    expect(persisted).not.toContain(value.answerMaterial.base64); expect(persisted).not.toContain("匿名答卷.png");
    const restored = await loadOfflineDraft("task", "secret-key");
    const material = await restoreOfflineAnswerMaterial(restored!.packageSnapshot);
    expect(material?.blob.size).toBe(bytes.length); expect(material?.filename).toBe("匿名答卷.png");
    expect(await prepareOfflineAnswerMaterial(restored!.packageSnapshot, material!.blob, material!.contentType)).toMatchObject({ sha256: value.answerMaterial.sha256, base64: value.answerMaterial.base64 });
    expect(restored?.draft.score).toBe(7);
  });
  it.each([{ id: "other" }, { revision: 3 }, { question_id: "other" }, { answer_segment_id: "other" }])("rejects mismatched task material %j", async (change) => {
    const value = pkg(); value.answerMaterial = await prepareOfflineAnswerMaterial(value, new Blob([bytes]), "image/png");
    value.task = { ...value.task, ...change };
    await expect(restoreOfflineAnswerMaterial(value)).rejects.toThrow("身份、版本");
  });
  it("rejects corrupt originals and keeps old text-only packages compatible", async () => {
    const value = pkg(); expect(await restoreOfflineAnswerMaterial(value)).toBeNull();
    value.answerMaterial = await prepareOfflineAnswerMaterial(value, new Blob([bytes]), "image/png");
    value.answerMaterial.base64 = btoa("X".repeat(bytes.length));
    await expect(restoreOfflineAnswerMaterial(value)).rejects.toThrow("校验失败");
  });
  it("refuses oversized or unsupported originals without truncation", async () => {
    await expect(prepareOfflineAnswerMaterial(pkg(), new Blob([new Uint8Array(MAX_OFFLINE_ANSWER_BYTES + 1)]), "image/png")).rejects.toThrow("不能离线准备");
    await expect(prepareOfflineAnswerMaterial(pkg(), new Blob([bytes]), "image/svg+xml")).rejects.toThrow("格式暂不能");
    const value = pkg(); value.ocrText = "中".repeat(1_000_000);
    await expect(prepareOfflineAnswerMaterial(value, new Blob([new Uint8Array(1_000_000)]), "image/png")).rejects.toThrow("超过离线容量");
  });
});
