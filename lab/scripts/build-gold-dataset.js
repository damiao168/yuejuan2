#!/usr/bin/env node
import { mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { dirname } from "node:path";
import { buildGoldRecord, checkAgreementGate, computeInterRaterAgreement, loadAnnotationPolicy, validateAnnotationBundle } from "../src/annotations/gold.js";

function parseArgs(argv) {
  const args = {};
  for (let index = 0; index < argv.length; index += 1) {
    if (!argv[index].startsWith("--")) continue;
    args[argv[index].slice(2)] = argv[index + 1];
    index += 1;
  }
  return args;
}
const args = parseArgs(process.argv.slice(2));
if (!args.input || !args.out || !args.report) throw new Error("Usage: build-gold-dataset --input bundles.jsonl --out gold.jsonl --report report.json [--gate dev]");
const bundles = readFileSync(args.input, "utf8").split(/\r?\n/).filter(Boolean).map((line) => JSON.parse(line));
const policy = loadAnnotationPolicy();
const invalid = bundles.map((bundle) => ({ bundle, validation: validateAnnotationBundle(bundle, policy) })).filter((item) => !item.validation.valid);
if (invalid.length) throw new Error(`Invalid annotation bundles: ${invalid.map((item) => `${item.bundle.bundle_id}: ${item.validation.errors.join("; ")}`).join(" | ")}`);
const gold = bundles.map((bundle) => buildGoldRecord(bundle, policy));
const metrics = computeInterRaterAgreement(bundles, policy);
const gate = checkAgreementGate(metrics, args.gate ?? "dev", policy);
mkdirSync(dirname(args.out), { recursive: true });
mkdirSync(dirname(args.report), { recursive: true });
// 门禁未通过也写出数据与诊断报告供复查；是否可继续使用须检查退出码和报告中的 gate。
writeFileSync(args.out, gold.map((record) => JSON.stringify(record)).join("\n") + (gold.length ? "\n" : ""), "utf8");
writeFileSync(args.report, `${JSON.stringify({ schema_version: "annotation-reliability-report-v1", generated_at: new Date().toISOString(), synthetic_only: bundles.every((bundle) => bundle.synthetic), metrics, gate }, null, 2)}\n`, "utf8");
console.log(JSON.stringify({ gold_records: gold.length, synthetic_only: bundles.every((bundle) => bundle.synthetic), metrics, gate }, null, 2));
if (!gate.passed) process.exitCode = 1;
