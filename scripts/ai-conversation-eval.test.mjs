import assert from "node:assert/strict";
import { test } from "node:test";
import { spawnSync } from "node:child_process";
import { fileURLToPath } from "node:url";
import { evaluationConfig, loadEvaluationCases, main, runLiveEvaluation } from "./ai-conversation-eval.mjs";

const base = { endpoint: "https://synthetic.example/v1/chat/completions", apiKey: "synthetic-eval-secret", model: "synthetic-model-v1", maxRequests: 3, maxOutputTokens: 128, timeoutMs: 100 };
const example = { id: "fixture", messages: [{ role: "user", content: "合成问题" }], required: ["回答"], forbidden: ["错误"] };
function response(content = "回答", usage = { prompt_tokens: 10, completion_tokens: 2, total_tokens: 12, prompt_tokens_details: { cached_tokens: 4 } }) {
  return new Response(JSON.stringify({ choices: [{ message: { content }, finish_reason: "stop" }], usage }), { headers: { "Content-Type": "application/json" } });
}

test("offline defaults validate all synthetic fixtures and never dispatch a provider request", async () => {
  const original = globalThis.fetch;
  let calls = 0;
  globalThis.fetch = () => { calls++; throw new Error("unexpected provider call"); };
  try {
    const result = await main([], { YUEJUAN_EVAL_ENDPOINT: base.endpoint, YUEJUAN_EVAL_API_KEY: base.apiKey, YUEJUAN_EVAL_MODEL: base.model });
    assert.equal(result.status, "fixtures_validated");
    assert.equal(result.cases, 12);
    assert.equal(result.live_status, "not_executed");
    assert.equal(calls, 0);
    assert.equal((await loadEvaluationCases()).some(item => item.id === "middle-important-constraint"), true);
  } finally { globalThis.fetch = original; }
});

test("live mode without explicit credentials is reported as not executed", async () => {
  assert.deepEqual(await main(["--live"], {}), { version: 1, mode: "live", status: "not_executed", reason: "missing_explicit_evaluation_configuration" });
});

test("evaluation configuration limits requests and rejects credential-bearing URLs", () => {
  const environment = { YUEJUAN_EVAL_ENDPOINT: base.endpoint, YUEJUAN_EVAL_API_KEY: base.apiKey, YUEJUAN_EVAL_MODEL: base.model };
  assert.equal(evaluationConfig(environment).maxRequests, 9);
  for (const value of ["http://example.org/api", "https://user:password@example.org/api", "https://example.org/api?secret=value", "https://example.org/api#secret"]) {
    assert.throws(() => evaluationConfig({ ...environment, YUEJUAN_EVAL_ENDPOINT: value }));
  }
  assert.throws(() => evaluationConfig({ ...environment, YUEJUAN_EVAL_MAX_REQUESTS: "61" }));
  assert.throws(() => evaluationConfig({ ...environment, YUEJUAN_EVAL_MAX_OUTPUT_TOKENS: "8192" }));
});

test("paired comparison measures one direct call versus two draft calls without disclosing input or secrets", async () => {
  const requests = [];
  const result = await runLiveEvaluation(base, [example, { ...example, id: "not-reached" }], async (url, request) => {
    assert.equal(url, base.endpoint);
    assert.equal(request.redirect, "error");
    assert.equal(request.headers.Authorization, `Bearer ${base.apiKey}`);
    requests.push(JSON.parse(request.body));
    return response();
  });
  assert.equal(result.requests, 3);
  assert.equal(requests.every(item => item.max_tokens === 128 && item.stream === false), true);
  assert.equal(result.strategies.direct.requests, 1);
  assert.equal(result.strategies.draft_then_answer.requests, 2);
  assert.equal(result.strategies.direct.cached_input_tokens, 4);
  assert.equal(result.strategies.draft_then_answer.cached_input_tokens, 8);
  assert.equal(result.strategies.direct.rubric_passed, 1);
  assert.equal(/synthetic-eval-secret|synthetic\.example|合成问题|"content"/u.test(JSON.stringify(result)), false);
  assert.match(result.config_sha256, /^[a-f0-9]{64}$/u);
});

test("live failures preserve fixed diagnostics and do not leak provider response text", async () => {
  const result = await runLiveEvaluation(base, [example], async () => new Response("secret-provider-output", { status: 429 }));
  assert.equal(result.requests, 2);
  assert.equal(result.cases.every(item => item.error_code === "http_429"), true);
  assert.equal(JSON.stringify(result).includes("secret-provider-output"), false);
});

test("per-request deadline aborts the provider request without retry", async () => {
  let calls = 0;
  const result = await runLiveEvaluation(base, [example], (_url, request) => new Promise((_resolve, reject) => {
    calls++;
    request.signal.addEventListener("abort", () => reject(new Error("secret timeout URL")), { once: true });
  }));
  assert.equal(calls, 2);
  assert.equal(result.cases.every(item => item.error_code === "timeout"), true);
  assert.equal(JSON.stringify(result).includes("secret timeout URL"), false);
});

test("truncated paid responses retain returned usage while refusing a success result", async () => {
  const result = await runLiveEvaluation(base, [example], async () => new Response(JSON.stringify({
    choices: [{ message: { content: "未完成回答" }, finish_reason: "length" }], usage: { prompt_tokens: 9, completion_tokens: 8, total_tokens: 17 },
  })));
  assert.equal(result.cases.every(item => item.error_code === "incomplete_response" && item.total_tokens === 17), true);
});

test("large upstream bodies are bounded and never become report content", async () => {
  const result = await runLiveEvaluation(base, [example], async () => new Response("x".repeat(2 * 1024 * 1024 + 1)));
  assert.equal(result.cases.every(item => item.error_code === "response_too_large"), true);
});

test("CLI missing credentials does not expose unrelated environment variables", () => {
  const environment = { ...process.env, PRIVATE_TEST_SECRET: "never-print-this", YUEJUAN_EVAL_ENDPOINT: "", YUEJUAN_EVAL_API_KEY: "", YUEJUAN_EVAL_MODEL: "" };
  const child = spawnSync(process.execPath, [fileURLToPath(new URL("./ai-conversation-eval.mjs", import.meta.url)), "--live"], { encoding: "utf8", env: environment });
  assert.equal(child.status, 0, child.stderr);
  assert.equal(JSON.parse(child.stdout).status, "not_executed");
  assert.equal(`${child.stdout}${child.stderr}`.includes("never-print-this"), false);
});
