#!/usr/bin/env node
import { readFileSync, writeFileSync } from "node:fs";
import { compareModelSelectionReports } from "../src/evaluators/modelSelection.js";

const args = process.argv.slice(2);
let output = "evals/reports/model-selection-decision.json";
let hasOutput = false;
const reportPaths = [];
// 只消费实际出现的输出选项；没有 --out 时，第一个位置参数仍是一份报告。
for (let index = 0; index < args.length; index += 1) {
  const item = args[index];
  if (item === "--out") {
    if (hasOutput || !args[index + 1] || args[index + 1].startsWith("--")) {
      throw new Error("--out requires exactly one output path and cannot be repeated");
    }
    hasOutput = true;
    output = args[++index];
  } else if (item.startsWith("--")) {
    throw new Error(`Unknown option: ${item}`);
  } else {
    reportPaths.push(item);
  }
}
if (reportPaths.length < 2) throw new Error("Usage: compare-model-selection report1.json report2.json [report3.json] --out decision.json");
const reports = reportPaths.map((path) => JSON.parse(readFileSync(path, "utf8")));
const decision = { generated_at: new Date().toISOString(), ...compareModelSelectionReports(reports) };
writeFileSync(output, `${JSON.stringify(decision, null, 2)}\n`, "utf8");
console.log(JSON.stringify(decision, null, 2));
