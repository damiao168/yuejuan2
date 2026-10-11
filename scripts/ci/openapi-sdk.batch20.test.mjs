import assert from "node:assert/strict";
import test from "node:test";

import { generateClient, generateTypes, schemaToType } from "../lib/openapi-sdk.mjs";

test("schema references and literal schemas retain precedence over sibling type modifiers", () => {
  assert.equal(schemaToType({
    $ref: "#/components/schemas/Referenced",
    const: "ignored",
    type: "object",
    allOf: [{ type: "boolean" }],
    nullable: true,
  }), "Referenced");
  assert.equal(schemaToType({ const: null, enum: ["ignored"], type: "string", nullable: true }), "null | null");
  assert.equal(schemaToType({ enum: [], oneOf: [{ type: "string" }], type: "string" }), "never");
  assert.equal(schemaToType({ oneOf: [], anyOf: [{ type: "string" }], type: "boolean" }), "");
  assert.equal(schemaToType({ anyOf: [{ type: "integer" }, { type: "boolean" }], type: "string" }), "number | boolean");
});

test("object schemas preserve required keys, property order and additional-property composition", () => {
  const schema = {
    type: "object",
    required: ["id"],
    properties: { id: { type: "integer" }, "quoted\"key": { type: "string", format: "binary" } },
    additionalProperties: { type: "boolean", nullable: true },
    allOf: [{ $ref: "#/components/schemas/Extra" }, { type: "string" }],
    nullable: true,
  };
  assert.equal(schemaToType(schema), '(({ "id": number; "quoted\\\"key"?: Blob | ArrayBuffer | Uint8Array; } & Record<string, boolean | null>)) & (Extra) & (string) | null');
  assert.equal(schemaToType({ type: "object" }), "Record<string, never>");
  assert.equal(schemaToType({ additionalProperties: true }), "Record<string, unknown>");
  assert.equal(schemaToType({ additionalProperties: { type: "integer" } }), "(Record<string, never> & Record<string, number>)");
  assert.equal(schemaToType({ properties: { id: { type: "string" } }, additionalProperties: true }), '({ "id"?: string; } & Record<string, unknown>)');
});

test("schema fallback boundaries retain primitive, inferred-object and array output", () => {
  const cases = [
    [null, "unknown"],
    ["string", "unknown"],
    [{}, "unknown"],
    [{ additionalProperties: false }, "unknown"],
    [{ properties: {} }, "Record<string, never>"],
    [{ type: "string", format: "binary", nullable: true }, "Blob | ArrayBuffer | Uint8Array | null"],
    [{ type: "string" }, "string"],
    [{ type: "integer" }, "number"],
    [{ type: "number" }, "number"],
    [{ type: "boolean" }, "boolean"],
    [{ type: "null" }, "null"],
    [{ type: "other", nullable: true }, "unknown | null"],
    [{ type: "array" }, "Array<unknown>"],
    [{ allOf: [{ type: "string" }], nullable: true }, "(unknown) & (string) | null"],
  ];
  for (const [schema, expected] of cases) assert.equal(schemaToType(schema), expected, JSON.stringify(schema));
});

test("type declarations preserve exact composition text and schema declaration order", () => {
  const spec = { components: { schemas: {
    Result: { type: "object", required: ["id"], properties: { id: { type: "string" } }, nullable: true },
    Extra: { type: "boolean" },
  } } };
  assert.equal(generateTypes(spec), [
    "// Generated from services/api-gateway/openapi/edugrade-api.openapi.json. DO NOT EDIT.",
    "",
    'export type Result = { "id": string; } | null;',
    "",
    "export type Extra = boolean;",
    "",
    "export interface components {",
    "  schemas: {",
    '    "Result": Result;',
    '    "Extra": Extra;',
    "  };",
    "}",
    "",
  ].join("\n"));
});

test("client generation retains schema-name boundaries and binary, JSON and header text", () => {
  const spec = {
    components: { schemas: { Payload: { type: "string" }, Payload2: { type: "object" }, Unused: { type: "boolean" } } },
    paths: {
      "/uploads/{id}": { post: {
        operationId: "upload",
        parameters: [{ in: "path", name: "id", required: true, schema: { type: "string" } }, { in: "header", name: "X-Trace", schema: { type: "integer" } }],
        requestBody: { content: { "application/octet-stream": { schema: { type: "string", format: "binary" } } } },
        responses: { 200: { content: { "application/json": { schema: { $ref: "#/components/schemas/Payload2" } } } } },
      } },
      "/records": { post: {
        operationId: "create",
        requestBody: { required: true, content: { "application/json": { schema: { type: "string" } } } },
        responses: { 204: {} },
      } },
      "/records/status": { get: { operationId: "status", responses: { 204: {} } } },
    },
  };
  const client = generateClient(spec);
  assert.ok(client.includes('import type {\n  Payload2\n} from "./types";'));
  assert.ok(client.includes('"upload": { args: { path: { "id": string; }; headers?: { "X-Trace"?: number; }; body?: Blob | ArrayBuffer | Uint8Array; signal?: AbortSignal; }; response: Payload2; };'));
  assert.ok(client.includes('const requestPath = fillPath("/uploads/{id}", args.path);'));
  assert.ok(client.includes('body: args.body as BodyInit, headers: { "Content-Type": "application/octet-stream", ...Object.fromEntries(Object.entries(args.headers ?? {}).map(([name, value]) => [name, String(value)])) }'));
  assert.ok(client.includes('create(args: operations["create"]["args"]): Promise<operations["create"]["response"]>'));
  assert.ok(client.includes('body: args.body === undefined ? undefined : JSON.stringify(args.body)'));
  assert.ok(client.includes('status(args: operations["status"]["args"] = {}): Promise<operations["status"]["response"]>'));
});
