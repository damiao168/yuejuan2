import { readFileSync } from "node:fs";
import { ADVERSARIAL_AGENT_IDS, createAdversarialAgents } from "../adversaries/index.js";

const AGGREGATE_FIELDS = [
  "attack_count",
  "control_count",
  "attack_family_count",
  "control_family_count",
  "attack_recall",
  "control_false_positive_rate"
];

function aggregateResults(report, reasons) {
  const prefix = report.agent_id;
  if (!Array.isArray(report.results)) {
    reasons.push(`${prefix}.results must be an array`);
    return null;
  }

  const caseIds = new Set();
  let malformed = false;
  for (const [index, result] of report.results.entries()) {
    const itemPrefix = `${prefix}.results[${index}]`;
    if (!result || typeof result !== "object") {
      reasons.push(`${itemPrefix} must be an object`);
      malformed = true;
      continue;
    }
    if (typeof result.case_id !== "string" || !result.case_id.trim()) {
      reasons.push(`${itemPrefix}.case_id must be a non-empty string`);
      malformed = true;
    } else if (caseIds.has(result.case_id)) {
      reasons.push(`${prefix}.results contains duplicate case_id ${result.case_id}`);
      malformed = true;
    } else {
      caseIds.add(result.case_id);
    }
    if (typeof result.family !== "string" || !result.family.trim()) {
      reasons.push(`${itemPrefix}.family must be a non-empty string`);
      malformed = true;
    }
    if (typeof result.attack !== "boolean" || typeof result.detected !== "boolean") {
      reasons.push(`${itemPrefix}.attack and detected must be booleans`);
      malformed = true;
    }
  }
  if (malformed) return null;

  const attacks = report.results.filter((item) => item.attack);
  const controls = report.results.filter((item) => !item.attack);
  return {
    attack_count: attacks.length,
    control_count: controls.length,
    attack_family_count: new Set(attacks.map((item) => item.family)).size,
    control_family_count: new Set(controls.map((item) => item.family)).size,
    attack_recall: attacks.length ? attacks.filter((item) => item.detected).length / attacks.length : 0,
    control_false_positive_rate: controls.length ? controls.filter((item) => item.detected).length / controls.length : 0
  };
}

function aggregateMatches(reported, calculated) {
  return Number.isFinite(reported) && Math.abs(reported - calculated) < 1e-12;
}

export function evaluateAdversarialAgent(agent) {
  const cases = agent.generate();
  const results = cases.map((testCase) => ({ case_id: testCase.case_id, family: testCase.family, attack: testCase.attack, detected: agent.evaluate(testCase) }));
  const attacks = results.filter((item) => item.attack);
  const controls = results.filter((item) => !item.attack);
  return {
    agent_id: agent.agent_id,
    attack_count: attacks.length,
    control_count: controls.length,
    attack_family_count: new Set(attacks.map((item) => item.family)).size,
    control_family_count: new Set(controls.map((item) => item.family)).size,
    attack_recall: attacks.length ? attacks.filter((item) => item.detected).length / attacks.length : 0,
    control_false_positive_rate: controls.length ? controls.filter((item) => item.detected).length / controls.length : 0,
    results
  };
}

export function runAdversarialSuite() {
  return createAdversarialAgents().map(evaluateAdversarialAgent);
}

export function checkAdversarialGate(reports, level, configPath = "config/adversarial-gates.json") {
  const config = JSON.parse(readFileSync(configPath, "utf8"));
  const gate = config[level];
  if (!gate) throw new Error(`Unknown adversarial gate: ${level}`);
  const reasons = [];
  if (!Array.isArray(reports)) return { level, passed: false, reasons: ["reports must be an array"] };

  const expectedIds = new Set(ADVERSARIAL_AGENT_IDS);
  const reportGroups = new Map(ADVERSARIAL_AGENT_IDS.map((agentId) => [agentId, []]));
  for (const [index, report] of reports.entries()) {
    if (!report || typeof report !== "object" || typeof report.agent_id !== "string") {
      reasons.push(`reports[${index}].agent_id must be a string`);
      continue;
    }
    if (!expectedIds.has(report.agent_id)) {
      reasons.push(`unexpected adversarial agent ${report.agent_id}`);
      continue;
    }
    reportGroups.get(report.agent_id).push(report);
  }

  for (const agentId of ADVERSARIAL_AGENT_IDS) {
    const matches = reportGroups.get(agentId);
    if (matches.length === 0) {
      reasons.push(`missing adversarial agent ${agentId}`);
      continue;
    }
    if (matches.length > 1) reasons.push(`duplicate adversarial agent ${agentId}`);

    const report = matches[0];
    // 从逐条用例重算汇总值，防止报告中的比例与实际攻击/对照样本不一致。
    const calculated = aggregateResults(report, reasons);
    if (!calculated) continue;
    for (const field of AGGREGATE_FIELDS) {
      if (!aggregateMatches(report[field], calculated[field])) {
        reasons.push(`${agentId}.${field} does not match results`);
      }
    }

    if (calculated.attack_count < gate.minimum_attacks_per_agent) reasons.push(`${agentId}.attack_count ${calculated.attack_count} < ${gate.minimum_attacks_per_agent}`);
    if (calculated.control_count < gate.minimum_controls_per_agent) reasons.push(`${agentId}.control_count ${calculated.control_count} < ${gate.minimum_controls_per_agent}`);
    if (calculated.attack_family_count < gate.minimum_attack_families_per_agent) reasons.push(`${agentId}.attack_family_count ${calculated.attack_family_count} < ${gate.minimum_attack_families_per_agent}`);
    if (calculated.control_family_count < gate.minimum_control_families_per_agent) reasons.push(`${agentId}.control_family_count ${calculated.control_family_count} < ${gate.minimum_control_families_per_agent}`);
    if (calculated.attack_recall < gate.minimum_attack_recall) reasons.push(`${agentId}.attack_recall ${calculated.attack_recall} < ${gate.minimum_attack_recall}`);
    if (calculated.control_false_positive_rate > gate.maximum_control_false_positive_rate) {
      reasons.push(`${agentId}.control_false_positive_rate ${calculated.control_false_positive_rate} > ${gate.maximum_control_false_positive_rate}`);
    }
  }
  return { level, passed: reasons.length === 0, reasons };
}
