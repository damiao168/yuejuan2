#!/usr/bin/env node
import { createHash } from "node:crypto";
import { readFileSync, writeFileSync } from "node:fs";
import { evaluateCalibration, fitIsotonicCalibration } from "../src/evaluators/calibration.js";
import { checkCalibrationFairnessGate, evaluateOperationalSlices } from "../src/evaluators/fairness.js";

const inputIndex = process.argv.indexOf("--input");
const outIndex = process.argv.indexOf("--out");
const levelIndex = process.argv.indexOf("--level");
const input = inputIndex >= 0 ? process.argv[inputIndex + 1] : "evals/calibration/synthetic-observations.json";
const output = outIndex >= 0 ? process.argv[outIndex + 1] : "evals/reports/calibration-fairness.json";
const level = levelIndex >= 0 ? process.argv[levelIndex + 1] : "dev";
const text = readFileSync(input, "utf8");
const observations = JSON.parse(text);
const calibrationSet = observations.filter((item) => item.partition === "calibration");
const evaluationSet = observations.filter((item) => item.partition === "evaluation");
if (!calibrationSet.length || !evaluationSet.length) throw new Error("both calibration and evaluation partitions are required");
// 拟合只使用 calibration 分区；evaluation 分区留给独立度量，避免用同一批结果证明校准效果。
const model = fitIsotonicCalibration(calibrationSet);
const calibration = evaluateCalibration(evaluationSet, model);
const config = JSON.parse(readFileSync("config/calibration-gates.json", "utf8"));
const fairness = evaluateOperationalSlices(calibration.predictions, config[level].minimum_records_per_slice);
const gate = checkCalibrationFairnessGate(calibration, fairness, level, config);
const report = {
  schema_version: "calibration-fairness-report-v1",
  generated_at: new Date().toISOString(),
  synthetic_only: observations.every((item) => item.synthetic === true),
  promotable_to_runtime: false,
  input_sha256: createHash("sha256").update(text).digest("hex"),
  partitions: { calibration: calibrationSet.length, evaluation: evaluationSet.length },
  calibration_model: model,
  calibration: { ...calibration, predictions: calibration.predictions.map((item) => ({ observation_id: item.observation.observation_id, signal: item.signal, probability: item.probability, outcome: item.outcome })) },
  fairness,
  gate
};
writeFileSync(output, `${JSON.stringify(report, null, 2)}\n`, "utf8");
console.log(JSON.stringify(report, null, 2));
if (!gate.passed) process.exitCode = 1;
