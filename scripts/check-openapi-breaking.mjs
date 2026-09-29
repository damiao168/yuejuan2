import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { createBreakingBaseline, readOpenApi } from "./lib/openapi-sdk.mjs";

// 此门禁比较已提取的契约子集，未覆盖全部 OpenAPI 语义；通过不能代替接口兼容性评审。
export function findBreakingChanges(baseline, current) {
  const failures = [];

  function compareSchema(name, before, after, location = name) {
    if (before === null || typeof before !== "object") {
      if (JSON.stringify(before) !== JSON.stringify(after)) failures.push(`changed schema ${location}`);
      return;
    }
    if (Array.isArray(before)) {
      if (!Array.isArray(after)) failures.push(`changed schema ${location}`);
      return;
    }
    if (!after || typeof after !== "object") {
      failures.push(`removed schema shape ${location}`);
      return;
    }
    if (before.type && before.type !== after.type) failures.push(`changed type for ${location}`);
    if (before.$ref && before.$ref !== after.$ref) failures.push(`changed ref for ${location}`);
    if (Array.isArray(before.enum)) {
      for (const value of before.enum) if (!after.enum?.includes(value)) failures.push(`removed enum value ${JSON.stringify(value)} from ${location}`);
    }
    const beforeRequired = new Set(before.required ?? []);
    const afterRequired = new Set(after.required ?? []);
    for (const required of beforeRequired) if (!afterRequired.has(required)) failures.push(`made required field optional ${location}.${required}`);
    for (const required of afterRequired) if (!beforeRequired.has(required)) failures.push(`made optional field required ${location}.${required}`);
    for (const [property, schema] of Object.entries(before.properties ?? {})) {
      if (!Object.hasOwn(after.properties ?? {}, property)) failures.push(`removed property ${location}.${property}`);
      else compareSchema(name, schema, after.properties[property], `${location}.${property}`);
    }
    if (before.items) compareSchema(name, before.items, after.items, `${location}[]`);
  }

  for (const [key, operation] of Object.entries(baseline.operations)) {
    const candidate = current.operations[key];
    if (!candidate) {
      failures.push(`removed operation ${key}`);
      continue;
    }
    if (candidate.operationId !== operation.operationId) failures.push(`changed operationId for ${key}`);
    const candidateParams = new Map(candidate.parameters.map((item) => [`${item.in}:${item.name}`, item]));
    for (const parameter of operation.parameters) {
      const next = candidateParams.get(`${parameter.in}:${parameter.name}`);
      if (!next) failures.push(`removed parameter ${parameter.in}:${parameter.name} from ${key}`);
      else if (JSON.stringify(next.schema) !== JSON.stringify(parameter.schema)) failures.push(`changed parameter type ${parameter.in}:${parameter.name} in ${key}`);
      else if (!parameter.required && next.required) failures.push(`made parameter required ${parameter.in}:${parameter.name} in ${key}`);
    }
    const baselineParams = new Set(operation.parameters.map((item) => `${item.in}:${item.name}`));
    for (const parameter of candidate.parameters) {
      if (parameter.required && !baselineParams.has(`${parameter.in}:${parameter.name}`)) failures.push(`added required parameter ${parameter.in}:${parameter.name} to ${key}`);
    }
    if (operation.requestBody && !candidate.requestBody) failures.push(`removed request body from ${key}`);
    if (!operation.requestBody?.required && candidate.requestBody?.required) failures.push(`made request body required in ${key}`);
    if (operation.requestBody?.schema && candidate.requestBody?.schema) compareSchema(key, operation.requestBody.schema, candidate.requestBody.schema, `${key} request`);
    if (operation.response && candidate.response) compareSchema(key, operation.response, candidate.response, `${key} response`);
    else if (operation.response && !candidate.response) failures.push(`removed success response from ${key}`);
  }

  for (const [name, schema] of Object.entries(baseline.schemas)) {
    if (!current.schemas[name]) failures.push(`removed schema ${name}`);
    else compareSchema(name, schema, current.schemas[name]);
  }
  return failures;
}

export function checkOpenApiBreaking(root) {
  const specFile = path.join(root, "services/api-gateway/openapi/edugrade-api.openapi.json");
  const baselineFile = path.join(root, "contracts/openapi/edugrade-api.breaking-baseline.json");
  const baseline = JSON.parse(fs.readFileSync(baselineFile, "utf8"));
  const current = createBreakingBaseline(readOpenApi(specFile));
  return findBreakingChanges(baseline, current);
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
  const failures = checkOpenApiBreaking(root);
  if (failures.length) {
    console.error("OpenAPI breaking-change gate failed:\n" + failures.map((item) => `- ${item}`).join("\n"));
    process.exit(1);
  }
  console.log("OpenAPI breaking-change gate passed.");
}
