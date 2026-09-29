// Execute the production syncDraft action body, with its native persistence
// contract modeled as a row update: an unsaved task has no draft row.
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import ts from "typescript";

const source = readFileSync(new URL("../../apps/desktop-client/src/components/OfflineWorkbench.tsx", import.meta.url), "utf8");
const file = ts.createSourceFile("OfflineWorkbench.tsx", source, ts.ScriptTarget.Latest, true, ts.ScriptKind.TSX);
let initializer;
function visit(node) {
  if (ts.isVariableDeclaration(node) && node.name.getText(file) === "syncDraft") initializer = node.initializer;
  ts.forEachChild(node, visit);
}
visit(file);
assert.ok(initializer, "production syncDraft action must exist");
const actionCode = ts.transpile(`(${initializer.getText(file)})`, { target: ts.ScriptTarget.ES2022 });
let accepted = 0;
const statuses = [];
const messages = [];
const rows = new Map();
const deps = {
  pkg: { task: { id: "downloaded-but-unsaved-task", revision: 2 } },
  draft: { score: 7, rubricSelections: {}, comments: "reviewed", privateNote: "", studentFeedback: "", reason: "review" },
  currentEnvelope: undefined, syncStatus: "draft", token: "test-session", isOnline: true,
  setSyncMessage: (value) => messages.push(value),
  updateOfflineDraftStatus: async (id, patch) => {
    // apps/desktop-client/src-tauri/src/durable_store/drafts.rs:90-96:
    // UPDATE draft ... WHERE task_id = ?; rows != 1 -> Err(...).
    if (!rows.has(id)) throw new Error("offline draft was not found");
    rows.set(id, patch);
  },
  durableScopeKey: "synthetic-scope", refreshEnvelopes: async () => {},
  setSyncing: () => {}, setSyncStatus: (value) => statuses.push(value),
  detectConflict: async () => null, client: {}, user: { id: "teacher" },
  submitHumanGrade: async () => { accepted += 1; },
  onLog: async () => {}, formatError: (error) => error.message
};
const syncDraft = new Function(...Object.keys(deps), `return ${actionCode}`)(...Object.values(deps));
await assert.rejects(syncDraft(), /offline draft was not found/);
assert.equal(accepted, 1);
assert.deepEqual(statuses, ["syncing", "synced", "failed"]);
assert.equal(messages.at(-1), "offline draft was not found");
console.log(JSON.stringify({ reproduced: true, backendSubmissionsAccepted: accepted, statuses, finalMessage: messages.at(-1), escapedRejection: "offline draft was not found" }, null, 2));
