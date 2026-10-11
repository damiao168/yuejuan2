import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { createBreakingBaseline, readOpenApi } from "./lib/openapi-sdk.mjs";

function compareRequiredFields(before, after, location, failures) {
  const beforeRequired = new Set(before.required ?? []);
  const afterRequired = new Set(after.required ?? []);
  for (const required of beforeRequired) {
    if (!afterRequired.has(required)) failures.push(`made required field optional ${location}.${required}`);
  }
  for (const required of afterRequired) {
    if (!beforeRequired.has(required)) failures.push(`made optional field required ${location}.${required}`);
  }
}

function compareSchemaProperties(before, after, location, failures) {
  for (const [property, schema] of Object.entries(before.properties ?? {})) {
    if (!Object.hasOwn(after.properties ?? {}, property)) {
      failures.push(`removed property ${location}.${property}`);
    } else {
      compareSchema(schema, after.properties[property], `${location}.${property}`, failures);
    }
  }
}

function compareSchemaMetadata(before, after, location, failures) {
  if (before.type && before.type !== after.type) failures.push(`changed type for ${location}`);
  if (before.$ref && before.$ref !== after.$ref) failures.push(`changed ref for ${location}`);
  for (const value of Array.isArray(before.enum) ? before.enum : []) {
    if (!after.enum?.includes(value)) failures.push(`removed enum value ${JSON.stringify(value)} from ${location}`);
  }
}

function compareSchema(before, after, location, failures) {
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
  compareSchemaMetadata(before, after, location, failures);
  compareRequiredFields(before, after, location, failures);
  compareSchemaProperties(before, after, location, failures);
  if (before.items) compareSchema(before.items, after.items, `${location}[]`, failures);
}

function parameterIdentity(parameter) {
  return `${parameter.in}:${parameter.name}`;
}

function compareParameters(operation, candidate, key, failures) {
  const candidateParams = new Map(candidate.parameters.map((item) => [parameterIdentity(item), item]));
  for (const parameter of operation.parameters) {
    const identity = parameterIdentity(parameter);
    const next = candidateParams.get(identity);
    if (!next) failures.push(`removed parameter ${identity} from ${key}`);
    else if (JSON.stringify(next.schema) !== JSON.stringify(parameter.schema)) failures.push(`changed parameter type ${identity} in ${key}`);
    else if (!parameter.required && next.required) failures.push(`made parameter required ${identity} in ${key}`);
  }
  const baselineParams = new Set(operation.parameters.map(parameterIdentity));
  for (const parameter of candidate.parameters) {
    const identity = parameterIdentity(parameter);
    if (parameter.required && !baselineParams.has(identity)) failures.push(`added required parameter ${identity} to ${key}`);
  }
}

function compareOperationPayloads(operation, candidate, key, failures) {
  if (operation.requestBody && !candidate.requestBody) failures.push(`removed request body from ${key}`);
  if (!operation.requestBody?.required && candidate.requestBody?.required) failures.push(`made request body required in ${key}`);
  if (operation.requestBody?.schema && candidate.requestBody?.schema) {
    compareSchema(operation.requestBody.schema, candidate.requestBody.schema, `${key} request`, failures);
  }
  if (operation.response && candidate.response) {
    compareSchema(operation.response, candidate.response, `${key} response`, failures);
  } else if (operation.response && !candidate.response) {
    failures.push(`removed success response from ${key}`);
  }
}

// 此门禁比较已提取的契约子集，未覆盖全部 OpenAPI 语义；通过不能代替接口兼容性评审。
export function findBreakingChanges(baseline, current) {
  const failures = [];
  for (const [key, operation] of Object.entries(baseline.operations)) {
    const candidate = current.operations[key];
    if (!candidate) {
      failures.push(`removed operation ${key}`);
      continue;
    }
    if (candidate.operationId !== operation.operationId) failures.push(`changed operationId for ${key}`);
    compareParameters(operation, candidate, key, failures);
    compareOperationPayloads(operation, candidate, key, failures);
  }
  for (const [name, schema] of Object.entries(baseline.schemas)) {
    if (!current.schemas[name]) failures.push(`removed schema ${name}`);
    else compareSchema(schema, current.schemas[name], name, failures);
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
