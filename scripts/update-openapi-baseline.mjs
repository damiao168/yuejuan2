import path from "node:path";
import { fileURLToPath } from "node:url";
import { createBreakingBaseline, readOpenApi, stableJson, writeIfChanged } from "./lib/openapi-sdk.mjs";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const spec = readOpenApi(path.join(root, "services/api-gateway/openapi/edugrade-api.openapi.json"));
const baselineFile = path.join(root, "contracts/openapi/edugrade-api.breaking-baseline.json");
// 此入口会接受当前契约为新基线，只能在完成兼容性评审后主动运行。
writeIfChanged(baselineFile, stableJson(createBreakingBaseline(spec)));
console.log(`Updated ${path.relative(root, baselineFile)}.`);
