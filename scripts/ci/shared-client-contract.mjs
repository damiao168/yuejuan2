import { build } from "esbuild";
import { mkdtemp, rm } from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { pathToFileURL } from "node:url";

const baseUrl = process.argv[2];
if (!baseUrl) throw new Error("Usage: shared-client-contract.mjs <handler-fixture-server-url>");
const directory = await mkdtemp(path.join(os.tmpdir(), "edugrade-shared-client-"));
try {
  const outfile = path.join(directory, "client.mjs");
  // 在 Node 中调用生产 SDK/runtime；此测试不覆盖浏览器 cookie、CORS 或页面交互。
  await build({ entryPoints: ["tests/integration/shared-client-contract.ts"], outfile,
    bundle: true, platform: "node", format: "esm", define: { "import.meta.env": "{}" } });
  const { run } = await import(pathToFileURL(outfile).href);
  console.log(JSON.stringify(await run(baseUrl)));
} finally {
  await rm(directory, { recursive: true, force: true });
}
