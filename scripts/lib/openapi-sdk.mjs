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

function objectSchemaToType(schema) {
  const required = new Set(schema.required ?? []);
  const properties = Object.entries(schema.properties ?? {}).map(([name, child]) => {
    return `${propertyKey(name)}${required.has(name) ? "" : "?"}: ${schemaToType(child)};`;
  });
  const objectType = properties.length ? `{ ${properties.join(" ")} }` : "Record<string, never>";
  if (schema.additionalProperties === true) {
    return properties.length ? `(${objectType} & Record<string, unknown>)` : "Record<string, unknown>";
  }
  if (schema.additionalProperties && typeof schema.additionalProperties === "object") {
    return `(${objectType} & Record<string, ${schemaToType(schema.additionalProperties)}>)`;
  }
  return objectType;
}

function primitiveSchemaToType(schema) {
  if (schema.type === "string") return schema.format === "binary" ? "Blob | ArrayBuffer | Uint8Array" : "string";
  if (schema.type === "integer" || schema.type === "number") return "number";
  if (schema.type === "boolean") return "boolean";
  if (schema.type === "null") return "null";
  return "unknown";
}

export function schemaToType(schema) {
  if (!schema || typeof schema !== "object") return "unknown";
  // $ref 直接输出引用名；其余节点先算基础类型，再拼接 allOf，最后追加 nullable。
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
    result = objectSchemaToType(schema);
  } else {
    result = primitiveSchemaToType(schema);
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

  const referencedSchemas = Object.keys(spec.components?.schemas ?? {}).filter((name) => {
    const escapedName = name.replace(/[.*+?^${}()|[\\]\\\\]/g, String.raw`\$&`);
    return new RegExp(String.raw`\b${escapedName}\b`).test(operationTypes);
  });

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
        if (!request.json) initParts.push(`headers: { "Content-Type": ${JSON.stringify(request.contentType)}, ...Object.fromEntries(Object.entries(args.headers ?? {}).map(([name, value]) => [name, String(value)])) }`);
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

function verifyArtifactPath(file, opened) {
  const current = fs.statSync(file, { bigint: true });
  // 这里只决定本次能否报告成功，之后不按路径重新读写；打开后被替换或删除的产物必须交由调用方重试。
  if (current.dev !== opened.dev || current.ino !== opened.ino) throw new Error(`Generated artifact path changed while open: ${file}`);
}

function readonlyArtifactUnchanged(file, content, originalError) {
  // 只读产物仅在内容和路径身份均未变化时通过；这个分支不能再按路径打开写入。
  const descriptor = fs.openSync(file, fs.constants.O_RDONLY | (fs.constants.O_NONBLOCK ?? 0));
  try {
    const opened = fs.fstatSync(descriptor, { bigint: true });
    if (!opened.isFile()) throw originalError;
    if (fs.readFileSync(descriptor, "utf8") !== content) return false;
    verifyArtifactPath(file, opened);
    return true;
  } finally { fs.closeSync(descriptor); }
}

function openExistingArtifact(file, content) {
  // undefined 表示路径缺失，可尝试排他创建；null 表示只读内容已校验，无需交出写入描述符。
  try {
    return { descriptor: fs.openSync(file, fs.constants.O_RDWR | (fs.constants.O_NONBLOCK ?? 0)), created: false };
  } catch (error) {
    if (error.code === "EACCES" || error.code === "EPERM") {
      if (readonlyArtifactUnchanged(file, content, error)) return null;
      throw error;
    }
    if (error.code === "ENOENT") return undefined;
    throw error;
  }
}

function openArtifactForUpdate(file, content) {
  // 已有文件用 r+ 保留权限及 Windows 兼容性；缺失时排他创建，最多尝试三轮。
  // 另一进程抢先创建时重新打开它，不能使用截断模式；null 沿用已完成只读校验的结果。
  for (let attempt = 0; attempt < 3; attempt += 1) {
    const existing = openExistingArtifact(file, content);
    if (existing !== undefined) return existing;
    try { return { descriptor: fs.openSync(file, "wx+"), created: true }; }
    catch (createError) {
      if (createError.code !== "EEXIST" || attempt >= 2) throw createError;
    }
  }
  throw new Error(`Generated artifact could not be opened: ${file}`);
}

export function writeIfChanged(file, content) {
  fs.mkdirSync(path.dirname(file), { recursive: true });
  const openedArtifact = openArtifactForUpdate(file, content);
  if (openedArtifact === null) return false;
  const { descriptor, created } = openedArtifact;
  try {
    // 链接按系统规则在打开时解析；内容比较和写入绑定同一文件，路径身份另在返回前核对。
    const opened = fs.fstatSync(descriptor, { bigint: true });
    if (!opened.isFile()) throw new Error(`Generated artifact is not a regular file: ${file}`);
    if (fs.readFileSync(descriptor, "utf8") === content) { verifyArtifactPath(file, opened); return created; }
    const buffer = Buffer.from(content, "utf8");
    // 读取已移动文件位置，必须从字节 0 写入；短写继续补齐，写入失败直接报错，不能返回生成成功。
    for (let offset = 0; offset < buffer.length;) {
      const written = fs.writeSync(descriptor, buffer, offset, buffer.length - offset, offset);
      if (written === 0) throw new Error(`Generated artifact write made no progress: ${file}`);
      offset += written;
    }
    fs.ftruncateSync(descriptor, buffer.length);
    verifyArtifactPath(file, opened);
    return true;
  } finally { fs.closeSync(descriptor); }
}

function banner() {
  return "// Generated from services/api-gateway/openapi/edugrade-api.openapi.json. DO NOT EDIT.\n\n";
}
