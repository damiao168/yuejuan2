import { existsSync, readFileSync } from "node:fs";
import { spawnSync } from "node:child_process";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const ROOT = dirname(dirname(fileURLToPath(import.meta.url)));
const LAB_ROOT = join(ROOT, "lab");
const API_ROOT = join(ROOT, "services", "api-gateway");
const npmCommand = process.platform === "win32" ? "npm.cmd" : "npm";

function run(name, command, args, cwd) {
  const started = performance.now();
  const isWindowsCommand = process.platform === "win32" && command.endsWith(".cmd");
  const executable = isWindowsCommand ? (process.env.ComSpec ?? "cmd.exe") : command;
  const executableArgs = isWindowsCommand ? ["/d", "/s", "/c", command, ...args] : args;
  const result = spawnSync(executable, executableArgs, {
    cwd,
    encoding: "utf8",
    maxBuffer: 32 * 1024 * 1024,
    windowsHide: true
  });
  return {
    name,
    passed: result.status === 0,
    exit_code: result.status,
    elapsed_ms: Math.round(performance.now() - started),
    spawn_error: result.error?.message ?? null,
    stdout_tail: (result.stdout ?? "").split(/\r?\n/).slice(-12).join("\n"),
    stderr_tail: (result.stderr ?? "").split(/\r?\n/).slice(-12).join("\n")
  };
}

function readJson(relativePath) {
  const absolutePath = join(ROOT, relativePath);
  if (!existsSync(absolutePath)) {
    throw new Error(`required integration artifact is missing: ${relativePath}`);
  }
  return JSON.parse(readFileSync(absolutePath, "utf8"));
}

// eval 会重写实验报告，后续门禁读取这次生成的结果；这些证据仅证明开发链路。
const commands = [
  run("lab_unit_tests", npmCommand, ["test"], LAB_ROOT),
  run("lab_synthetic_eval", npmCommand, ["run", "eval"], LAB_ROOT),
  run("lab_development_gate", npmCommand, ["run", "gate:dev"], LAB_ROOT),
  run("main_objective_grading_tests", "go", ["test", "./internal/grading", "-count=1"], API_ROOT)
];

const failures = commands.filter((command) => !command.passed).map((command) => `${command.name} failed`);
let report;
let matrix;
let readiness;
let graduation;
let objectiveEngineSource;

try {
  report = readJson("lab/evals/reports/latest-report.json");
  matrix = readJson("lab/config/capability-matrix.json");
  readiness = readJson("lab/evals/pilot/pilot-candidate-v1/readiness-report.json");
  graduation = readJson("lab/evals/reports/graduation-audit.json");
  objectiveEngineSource = readFileSync(join(API_ROOT, "internal", "grading", "engine.go"), "utf8");
} catch (error) {
  failures.push(error.message);
}

function assert(condition, message) {
  if (!condition) failures.push(message);
}

if (report) {
  assert(report.adapter === "mock", "integration preflight must use the explicitly marked mock adapter");
  assert(report.metrics?.schema_validity_rate === 1, "Lab schema validity must remain 100% in the development gate");
  assert(report.metrics?.evidence_verifier_ran === true, "Lab evidence verifier must run before results are accepted");
  assert(report.metrics?.no_score_above_max === true, "Lab report must prove no score exceeds max_score");
  assert(report.metrics?.mock_marked_rate === 1, "mock outputs must remain explicitly marked");
}

if (matrix) {
  assert(matrix.final_grade_publication_allowed === false, "Lab capability matrix must forbid final-grade publication");
  assert(matrix.default_route?.mode === "human_only", "out-of-scope Lab routes must fail closed to human grading");
  for (const route of matrix.capabilities ?? []) {
    if (route.grader === "rule") {
      assert(
        objectiveEngineSource?.includes(`case "${route.question_type}"`),
        `Lab rule route ${route.question_type} is not represented by the main objective engine`
      );
    }
    if (["llm_assisted", "hybrid_assisted", "shadow"].includes(route.mode)) {
      assert(route.review_policy === "always", `LLM-backed route ${route.subject}:${route.question_type} must require review`);
    }
    if (route.question_type === "essay" || route.question_type === "discussion") {
      assert(route.delivery === "shadow_only", `${route.question_type} must remain shadow-only`);
    }
  }
}

if (readiness) {
  assert(readiness.readiness?.decision === "NOT_READY", "Pilot readiness must remain NOT_READY until real evidence gates pass");
}

if (graduation) {
  assert(graduation.audit_passed === true, "Lab graduation audit must pass before the main-project gate can pass");
  assert(graduation.lab_status === "LAB_IMPLEMENTATION_COMPLETE", "Lab graduation status must be complete");
  assert(graduation.pilot_status === "NOT_READY", "Main project must not claim Pilot readiness from Lab-only evidence");
}

const output = {
  check: "lab-main-project-integration",
  passed: failures.length === 0,
  failures,
  commands,
  boundaries: {
    suggestion_only: matrix?.final_grade_publication_allowed === false,
    pilot_not_ready: readiness?.readiness?.decision === "NOT_READY",
    mock_explicitly_marked: report?.metrics?.mock_marked_rate === 1
  }
};

console.log(JSON.stringify(output, null, 2));
if (failures.length > 0) process.exitCode = 1;
