import { build } from "esbuild";
import { mkdtemp, rm } from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { pathToFileURL } from "node:url";

const [baseUrl, fixture] = process.argv.slice(2);
if (!baseUrl || !fixture) throw new Error("Usage: exam-command-client.mjs <test-server-url> <fixture.json>");
const directory = await mkdtemp(path.join(os.tmpdir(), "edugrade-command-client-"));
try {
  const outfile = path.join(directory, "client.mjs");
  // 打包真实客户端并将 API 地址绑定到测试服务，避免替身请求绕开生产请求构造。
  await build({
    entryPoints: ["tests/integration/exam-command-client.ts"], outfile,
    bundle: true, platform: "node", format: "esm", target: "node24",
    define: { "import.meta.env.VITE_API_BASE_URL": JSON.stringify(baseUrl) },
  });
  const { run } = await import(pathToFileURL(outfile).href);
  console.log(JSON.stringify(await run(fixture)));
} finally {
  await rm(directory, { recursive: true, force: true });
}
