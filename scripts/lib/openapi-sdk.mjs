import fs from "node:fs";
import path from "node:path";

const HTTP_METHODS = ["get", "post", "put", "patch", "delete"];

export function readOpenApi(file) {
  return JSON.parse(fs.readFileSync(file, "utf8"));
}

function refName(ref) {
  return ref.split("/").at(-1);
}

function propertyKey(value) {
  return JSON.stringify(value);
}

export function schemaToType(schema) {
  if (!schema || typeof schema !== "object") return "unknown";
  if (schema.$ref) return refName(schema.$ref);

  let result;
  if (Object.hasOwn(schema, "const")) {
    result = JSON.stringify(schema.const);
  } else if (Array.isArray(schema.enum)) {
    result = schema.enum.map((value) => JSON.stringify(value)).join(" | ") || "never";
  } else if (Array.isArray(schema.oneOf)) {
    result = schema.oneOf.map(schemaToType).join(" | ");
  } else if (Array.isArray(schema.anyOf)) {
    result = schema.anyOf.map(schemaToType).join(" | ");
  } else if (schema.type === "array") {
    result = `Array<${schemaToType(schema.items)}>`;
  } else if (schema.type === "object" || schema.properties || schema.additionalProperties) {
    const required = new Set(schema.required ?? []);
    const properties = Object.entries(schema.properties ?? {}).map(([name, child]) => {
      return `${propertyKey(name)}${required.has(name) ? "" : "?"}: ${schemaToType(child)};`;
    });
    const objectType = properties.length ? `{ ${properties.join(" ")} }` : "Record<string, never>";
    if (schema.additionalProperties === true) {
      result = properties.length ? `(${objectType} & Record<string, unknown>)` : "Record<string, unknown>";
    } else if (schema.additionalProperties && typeof schema.additionalProperties === "object") {
      result = `(${objectType} & Record<string, ${schemaToType(schema.additionalProperties)}>)`;
    } else {
      result = objectType;
    }
  } else if (schema.type === "string") {
    result = schema.format === "binary" ? "Blob | ArrayBuffer | Uint8Array" : "string";
  } else if (schema.type === "integer" || schema.type === "number") {
    result = "number";
  } else if (schema.type === "boolean") {
    result = "boolean";
  } else if (schema.type === "null") {
    result = "null";
  } else {
    result = "unknown";
  }

  if (Array.isArray(schema.allOf) && schema.allOf.length) {
    result = [result, ...schema.allOf.map((item) => schemaToType(item))].map((item) => `(${item})`).join(" & ");
  }

  return schema.nullable ? `${result} | null` : result;
}

function resolveParameter(spec, parameter) {
  if (!parameter?.$ref) return parameter;
  return spec.components?.parameters?.[refName(parameter.$ref)] ?? parameter;
}

function responseSchema(spec, operation) {
  for (const [status, responseOrRef] of Object.entries(operation.responses ?? {})) {
    if (!/^2\d\d$/.test(status)) continue;
    const response = responseOrRef?.$ref
      ? spec.components?.responses?.[refName(responseOrRef.$ref)]
      : responseOrRef;
    const schema = response?.content?.["application/json"]?.schema;
    if (schema) return schemaToType(schema);
    if (status === "204") return "void";
  }
  return "unknown";
}

// 生成器只处理这里显式支持的 JSON 和二进制正文；新增媒体类型需要同步扩展生成逻辑。
function requestBodyDescriptor(operation) {
  const content = operation.requestBody?.content;
  const json = content?.["application/json"]?.schema;
  if (json) return { schema: json, contentType: "application/json", json: true };
  const octetStream = content?.["application/octet-stream"]?.schema;
  if (octetStream) return { schema: octetStream, contentType: "application/octet-stream", json: false };
  return undefined;
}

function operationEntries(spec) {
  const entries = [];
  for (const [pathTemplate, pathItem] of Object.entries(spec.paths ?? {})) {
    for (const method of HTTP_METHODS) {
      const operation = pathItem[method];
      if (!operation) continue;
      if (!operation.operationId) {
        throw new Error(`${method.toUpperCase()} ${pathTemplate} is missing operationId`);
      }
      const parameters = [...(pathItem.parameters ?? []), ...(operation.parameters ?? [])].map((item) =>
        resolveParameter(spec, item)
      );
      entries.push({ method, pathTemplate, operation, parameters });
    }
  }
  return entries;
}

function parameterObject(parameters, location) {
  const selected = parameters.filter((parameter) => parameter?.in === location);
  if (!selected.length) return undefined;
  return `{ ${selected
    .map((parameter) => {
      const optional = parameter.required ? "" : "?";
      return `${propertyKey(parameter.name)}${optional}: ${schemaToType(parameter.schema)};`;
    })
    .join(" ")} }`;
}

export function generateTypes(spec) {
  const schemas = spec.components?.schemas ?? {};
  const body = Object.entries(schemas)
    .map(([name, schema]) => `export type ${name} = ${schemaToType(schema)};`)
    .join("\n\n");
  const componentMap = Object.keys(schemas)
    .map((name) => `    ${propertyKey(name)}: ${name};`)
    .join("\n");
  return `${banner()}${body}\n\nexport interface components {\n  schemas: {\n${componentMap}\n  };\n}\n`;
}

export function generateClient(spec) {
  const entries = operationEntries(spec);
  const operationTypes = entries
    .map(({ operation, parameters }) => {
      const fields = [];
      const pathType = parameterObject(parameters, "path");
      const queryType = parameterObject(parameters, "query");
      const headerType = parameterObject(parameters, "header");
      if (pathType) fields.push(`path: ${pathType};`);
      if (queryType) {
        const hasRequiredQuery = parameters.some((parameter) => parameter?.in === "query" && parameter.required);
        fields.push(`query${hasRequiredQuery ? "" : "?"}: ${queryType};`);
      }
      if (headerType) {
        const hasRequiredHeader = parameters.some((parameter) => parameter?.in === "header" && parameter.required);
        fields.push(`headers${hasRequiredHeader ? "" : "?"}: ${headerType};`);
      }
      const request = requestBodyDescriptor(operation);
      if (request) fields.push(`body${operation.requestBody.required ? "" : "?"}: ${schemaToType(request.schema)};`);
      fields.push("signal?: AbortSignal;");
      return `  ${propertyKey(operation.operationId)}: { args: { ${fields.join(" ")} }; response: ${responseSchema(spec, operation)}; };`;
    })
    .join("\n");

  const referencedSchemas = Object.keys(spec.components?.schemas ?? {}).filter((name) =>
    new RegExp(`\\b${name.replace(/[.*+?^${}()|[\\]\\\\]/g, "\\$&")}\\b`).test(operationTypes)
  );

  const methods = entries
    .map(({ method, pathTemplate, operation, parameters }) => {
      const operationId = operation.operationId;
      // 仅完全没有必填路径、查询、请求头或正文时，生成的方法才允许省略整个参数对象。
      const hasRequiredArgs =
        parameters.some((parameter) => parameter?.in === "path" || parameter?.required) ||
        Boolean(operation.requestBody?.required);
      const args = `args: operations[${propertyKey(operationId)}]["args"]${hasRequiredArgs ? "" : " = {}"}`;
      const pathExpression = parameters.some((parameter) => parameter?.in === "path")
        ? `fillPath(${JSON.stringify(pathTemplate)}, args.path)`
        : JSON.stringify(pathTemplate);
      const queryExpression = parameters.some((parameter) => parameter?.in === "query")
        ? `appendQuery(${pathExpression}, args.query)`
        : pathExpression;
      const request = requestBodyDescriptor(operation);
      const initParts = [`method: ${JSON.stringify(method.toUpperCase())}`, "signal: args.signal"];
      if (request) {
        initParts.push(request.json ? "body: args.body === undefined ? undefined : JSON.stringify(args.body)" : "body: args.body as BodyInit");
        if (!request.json) initParts.push(`headers: { \"Content-Type\": ${JSON.stringify(request.contentType)}, ...Object.fromEntries(Object.entries(args.headers ?? {}).map(([name, value]) => [name, String(value)])) }`);
        else if (parameters.some((parameter) => parameter?.in === "header")) {
          initParts.push("headers: Object.fromEntries(Object.entries(args.headers ?? {}).map(([name, value]) => [name, String(value)]))");
        }
      } else if (parameters.some((parameter) => parameter?.in === "header")) {
        initParts.push("headers: Object.fromEntries(Object.entries(args.headers ?? {}).map(([name, value]) => [name, String(value)]))");
      }
      return `  ${operationId}(${args}): Promise<operations[${propertyKey(operationId)}]["response"]> {\n    const requestPath = ${queryExpression};\n    return this.transport.request(requestPath, { ${initParts.join(", ")} });\n  }`;
    })
    .join("\n\n");

  return `${banner()}import type { ApiTransport } from "../runtime";\nimport { appendQuery, fillPath } from "../runtime";\nimport type {\n${referencedSchemas
    .map((name) => `  ${name}`)
    .join(",\n")}\n} from "./types";\n\nexport interface operations {\n${operationTypes}\n}\n\nexport class EduGradeApi {\n  constructor(private readonly transport: ApiTransport) {}\n\n${methods}\n}\n`;
}

// 基线仅保留白名单字段，描述文字不会进入兼容性比较；它不是原始契约的完整副本。
function normalizeSchema(schema) {
  if (!schema || typeof schema !== "object") return schema ?? null;
  if (Array.isArray(schema)) return schema.map(normalizeSchema);
  const result = {};
  for (const key of ["$ref", "type", "format", "nullable", "required", "enum", "properties", "items", "oneOf", "anyOf", "allOf", "additionalProperties"]) {
    if (Object.hasOwn(schema, key)) result[key] = normalizeSchema(schema[key]);
  }
  return result;
}

export function createBreakingBaseline(spec) {
  const operations = {};
  for (const { method, pathTemplate, operation, parameters } of operationEntries(spec)) {
    operations[`${method.toUpperCase()} ${pathTemplate}`] = {
      operationId: operation.operationId,
      parameters: parameters.map((parameter) => ({
        in: parameter.in,
        name: parameter.name,
        required: Boolean(parameter.required),
        schema: normalizeSchema(parameter.schema)
      })),
      requestBody: operation.requestBody
        ? {
            required: Boolean(operation.requestBody.required),
            schema: normalizeSchema(requestBodyDescriptor(operation)?.schema)
          }
        : null,
      response: normalizeSchema(
        Object.entries(operation.responses ?? {})
          .filter(([status]) => /^2\d\d$/.test(status))
          .map(([, response]) => response?.content?.["application/json"]?.schema)
          .find(Boolean)
      )
    };
  }
  const schemas = Object.fromEntries(
    Object.entries(spec.components?.schemas ?? {}).map(([name, schema]) => [name, normalizeSchema(schema)])
  );
  return { openapi: spec.openapi, operations, schemas };
}

export function stableJson(value) {
  return `${JSON.stringify(value, null, 2)}\n`;
}

export function writeIfChanged(file, content) {
  fs.mkdirSync(path.dirname(file), { recursive: true });
  if (fs.existsSync(file) && fs.readFileSync(file, "utf8") === content) return false;
  fs.writeFileSync(file, content, "utf8");
  return true;
}

function banner() {
  return "// Generated from services/api-gateway/openapi/edugrade-api.openapi.json. DO NOT EDIT.\n\n";
}
