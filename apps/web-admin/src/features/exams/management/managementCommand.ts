import { ApiClientError } from "../../../api/client";
import type { ManagementExamKind, ManagementExamVersion } from "../../../api/exams";

export interface ManagementArchiveCommand {
  version: 1;
  commandId: string;
  kind: ManagementExamKind;
  examId: string;
  action: "archive" | "restore";
  input: ManagementExamVersion;
  createdAt: string;
}

/** 每个身份、考试和动作分别留存回执入口，恢复不能借用另一用户的未知命令。 */
export function managementCommandKey(actorScope: string, kind: ManagementExamKind, examId: string, action: "archive" | "restore") {
  return `exam-management-command:v1:${encodeURIComponent(actorScope)}:${kind}:${encodeURIComponent(examId)}:${action}`;
}

export function loadManagementCommand(storage: Pick<Storage, "getItem">, key: string): ManagementArchiveCommand | null {
  const raw = storage.getItem(key);
  if (!raw) return null;
  const command = JSON.parse(raw) as Partial<ManagementArchiveCommand>;
  if (command.version !== 1 || !command.commandId || !command.examId || !["session", "standalone"].includes(command.kind ?? "")
    || !["archive", "restore"].includes(command.action ?? "") || !command.input || !Number.isFinite(command.input.expected_revision)
    || !command.input.expected_subject_revisions) throw new Error("本地考试操作记录异常，请保留记录并联系管理员");
  return command as ManagementArchiveCommand;
}

export function isManagementCommandUncertain(error: unknown) {
  return !(error instanceof ApiClientError) || error.status >= 500 || [408, 425, 429].includes(error.status)
    || ["operation_in_progress", "operation_outcome_unknown", "idempotency_persist_failed", "idempotency_unavailable"].includes(error.code);
}
