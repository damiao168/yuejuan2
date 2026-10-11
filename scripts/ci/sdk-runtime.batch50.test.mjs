import assert from "node:assert/strict";
import { test } from "node:test";
import { appendQuery, fillPath } from "../../packages/sdk/src/runtime.ts";

test("路径参数只编码参数值，保留空占位符和未闭合文本", () => {
  assert.equal(fillPath("/items/{id}/{id}?page={page}", { id: "中文/a ?#", page: 0 }), "/items/%E4%B8%AD%E6%96%87%2Fa%20%3F%23/%E4%B8%AD%E6%96%87%2Fa%20%3F%23?page=0");
  assert.equal(fillPath("/items/{}{id}/{missing", { id: "42" }), "/items/{}42/{missing");
  assert.equal(fillPath("/items/{{id}/tail}", { "{id": "a/b" }), "/items/a%2Fb/tail}");
  assert.equal(fillPath("/items/no-parameters", {}), "/items/no-parameters");
});

test("缺少非空路径参数时仍报告首个缺失的参数", () => {
  for (const value of [undefined, null, ""]) {
    assert.throws(() => fillPath("/{id}/{next}", { id: value, next: "ok" }), { message: "Missing path parameter: id" });
  }
});

test("长未闭合路径不需要寻找每个左括号的替代匹配", () => {
  const path = `/items/${"{".repeat(100_000)}`;
  assert.equal(fillPath(path, {}), path);
  const emptyPlaceholders = "{}".repeat(10_000);
  assert.equal(fillPath(`${emptyPlaceholders}{id}`, { id: "ok" }), `${emptyPlaceholders}ok`);
});

test("查询保留零和false，数组重复键与顶层空值过滤保持", () => {
  const path = appendQuery("/items", { page: 0, enabled: false, q: "中文/a", tag: ["a", "b", 0, false], empty: "", missing: undefined, absent: null });
  const url = new URL(path, "https://example.test");
  assert.equal(url.pathname, "/items");
  assert.equal(url.searchParams.get("page"), "0");
  assert.equal(url.searchParams.get("enabled"), "false");
  assert.equal(url.searchParams.get("q"), "中文/a");
  assert.deepEqual(url.searchParams.getAll("tag"), ["a", "b", "0", "false"]);
  for (const key of ["empty", "missing", "absent"]) assert.equal(url.searchParams.has(key), false);
  assert.equal(appendQuery("/items", undefined), "/items");
  assert.equal(appendQuery("/items", { tag: [] }), "/items");
});

test("查询数组中的原有标量转换语义保持", () => {
  const path = appendQuery("/items", { value: [null, undefined, "", 1n, Symbol("value")] });
  assert.deepEqual(new URL(path, "https://example.test").searchParams.getAll("value"), ["null", "undefined", "", "1", "Symbol(value)"]);
});

test("对象和嵌套数组不能变成无意义查询文本", () => {
  for (const value of [{ id: "a" }, new Date("2026-01-01T00:00:00Z"), () => "a", [{ id: "a" }], [["a"]]]) {
    assert.throws(() => appendQuery("/items", { filter: value }), { name: "TypeError", message: "Unsupported query parameter: filter" });
  }
});
