import { ApiClientError } from "../../../api/client";
import type { ExamSession, ExamSessionPayload } from "../../../api/exams";
import type { ExamCreationMode } from "./types";

export type ExamCreateCommandState = "submitting" | "unknown" | "conflict" | "succeeded";

export interface ExamCreateCommandSnapshot {
  version: 1;
  commandId: string;
  operation: "create_exam_session";
  target: { schoolId: string; gradeId: string };
  payload: ExamSessionPayload;
  creationMode?: ExamCreationMode;
  state: ExamCreateCommandState;
  createdAt: string;
  recoveryAttempt?: number;
  retryNotBefore?: string;
  result?: { examSessionId: string; examIds: string[] };
}

export function commandSnapshotKey(tenantId: string, userId: string) {
  return `exam-create-command:v1:${tenantId}:${userId}`;
}

export function loadExamCreateCommand(storage: Pick<Storage, "getItem">, key: string): ExamCreateCommandSnapshot | null {
  try {
    const value = JSON.parse(storage.getItem(key) ?? "null") as Partial<ExamCreateCommandSnapshot> | null;
    if (!value || value.version !== 1 || value.operation !== "create_exam_session" || !value.commandId || !value.payload || !value.target) return null;
    return value as ExamCreateCommandSnapshot;
  } catch {
    return null;
  }
}

export function beginExamCreateCommand(payload: ExamSessionPayload, commandId: string = crypto.randomUUID(), creationMode?: ExamCreationMode): ExamCreateCommandSnapshot {
  // 提交快照与编辑草稿分离；结果未知时必须重用原输入，不能夹带后续表单修改。
  return {
    version: 1,
    commandId,
    operation: "create_exam_session",
    target: { schoolId: payload.school_id, gradeId: payload.grade_id },
    payload: structuredClone(payload),
    creationMode,
    state: "submitting",
    createdAt: new Date().toISOString()
  };
}

export function persistExamCreateCommand(storage: Pick<Storage, "setItem">, key: string, command: ExamCreateCommandSnapshot) {
  // 保留每次操作的独立回执，再更新最近操作入口，避免新建草稿覆盖旧请求的恢复依据。
  storage.setItem(`${key}:operation:${command.commandId}`, JSON.stringify(command));
  storage.setItem(key, JSON.stringify(command));
}

export function commandAfterFailure(command: ExamCreateCommandSnapshot, error: unknown): ExamCreateCommandSnapshot | null {
  if (!(error instanceof ApiClientError)) return { ...command, state: "unknown" };
  if (error.code === "idempotency_key_reused_with_different_request") return { ...command, state: "conflict" };
  if (
    error.status >= 500 || error.status === 408 || error.status === 425 || error.status === 429 ||
    ["operation_in_progress", "idempotency_persist_failed", "idempotency_unavailable"].includes(error.code)
  ) return {
    ...command,
    state: "unknown",
    recoveryAttempt: 0,
    ...(error.retryAfterSeconds ? { retryNotBefore: new Date(Date.now() + error.retryAfterSeconds * 1000).toISOString() } : {})
  };
  // An expired login or changed scope rejects this HTTP attempt, but says
  // nothing about a previous attempt whose response was lost.
  if (["invalid_exam_session", "invalid_request", "invalid_exam"].includes(error.code)) return null;
  return { ...command, state: "unknown" };
}

export function nextCommandRecovery(command: ExamCreateCommandSnapshot, maxAttempts = 8): ExamCreateCommandSnapshot | null {
  const attempt = (command.recoveryAttempt ?? 0) + 1;
  return attempt > maxAttempts ? null : { ...command, recoveryAttempt: attempt };
}

export function commandRecoveryDelayMS(command: ExamCreateCommandSnapshot) {
  const backoff = Math.min(30_000, 1_000 * 2 ** Math.min(command.recoveryAttempt ?? 0, 5));
  const retryDelay = command.retryNotBefore ? Math.max(0, Date.parse(command.retryNotBefore) - Date.now()) : 0;
  return Math.max(backoff, retryDelay);
}

export interface ExamCreateSubmissionGate {
  enter(): boolean;
  leave(): void;
}

export function createExamCreateSubmissionGate(): ExamCreateSubmissionGate {
  let active = false;
  return {
    enter() {
      if (active) return false;
      active = true;
      return true;
    },
    leave() { active = false; }
  };
}

export function commandAfterSuccess(command: ExamCreateCommandSnapshot, session: ExamSession): ExamCreateCommandSnapshot {
  return {
    ...command,
    state: "succeeded",
    result: { examSessionId: session.id, examIds: session.exams.map((exam) => exam.id) }
  };
}

export function recordExamCreateSuccess(
  storage: Pick<Storage, "setItem" | "removeItem">,
  key: string,
  draftKey: string,
  command: ExamCreateCommandSnapshot,
  session: ExamSession,
): ExamCreateCommandSnapshot {
  const completed = commandAfterSuccess(command, session);
  // Transport success is definitive. Local cleanup must not turn it into a
  // retryable creation; the previous durable snapshot can still recover it.
  try { persistExamCreateCommand(storage, key, completed); } catch { /* recover original command on reload */ }
  try { storage.removeItem(draftKey); } catch { /* stale draft cannot change the receipt */ }
  return completed;
}
