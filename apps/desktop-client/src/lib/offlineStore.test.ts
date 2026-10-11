// @vitest-environment jsdom
import { webcrypto } from "node:crypto";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { OfflineDraftRecord } from "../types";
import { loadOfflineDraft, purgeExpiredOfflineDrafts, readOfflineDraftEnvelopes, saveOfflineDraft, updateOfflineDraftStatus } from "./offlineStore";

vi.mock("./durableStore", () => ({
  hasDurableDesktopStore: () => false,
  listDurableDraftEnvelopes: vi.fn(), loadDurableDraft: vi.fn(), purgeExpiredDurableDrafts: vi.fn(),
  saveDurableDraft: vi.fn(), updateDurableDraftStatus: vi.fn()
}));

const passphrase = "offline-test-only-passphrase";
const storeKey = "edugrade.desktop.offline_drafts";

function record(): OfflineDraftRecord {
  return {
    taskId: "task", anonymousCode: "anonymous", savedAt: "2026-10-03T00:00:00Z", expiresAt: "2026-10-04T00:00:00Z", syncStatus: "draft",
    packageSnapshot: {
      task: { id: "task", revision: 1, exam_id: "exam", question_id: "question", question_no: "1", answer_segment_id: "segment", submission_id: "submission", anonymous_code: "anonymous", source: "test", status: "pending", priority: 1, created_at: "" },
      ocrText: "中文内容与非 BMP 字符 \u{1F4DD}", aiGrades: [], warnings: [], downloadedAt: ""
    },
    draft: { taskId: "task", score: 0, rubricSelections: { point: 0 }, comments: "\u0000\u00FF中文\u{10437}", studentFeedback: "反馈", privateNote: "私有记录", reason: "复核" }
  };
}

describe("browser offline draft encoding compatibility", () => {
  beforeEach(() => {
    window.localStorage.clear();
    vi.stubGlobal("crypto", webcrypto);
  });
  afterEach(() => { vi.restoreAllMocks(); vi.unstubAllGlobals(); });

  it("round-trips Unicode contents through encrypted byte Base64 without plaintext storage", async () => {
    const value = record();
    await saveOfflineDraft(value, passphrase);
    expect(await loadOfflineDraft(value.taskId, passphrase)).toEqual(value);
    expect(window.localStorage.getItem(storeKey)).not.toContain(value.draft.privateNote);
    const payload = readOfflineDraftEnvelopes()[0].encrypted;
    expect(payload?.version).toBe(1);
    expect(window.atob(payload!.salt)).toHaveLength(16);
    expect(window.atob(payload!.iv)).toHaveLength(12);
  });

  it("decrypts a version-1 payload produced with the previous UTF-16 byte encoder", async () => {
    const value = record();
    const salt = Uint8Array.from({ length: 16 }, (_, index) => index + 128);
    const iv = Uint8Array.from({ length: 12 }, (_, index) => 255 - index);
    const encoder = new TextEncoder();
    const material = await webcrypto.subtle.importKey("raw", encoder.encode(passphrase), "PBKDF2", false, ["deriveKey"]);
    const key = await webcrypto.subtle.deriveKey({ name: "PBKDF2", salt, iterations: 120000, hash: "SHA-256" }, material, { name: "AES-GCM", length: 256 }, false, ["encrypt"]);
    const data = new Uint8Array(await webcrypto.subtle.encrypt({ name: "AES-GCM", iv }, key, encoder.encode(JSON.stringify(value))));
    // 旧实现只将字节映射为单字符；测试保留旧编码器，验证读取兼容而不是自测新编码器。
    const oldBase64 = (bytes: Uint8Array) => window.btoa(Array.from(bytes, (byte) => String.fromCharCode(byte)).join(""));
    window.localStorage.setItem(storeKey, JSON.stringify([{ taskId: value.taskId, encrypted: { version: 1, salt: oldBase64(salt), iv: oldBase64(iv), data: oldBase64(data) } }]));
    expect(await loadOfflineDraft(value.taskId, passphrase)).toEqual(value);
  });

  it("still rejects wrong credentials instead of returning corrupt draft text", async () => {
    await saveOfflineDraft(record(), passphrase);
    await expect(loadOfflineDraft("task", "wrong-test-passphrase")).rejects.toThrow();
  });

  it.each(["draft", "syncing", "failed", "conflict"] as const)("retains an expired %s command and its original payload", async (syncStatus) => {
    const value = record();
    value.syncStatus = syncStatus;
    value.submissionCommand = {
      id: "frozen-command", taskId: value.taskId, server: "https://grading.example.edu/", actorId: "teacher", tenantId: "tenant",
      payload: { expected_revision: 1, score: 0, rubric_selections: [], comments: "原评语", private_note: "原备注", student_feedback: "原反馈", reason: "原原因" }
    };
    await saveOfflineDraft(value, passphrase);

    expect(await purgeExpiredOfflineDrafts(new Date("2026-10-11T00:00:00Z"), undefined, passphrase)).toBe(0);
    expect(await loadOfflineDraft(value.taskId, passphrase)).toEqual(value);
  });

  it("cleans only confirmed expired drafts while retaining every unsynced scoring record", async () => {
    const legacy = record();
    await saveOfflineDraft(legacy, passphrase);
    const failed = { ...record(), taskId: "ordinary-failed", syncStatus: "failed" as const };
    await saveOfflineDraft(failed, passphrase);
    const confirmed = { ...record(), taskId: "confirmed", submissionCommand: {
      id: "confirmed-command", taskId: "confirmed", server: "https://grading.example.edu/", actorId: "teacher", tenantId: "tenant",
      payload: { expected_revision: 1, score: 0, rubric_selections: [], comments: "", private_note: "", student_feedback: "", reason: "" }
    } };
    await saveOfflineDraft(confirmed, passphrase);
    // 正文保留发送前的状态，外层元数据已经持久化服务端成功确认。
    await updateOfflineDraftStatus("confirmed", { syncStatus: "synced" });
    await saveOfflineDraft({ ...record(), taskId: "future", expiresAt: "2026-10-12T00:00:00Z" }, passphrase);

    expect(await purgeExpiredOfflineDrafts(new Date("2026-10-11T00:00:00Z"), undefined, passphrase)).toBe(1);
    expect(readOfflineDraftEnvelopes().map((item) => item.taskId)).toEqual(["future", "ordinary-failed", legacy.taskId]);
  });

  it("does not partially clean when an expired payload cannot be decrypted", async () => {
    await saveOfflineDraft(record(), passphrase);
    await saveOfflineDraft({ ...record(), taskId: "corrupt" }, passphrase);
    const entries = readOfflineDraftEnvelopes();
    entries.find((item) => item.taskId === "corrupt")!.encrypted!.data = "corrupt";
    window.localStorage.setItem(storeKey, JSON.stringify(entries.reverse()));
    const stored = window.localStorage.getItem(storeKey);

    await expect(purgeExpiredOfflineDrafts(new Date("2026-10-11T00:00:00Z"), undefined, passphrase)).rejects.toThrow();
    expect(window.localStorage.getItem(storeKey)).toBe(stored);
  });

  it("preserves expired records when the browser key is missing or incorrect", async () => {
    await saveOfflineDraft(record(), passphrase);
    const stored = window.localStorage.getItem(storeKey);

    await expect(purgeExpiredOfflineDrafts(new Date("2026-10-11T00:00:00Z"))).rejects.toThrow("至少 8 位");
    await expect(purgeExpiredOfflineDrafts(new Date("2026-10-11T00:00:00Z"), undefined, "wrong-test-passphrase")).rejects.toThrow();
    expect(window.localStorage.getItem(storeKey)).toBe(stored);
  });

  it("does not delete a record saved again while cleanup decrypts its old payload", async () => {
    await saveOfflineDraft(record(), passphrase);
    const originalDecrypt = webcrypto.subtle.decrypt.bind(webcrypto.subtle);
    vi.spyOn(webcrypto.subtle, "decrypt").mockImplementationOnce(async (...args) => {
      const decrypted = await originalDecrypt(...args);
      const entries = readOfflineDraftEnvelopes();
      entries[0].expiresAt = "2026-10-12T00:00:00Z";
      entries[0].savedAt = "2026-10-11T00:00:00Z";
      window.localStorage.setItem(storeKey, JSON.stringify(entries));
      return decrypted;
    });

    expect(await purgeExpiredOfflineDrafts(new Date("2026-10-11T00:00:00Z"), undefined, passphrase)).toBe(0);
    expect(readOfflineDraftEnvelopes()[0].expiresAt).toBe("2026-10-12T00:00:00Z");
  });

  it("does not silently evict the oldest unconfirmed command after 200 records", async () => {
    const pending = { ...record(), syncStatus: "failed" as const, submissionCommand: {
      id: "oldest-command", taskId: "task", server: "https://grading.example.edu/", actorId: "teacher", tenantId: "tenant",
      payload: { expected_revision: 1, score: 0, rubric_selections: [], comments: "原评语", private_note: "原备注", student_feedback: "原反馈", reason: "原原因" }
    } };
    await saveOfflineDraft(pending, passphrase);
    const oldest = readOfflineDraftEnvelopes()[0];
    // 只检查容量处理，其他封装不加载；保留真实加密的最旧命令，无需重复派生 200 次密钥。
    const fillers = Array.from({ length: 199 }, (_, index) => ({ ...oldest, taskId: `filler-${index}` }));
    window.localStorage.setItem(storeKey, JSON.stringify([...fillers, oldest]));
    await saveOfflineDraft({ ...record(), taskId: "newest" }, passphrase);

    expect(readOfflineDraftEnvelopes()).toHaveLength(201);
    expect(await loadOfflineDraft("task", passphrase)).toEqual(pending);
    const stored = window.localStorage.getItem(storeKey);
    vi.spyOn(Storage.prototype, "setItem").mockImplementationOnce(() => { throw new DOMException("quota exceeded", "QuotaExceededError"); });
    await expect(saveOfflineDraft({ ...record(), taskId: "quota-rejected" }, passphrase)).rejects.toThrow("quota exceeded");
    expect(window.localStorage.getItem(storeKey)).toBe(stored);
  });
});
