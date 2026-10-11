import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import fs, { openAsBlob } from "node:fs";
import os from "node:os";
import path from "node:path";
import { spawnSync } from "node:child_process";
import { pathToFileURL, fileURLToPath } from "node:url";
import { build } from "esbuild";

const MiB = 1024 * 1024;

if (process.argv[2] === "--worker") {
  const [, , , bundle, input, abortAfter] = process.argv;
  const { sha256ForFile } = await import(pathToFileURL(bundle).href);
  await sha256ForFile(new Blob([new Uint8Array(MiB)]));
  const blob = await openAsBlob(input);
  const baseline = process.memoryUsage();
  let peakRSS = baseline.rss;
  let peakArrayBuffers = baseline.arrayBuffers;
  let bytesRead = 0;
  let largestRead = 0;
  const sample = () => {
    const memory = process.memoryUsage();
    peakRSS = Math.max(peakRSS, memory.rss);
    peakArrayBuffers = Math.max(peakArrayBuffers, memory.arrayBuffers);
  };
  const observedFile = {
    size: blob.size,
    slice(start, end) {
      sample();
      if (Number(abortAfter) && bytesRead >= Number(abortAfter)) throw new Error("injected process interruption");
      const slice = blob.slice(start, end);
      return { async arrayBuffer() {
        const result = await slice.arrayBuffer();
        bytesRead += result.byteLength;
        largestRead = Math.max(largestRead, result.byteLength);
        sample();
        return result;
      } };
    },
    arrayBuffer() { throw new Error("whole file read forbidden"); },
  };
  let digest;
  let interrupted = false;
  try { digest = await sha256ForFile(observedFile); }
  catch (error) {
    if (!Number(abortAfter) || error.message !== "injected process interruption") throw error;
    interrupted = true;
  }
  sample();
  console.log(JSON.stringify({ size: blob.size, bytesRead, largestRead, digest, interrupted,
    baselineRSS: baseline.rss, peakRSS, rssGrowth: peakRSS - baseline.rss,
    arrayBufferGrowth: peakArrayBuffers - baseline.arrayBuffers,
    processMaxRSS: process.resourceUsage().maxRSS * 1024 }));
} else {
  const directory = fs.mkdtempSync(path.join(os.tmpdir(), "edugrade-digest-memory-"));
  try {
    const bundle = path.join(directory, "digest.mjs");
    await build({ entryPoints: ["apps/desktop-client/src/api/captureUploads.ts"], outfile: bundle,
      bundle: true, platform: "node", format: "esm", define: { "import.meta.env": "{}" } });
    const fixture = path.join(directory, "large-original.bin");
    const chunk = Buffer.alloc(MiB);
    for (let index = 0; index < chunk.length; index++) chunk[index] = (index * 31 + 17) % 251;
    const results = [];
    const run = (abortAfter = 0) => {
      const child = spawnSync(process.execPath, [fileURLToPath(import.meta.url), "--worker", bundle, fixture, String(abortAfter)], { encoding: "utf8", timeout: 180_000 });
      assert.equal(child.status, 0, child.stderr || String(child.error));
      return JSON.parse(child.stdout.trim());
    };
    for (const sizeMiB of [32, 256]) {
      const expected = createHash("sha256");
      const descriptor = fs.openSync(fixture, "w");
      try {
        for (let index = 0; index < sizeMiB; index++) { fs.writeSync(descriptor, chunk); expected.update(chunk); }
      } finally { fs.closeSync(descriptor); }
      if (sizeMiB === 256) {
        const interrupted = run(8 * MiB);
        assert.equal(interrupted.interrupted, true);
        assert.equal(interrupted.digest, undefined);
        assert.ok(interrupted.bytesRead < interrupted.size);
        results.push({ phase: "interrupted", ...interrupted });
      }
      const measured = run();
      assert.equal(measured.digest, expected.digest("hex"));
      assert.equal(measured.bytesRead, measured.size);
      assert.ok(measured.largestRead <= 4 * MiB);
      assert.ok(measured.rssGrowth < 128 * MiB, `RSS grew ${measured.rssGrowth} bytes`);
      // 计入等待 GC 的临时 Blob 副本，不只统计当前活动分块。
      // 固定上限应低于大测试文件的一半，不能随文件大小
      // 提高，也不能强制 GC 来制造偏低的测量值。
      assert.ok(measured.arrayBufferGrowth < 128 * MiB, `array buffers grew ${measured.arrayBufferGrowth} bytes`);
      results.push({ phase: sizeMiB === 256 ? "fresh-process-recovery" : "baseline-size", ...measured });
    }
    console.log(JSON.stringify({ status: "passed", runtime: process.version, platform: process.platform,
      scope: "production sha256ForFile, disk-backed Blob, separate processes; rehash after interruption; not WebView total memory", results }, null, 2));
  } finally {
    fs.rmSync(directory, { recursive: true, force: true });
  }
}
