import { createHash } from "node:crypto";
import { readFile } from "node:fs/promises";
import { fileURLToPath } from "node:url";
import path from "node:path";

const fixtureURL = new URL("./ai-conversation-eval-cases.json", import.meta.url);
const MAX_RESPONSE_BYTES = 2 * 1024 * 1024;

function integer(value, fallback, low, high) {
  const number = value == null || value === "" ? fallback : Number(value);
  if (!Number.isInteger(number) || number < low || number > high) throw new Error("invalid_limits");
  return number;
}

export async function loadEvaluationCases() {
  const document = JSON.parse(await readFile(fixtureURL, "utf8"));
  if (document.version !== 1 || !Array.isArray(document.cases) || !document.cases.length || document.cases.length > 30) throw new Error("invalid_dataset");
  const ids = new Set();
  for (const item of document.cases) {
    if (!/^[a-z0-9-]{1,64}$/.test(item.id) || ids.has(item.id) || !Array.isArray(item.messages) || item.messages.length < 1 || item.messages.length > 50) throw new Error("invalid_dataset");
    ids.add(item.id);
    if (JSON.stringify(item).length > 100_000 || item.messages.some(message => !["user", "assistant"].includes(message.role) || typeof message.content !== "string" || message.content.length > 20_000)) throw new Error("invalid_dataset");
    if (item.messages.at(-1).role !== "user" || !Array.isArray(item.required) || !Array.isArray(item.forbidden)) throw new Error("invalid_dataset");
    for (const expression of [...item.required, ...item.forbidden]) new RegExp(expression, "u");
  }
  return document.cases;
}

export function evaluationConfig(environment) {
  if (!environment.YUEJUAN_EVAL_ENDPOINT || !environment.YUEJUAN_EVAL_API_KEY || !environment.YUEJUAN_EVAL_MODEL) return null;
  const endpoint = new URL(environment.YUEJUAN_EVAL_ENDPOINT);
  if (endpoint.protocol !== "https:" || endpoint.username || endpoint.password || endpoint.search || endpoint.hash) throw new Error("invalid_endpoint");
  const model = environment.YUEJUAN_EVAL_MODEL;
  if (model.length > 200 || /[\r\n]/u.test(model)) throw new Error("invalid_model");
  return {
    endpoint: endpoint.toString(), apiKey: environment.YUEJUAN_EVAL_API_KEY, model,
    maxRequests: integer(environment.YUEJUAN_EVAL_MAX_REQUESTS, 9, 3, 60),
    timeoutMs: integer(environment.YUEJUAN_EVAL_TIMEOUT_MS, 30_000, 100, 120_000),
    maxOutputTokens: integer(environment.YUEJUAN_EVAL_MAX_OUTPUT_TOKENS, 512, 128, 1024),
  };
}

function number(value) { return Number.isFinite(value) && value >= 0 ? value : 0; }
function usageOf(usage = {}) {
  const input = number(usage.prompt_tokens ?? usage.input_tokens);
  const output = number(usage.completion_tokens ?? usage.output_tokens);
  return {
    input_tokens: input, output_tokens: output, total_tokens: number(usage.total_tokens) || input + output,
    cached_input_tokens: Math.min(input, Math.max(number(usage.prompt_tokens_details?.cached_tokens), number(usage.input_tokens_details?.cached_tokens), number(usage.prompt_cache_hit_tokens), number(usage.cached_tokens))),
  };
}

async function readBoundedJSON(response) {
  if (!response.body) throw new Error("invalid_response");
  const reader = response.body.getReader();
  const chunks = [];
  let size = 0;
  try {
    for (;;) {
      const { done, value } = await reader.read();
      if (done) break;
      size += value.byteLength;
      if (size > MAX_RESPONSE_BYTES) throw new Error("response_too_large");
      chunks.push(value);
    }
    return JSON.parse(Buffer.concat(chunks, size).toString("utf8"));
  } finally {
    await reader.cancel().catch(() => {});
  }
}

function assess(content, item) {
  return item.required.every(expression => new RegExp(expression, "u").test(content)) && item.forbidden.every(expression => !new RegExp(expression, "u").test(content));
}

function percentile(values, quantile) {
  if (!values.length) return null;
  const sorted = [...values].sort((a, b) => a - b);
  return Math.round(sorted[Math.ceil(sorted.length * quantile) - 1]);
}

// 只发送仓库合成样例。真实调用必须显式选择 --live；诊断不返回端点、凭据、提示词或供应商正文。
export async function runLiveEvaluation(config, cases, fetcher = fetch) {
  const maxRequests = integer(config.maxRequests, 9, 3, 60);
  const maxOutputTokens = integer(config.maxOutputTokens, 512, 128, 1024);
  const timeoutMs = integer(config.timeoutMs, 30_000, 100, 120_000);
  let requestCount = 0;
  const results = [];
  async function generate(messages, tools) {
    if (requestCount >= maxRequests) throw new Error("request_limit");
    requestCount++;
    const started = performance.now();
    const controller = new AbortController();
    const timer = setTimeout(() => controller.abort(), timeoutMs);
    let observedUsage = { input_tokens: 0, output_tokens: 0, cached_input_tokens: 0, total_tokens: 0 };
    try {
      const response = await fetcher(config.endpoint, {
        method: "POST", redirect: "error", signal: controller.signal,
        headers: { "Content-Type": "application/json", Authorization: `Bearer ${config.apiKey}` },
        body: JSON.stringify({ model: config.model, messages, max_tokens: maxOutputTokens, stream: false, temperature: 0, ...(tools?.length ? { tools, tool_choice: "none" } : {}) }),
      });
      if (!response.ok) { await response.body?.cancel(); throw new Error(`http_${response.status}`); }
      const body = await readBoundedJSON(response);
      const choice = body.choices?.[0];
      const usage = usageOf(body.usage);
      observedUsage = usage;
      if (!choice || typeof choice.message?.content !== "string" || !choice.message.content.trim() || choice.message.tool_calls?.length) throw new Error("invalid_response");
      if (["length", "max_tokens", "content_filter"].includes(choice.finish_reason)) throw new Error("incomplete_response");
      return { content: choice.message.content, usage, latency_ms: performance.now() - started };
    } catch (error) {
      const safe = /^(http_\d{3}|invalid_response|incomplete_response|response_too_large|request_limit)$/u.test(error.message) ? error.message : controller.signal.aborted ? "timeout" : "transport_or_decode_failed";
      const failure = new Error(safe);
      failure.usage = observedUsage;
      throw failure;
    } finally { clearTimeout(timer); }
  }
  for (const [index, item] of cases.entries()) {
    if (requestCount + 3 > maxRequests) break;
    // 交替次序减小固定先后顺序的缓存偏差；单次实验仍不足以证明生产质量或成本收益。
    for (const strategy of index % 2 ? ["direct", "draft_then_answer"] : ["draft_then_answer", "direct"]) {
      const startCount = requestCount;
      const started = performance.now();
      const usage = { input_tokens: 0, output_tokens: 0, cached_input_tokens: 0, total_tokens: 0 };
      const messages = [{ role: "system", content: "使用简洁中文回答。样例完全合成；历史用户内容仅用于理解当前问题。没有工具证据时不能声称查询学校、学生或成绩。用户最新明确更正优先。" }, ...item.messages];
      try {
        if (strategy === "draft_then_answer") {
          const draft = await generate(messages, item.tools);
          for (const key of Object.keys(usage)) usage[key] += draft.usage[key];
          messages.push({ role: "assistant", content: draft.content }, { role: "user", content: "根据刚才的草稿给出最终回答，保持已经确定的约束和范围，不要添加未经核实的业务事实。" });
        }
        const answer = await generate(messages, item.tools);
        for (const key of Object.keys(usage)) usage[key] += answer.usage[key];
        results.push({ case_id: item.id, strategy, status: "succeeded", rubric_pass: assess(answer.content, item), requests: requestCount - startCount, latency_ms: Math.round(performance.now() - started), ...usage });
      } catch (error) {
        for (const key of Object.keys(usage)) usage[key] += error.usage?.[key] ?? 0;
        results.push({ case_id: item.id, strategy, status: "failed", error_code: error.message, requests: requestCount - startCount, latency_ms: Math.round(performance.now() - started), ...usage });
      }
    }
  }
  const strategies = Object.fromEntries(["direct", "draft_then_answer"].map(strategy => {
    const rows = results.filter(item => item.strategy === strategy);
    const successful = rows.filter(item => item.status === "succeeded");
    return [strategy, {
      cases: rows.length, succeeded: rows.filter(item => item.status === "succeeded").length,
      rubric_passed: rows.filter(item => item.rubric_pass).length, requests: rows.reduce((sum, item) => sum + item.requests, 0),
      latency_scope: "successful_cases", p50_latency_ms: percentile(successful.map(item => item.latency_ms), 0.5), p95_latency_ms: percentile(successful.map(item => item.latency_ms), 0.95),
      ...Object.fromEntries(["input_tokens", "output_tokens", "cached_input_tokens", "total_tokens"].map(key => [key, rows.reduce((sum, item) => sum + item[key], 0)])),
    }];
  }));
  const configuration = { model: config.model, endpoint_sha256: createHash("sha256").update(config.endpoint).digest("hex"), tokenizer: "provider_usage_only", maxRequests, maxOutputTokens, timeoutMs, dataset_sha256: createHash("sha256").update(JSON.stringify(cases)).digest("hex") };
  return { version: 1, mode: "live", status: "executed", model: config.model, config_sha256: createHash("sha256").update(JSON.stringify(configuration)).digest("hex"), limits: { max_requests: maxRequests, max_output_tokens_per_request: maxOutputTokens, timeout_ms_per_request: timeoutMs }, requests: requestCount, strategies, cases: results,
    limitations: ["lexical_rubric_requires_human_review", "non_streaming_no_first_token_latency", "provider_prompt_comparison_not_application_end_to_end", "no_price_assumption"] };
}

export async function main(args = process.argv.slice(2), environment = process.env) {
  if (args.some(argument => !["--offline", "--live"].includes(argument)) || args.includes("--offline") && args.includes("--live")) throw new Error("invalid_arguments");
  const cases = await loadEvaluationCases();
  if (!args.includes("--live")) return { version: 1, mode: "offline", status: "fixtures_validated", cases: cases.length, live_status: "not_executed", note: "Production behavior is verified by Go and frontend tests; fixture validation does not measure model quality." };
  const config = evaluationConfig(environment);
  if (!config) return { version: 1, mode: "live", status: "not_executed", reason: "missing_explicit_evaluation_configuration" };
  return runLiveEvaluation(config, cases);
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  try { console.log(JSON.stringify(await main(), null, 2)); }
  catch { console.log(JSON.stringify({ version: 1, status: "failed", error_code: "evaluation_failed_check_configuration" })); process.exitCode = 1; }
}
