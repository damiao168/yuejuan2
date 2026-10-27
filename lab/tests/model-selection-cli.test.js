import test from "node:test";
import assert from "node:assert/strict";
import { mkdtempSync, mkdirSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { spawnSync } from "node:child_process";
import { fileURLToPath } from "node:url";

const cli = fileURLToPath(new URL("../scripts/compare-model-selection.js", import.meta.url));

function fixture(t) {
  const cwd = mkdtempSync(join(tmpdir(), "model-selection-cli-"));
  t.after(() => rmSync(cwd, { recursive: true, force: true }));
  mkdirSync(join(cwd, "evals", "reports"), { recursive: true });
  for (const [index, id] of ["first", "second", "third"].entries()) {
    writeFileSync(join(cwd, `${id}.json`), JSON.stringify({
      synthetic: true, synthetic_only: true, candidate_id: id,
      evidence_scope: "synthetic_development", model_info: { mock: false },
      dataset: { sha256: "synthetic-same", total_samples: 4 },
      quality: { mae: index / 10, exact_agreement: 1, schema_validity_rate: 1, evidence_validity_rate: 1 },
      operations: { completed: 4, failed: 0, latency_ms_p95: 100 }
    }));
  }
  return cwd;
}

// 必须执行真实入口；仅测比较器本身无法捕获第一个 CLI 参数被丢弃的问题。
for (const count of [2, 3]) {
  test(`default output compares all ${count} reports including the first winner`, (t) => {
    const cwd = fixture(t);
    const ids = ["first", "second", "third"].slice(0, count);
    const result = spawnSync(process.execPath, [cli, ...ids.map((id) => `${id}.json`)], { cwd, encoding: "utf8" });
    assert.equal(result.status, 0, result.stderr);
    const decision = JSON.parse(readFileSync(join(cwd, "evals/reports/model-selection-decision.json"), "utf8"));
    assert.deepEqual(decision.candidates.map((candidate) => candidate.candidate_id), ids);
    assert.equal(decision.selected_local_llm, "first");
  });
}

test("explicit output between reports consumes only the option and its value", (t) => {
  const cwd = fixture(t);
  const result = spawnSync(process.execPath, [cli, "first.json", "--out", "decision.json", "second.json"], { cwd, encoding: "utf8" });
  assert.equal(result.status, 0, result.stderr);
  const decision = JSON.parse(readFileSync(join(cwd, "decision.json"), "utf8"));
  assert.equal(decision.selected_local_llm, "first");
  assert.equal(decision.candidates.length, 2);
});

test("missing or repeated output option is rejected before reading reports", (t) => {
  const cwd = fixture(t);
  for (const suffix of [["--out"], ["--out", ""], ["--out", "--other"], ["--out", "a.json", "--out", "b.json"]]) {
    const result = spawnSync(process.execPath, [cli, "first.json", "second.json", ...suffix], { cwd, encoding: "utf8" });
    assert.notEqual(result.status, 0);
    assert.match(result.stderr, /--out requires exactly one output path/);
  }
});
