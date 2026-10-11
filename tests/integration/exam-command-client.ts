import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { createExamSession, recoverExamSessionCommand } from "../../apps/web-admin/src/api/exams";
import { ApiClientError } from "../../apps/web-admin/src/api/client";
import { beginExamCreateCommand, commandAfterFailure, loadExamCreateCommand, persistExamCreateCommand, recordExamCreateSuccess } from "../../apps/web-admin/src/features/exams/create/examCreateCommand";

export async function run(fixture: string) {
  const payload = JSON.parse(await readFile(fixture, "utf8"));
  delete payload.command_id;
  const values = new Map<string,string>();
  const storage = {
    getItem: (key: string) => values.get(key) ?? null,
    setItem: (key: string,value: string) => { values.set(key,value); },
    removeItem: (key: string) => { values.delete(key); },
  };
  const requests: {key: string | null; body: string}[] = [];
  const nativeFetch = globalThis.fetch;
  globalThis.fetch = async (url,init) => {
    if (init?.method === "POST") requests.push({key:new Headers(init.headers).get("Idempotency-Key"),body:String(init.body)});
    return nativeFetch(url,init);
  };
  try {
    const command = beginExamCreateCommand(payload);
    persistExamCreateCommand(storage,"submitted",command);
    let rejected = false;
    try { await createExamSession(command.payload,command.commandId); }
    catch(error) {
      assert.ok(error instanceof ApiClientError);
      assert.equal(error.status,503);
      assert.equal(error.code,"idempotency_persist_failed");
      const retained = commandAfterFailure(command,error);
      assert.ok(retained);
      persistExamCreateCommand(storage,"submitted",retained);
      rejected = true;
    }
    assert.equal(rejected,true,"server must inject failure after committing business facts");
    payload.name = "edited draft must not alter pending request";
    // 只从持久化存储重建页面状态，再通过实际恢复 API
    // 和实际传输重放原始不可变请求体。
    const restored = loadExamCreateCommand(storage,"submitted");
    assert.ok(restored);
    const recovered = await recoverExamSessionCommand(restored.commandId);
    assert.equal(recovered.command.status,"succeeded");
    const replay = await createExamSession(restored.payload,restored.commandId);
    assert.equal(replay.exam_session.id,recovered.command.exam_session?.id);
    assert.deepEqual(requests[0],requests[1]);
    const completed = recordExamCreateSuccess(storage,"submitted","draft",restored,replay.exam_session);
    assert.equal(completed.state,"succeeded");
    const next = beginExamCreateCommand(restored.payload);
    assert.notEqual(next.commandId,restored.commandId);
    const distinct = await createExamSession(next.payload,next.commandId);
    assert.notEqual(distinct.exam_session.id,replay.exam_session.id);
    return { cases:["CMD-02","CMD-04","CMD-07"], commandIds:[restored.commandId,next.commandId], sessionIds:[replay.exam_session.id,distinct.exam_session.id], requests };
  } finally { globalThis.fetch = nativeFetch; }
}
