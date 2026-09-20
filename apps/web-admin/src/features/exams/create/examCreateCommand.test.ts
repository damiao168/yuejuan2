import { describe, expect, it } from "vitest";
import { ApiClientError } from "../../../api/client";
import { beginExamCreateCommand, commandAfterFailure, commandAfterSuccess, commandRecoveryDelayMS, createExamCreateSubmissionGate, loadExamCreateCommand, nextCommandRecovery, persistExamCreateCommand, recordExamCreateSuccess } from "./examCreateCommand";

const payload = {
  school_id: "school-1", grade_id: "grade-1", name: "期中考试", exam_type: "formal_exam",
  grading_mode: "ai_assisted", appeal_enabled: true, publish_policy: "after_admin_approval",
  class_ids: ["class-1"], subjects: [{ subject: "math", total_score: 100, duration_minutes: 90, candidate_rule: "all_selected_classes", class_ids: [], sections: [] }]
};

function storage() {
  const values = new Map<string, string>();
  return { getItem: (key: string) => values.get(key) ?? null, setItem: (key: string, value: string) => values.set(key, value) };
}

describe("exam create command recovery", () => {
  it("persists the immutable payload before transport and restores it after reload", () => {
    const command = beginExamCreateCommand(payload, "command-1234");
    const target = storage();
    persistExamCreateCommand(target, "key", command);
    expect(loadExamCreateCommand(target, "key")).toEqual(command);
    expect(loadExamCreateCommand(target, "key:operation:command-1234")).toEqual(command);
  });

  it("persists the creation mode so recovery keeps the original destination", () => {
    const command = beginExamCreateCommand(payload, "command-with-mode", "quick");
    const target = storage();
    persistExamCreateCommand(target, "key", command);
    expect(loadExamCreateCommand(target, "key")?.creationMode).toBe("quick");
  });

  it("keeps separate operation receipts when a new explicit draft is submitted", () => {
    const target = storage();
    const first = beginExamCreateCommand(payload,"operation-first");
    const next = beginExamCreateCommand({...payload,name:"another draft"},"operation-next");
    persistExamCreateCommand(target,"user-key",first);
    persistExamCreateCommand(target,"user-key",next);
    expect(loadExamCreateCommand(target,"user-key:operation:operation-first")).toEqual(first);
    expect(loadExamCreateCommand(target,"user-key:operation:operation-next")).toEqual(next);
    expect(loadExamCreateCommand(target,"other-user-key")).toBeNull();
  });

  it("separates the submitted snapshot from subsequent nested draft edits", () => {
    const draft = structuredClone(payload);
    const command = beginExamCreateCommand(draft, "immutable-command");
    draft.class_ids.push("another-class");
    draft.subjects[0]!.total_score = 200;
    expect(command.payload).toEqual(payload);
  });

  it.each(["unauthenticated", "access_scope_forbidden"])("keeps an unknown operation across %s on a later attempt", (code) => {
    const command = { ...beginExamCreateCommand(payload, "lost-response"), state: "unknown" as const };
    expect(commandAfterFailure(command, new ApiClientError(403, code, "relogin"))).toMatchObject({ commandId: command.commandId, state: "unknown", payload });
  });

  it.each(["receipt", "draft"])("retains definitive success when local %s storage fails", (failure) => {
    const target = storage();
    const command = beginExamCreateCommand(payload, "committed-command");
    persistExamCreateCommand(target, "key", command);
    const completed = recordExamCreateSuccess({
      setItem(key, value) { if (failure === "receipt") throw new Error("quota"); target.setItem(key, value); },
      removeItem() { if (failure === "draft") throw new Error("storage unavailable"); }
    }, "key", "draft", command, { id: "session-1", exams: [{ id: "exam-1" }] } as never);
    expect(completed).toMatchObject({ state: "succeeded", commandId: "committed-command", result: { examSessionId: "session-1", examIds: ["exam-1"] } });
    expect(loadExamCreateCommand(target, "key")?.commandId).toBe(command.commandId);
  });

  it.each([
    new Error("connection reset"),
    new ApiClientError(503, "request_failed", "unavailable"),
    new ApiClientError(503, "idempotency_persist_failed", "unknown result"),
    new ApiClientError(409, "operation_in_progress", "still running"),
    new ApiClientError(429, "rate_limited", "retry")
  ])("retains command id and submitted payload when the result is unknown", (error) => {
    const command = beginExamCreateCommand(payload, "command-1234");
    const recovered = commandAfterFailure(command, error);
    expect(recovered?.commandId).toBe("command-1234");
    expect(recovered?.payload).toEqual(payload);
    expect(recovered?.state).toBe("unknown");
  });

  it("does not hide a request fingerprint conflict behind a new id", () => {
    const command = beginExamCreateCommand(payload, "command-1234");
    const recovered = commandAfterFailure(command, new ApiClientError(409, "idempotency_key_reused_with_different_request", "conflict"));
    expect(recovered).toMatchObject({ commandId: "command-1234", state: "conflict", payload });
  });

  it("records the durable resource association after success", () => {
    const command = beginExamCreateCommand(payload, "command-1234");
    const completed = commandAfterSuccess(command, { id: "session-1", school_id: "school-1", grade_id: "grade-1", name: "期中考试", exam_type: "formal_exam", status: "draft", exams: [{ id: "exam-1" }] } as never);
    expect(completed).toMatchObject({ state: "succeeded", result: { examSessionId: "session-1", examIds: ["exam-1"] } });
  });

  it("releases only an explicitly rejected pre-write command and allocates a new id", () => {
    const rejected = beginExamCreateCommand(payload, "rejected-command");
    expect(commandAfterFailure(rejected, new ApiClientError(400, "invalid_exam_session", "invalid"))).toBeNull();
    expect(beginExamCreateCommand(payload, "corrected-command").commandId).not.toBe(rejected.commandId);
  });

  it("honors Retry-After and bounds command recovery polling", () => {
    const command = commandAfterFailure(beginExamCreateCommand(payload, "command-1234"), new ApiClientError(429, "rate_limited", "retry", {}, 12));
    expect(command).not.toBeNull();
    expect(commandRecoveryDelayMS(command!)).toBeGreaterThanOrEqual(11_000);
    let current = command!;
    for (let attempt = 0; attempt < 8; attempt += 1) current = nextCommandRecovery(current)!;
    expect(nextCommandRecovery(current)).toBeNull();
  });

  it("rejects a duplicate submit before React can rerender", () => {
    const gate = createExamCreateSubmissionGate();
    expect(gate.enter()).toBe(true);
    expect(gate.enter()).toBe(false);
    gate.leave();
    expect(gate.enter()).toBe(true);
  });
});
