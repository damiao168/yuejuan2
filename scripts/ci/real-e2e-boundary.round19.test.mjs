import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";
import ts from "typescript";

const source = readFileSync(new URL("../../tests/e2e/real/response-boundary.ts", import.meta.url), "utf8");
const emitted = ts.transpileModule(source, { compilerOptions: { module: ts.ModuleKind.ESNext } }).outputText;
const { unexpectedForbiddenResponses } = await import(`data:text/javascript;base64,${Buffer.from(emitted).toString("base64")}`);
const response = (url, method = "GET", status = 403) => ({ url: () => url, status: () => status, request: () => ({ method: () => method }) });

test("cross-school 403 exempts its response instance while ordinary/same-URL failures remain", () => {
  const expected = response("https://synthetic.test/api/v1/exams/foreign");
  const own = response("https://synthetic.test/api/v1/exams/own");
  const duplicate = response(expected.url());
  assert.deepEqual(unexpectedForbiddenResponses([expected], expected), []);
  assert.deepEqual(unexpectedForbiddenResponses([expected, own, duplicate], expected), [`GET ${own.url()}`, `GET ${duplicate.url()}`]);
  assert.deepEqual(unexpectedForbiddenResponses([own]), [`GET ${own.url()}`]);
});

test("404 boundary probe does not hide an unrelated authenticated 403", () => {
  const expected = response("https://synthetic.test/api/v1/exams/foreign", "GET", 404);
  const unexpected = response(expected.url(), "POST");
  assert.deepEqual(unexpectedForbiddenResponses([unexpected], expected), [`POST ${unexpected.url()}`]);
});
