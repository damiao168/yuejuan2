#!/usr/bin/env node
import { readFileSync } from "node:fs";
import { createAdapter } from "../src/adapters/index.js";
import { applyEvidenceVerification } from "../src/evidenceVerifier.js";
import { validateGradingOutput } from "../src/schemas/gradingSchema.js";

function readJsonl(path) {
  return readFileSync(path, "utf8").split(/\r?\n/).filter(Boolean).map((line) => JSON.parse(line));
}

const path = process.argv[2] ?? "evals/regression/failed_cases.jsonl";
// 此回归固定使用模拟适配器，只校验规则与输出契约；不能代表真实模型的评分准确度。
const adapter = createAdapter("mock");
const cases = readJsonl(path);
const failures = [];
for (const item of cases) {
  const output = applyEvidenceVerification(item.input, adapter.grade(item.input));
  const validation = validateGradingOutput(output, item.input);
  if (!validation.valid || Math.abs(output.suggested_score - item.expected_output.suggested_score) > 1) {
    failures.push({ case_id: item.case_id, errors: validation.errors, suggested_score: output.suggested_score });
  }
}
console.log(JSON.stringify({ cases: cases.length, failures }, null, 2));
if (failures.length > 0) process.exitCode = 1;
