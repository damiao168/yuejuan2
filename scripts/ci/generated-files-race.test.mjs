import assert from "node:assert/strict";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import test from "node:test";
import { snapshotGenerated } from "./check-generated-contracts.mjs";
import { writeIfChanged } from "../lib/openapi-sdk.mjs";
import { writeRouteCoverage } from "../update-api-route-coverage.mjs";

function temporaryRoot(t) {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), "edugrade-generated-race-"));
  t.after(() => fs.rmSync(root, { recursive: true, force: true }));
  return root;
}

function coverageFixture(t) {
  const root = temporaryRoot(t);
  const gateway = path.join(root, "services/api-gateway");
  fs.mkdirSync(path.join(gateway, "internal"), { recursive: true });
  fs.mkdirSync(path.join(gateway, "openapi"));
  fs.mkdirSync(path.join(root, "contracts/openapi"), { recursive: true });
  fs.writeFileSync(path.join(gateway, "internal/routes.go"), 'package gateway\nfunc register() { mux.Handle("GET /contract", handler) }\n');
  fs.writeFileSync(path.join(gateway, "openapi/edugrade-api.openapi.json"), JSON.stringify({ paths: { "/contract": { get: { operationId: "readContract" } } } }));
  fs.writeFileSync(path.join(root, "contracts/openapi/registered-route-exceptions.json"), JSON.stringify({ policies: [] }));
  return { root, output: path.join(gateway, "openapi/route-coverage.json") };
}

// 在打开完成后真实替换路径，验证校验、读取和写入仍使用原文件，而不是只模拟错误码。
function replaceAfterOpen(t, file, replacement) {
  const open = fs.openSync;
  const moved = `${file}.opened`;
  let descriptor;
  t.mock.method(fs, "openSync", (...args) => {
    const result = open(...args);
    if (args[0] === file && descriptor === undefined) {
      descriptor = result;
      fs.renameSync(file, moved);
      fs.writeFileSync(file, replacement);
    }
    return result;
  });
  return { moved, descriptor: () => descriptor };
}

test("snapshot reads the opened file after path replacement and includes nested and missing outputs", (t) => {
  const root = temporaryRoot(t);
  fs.mkdirSync(path.join(root, "generated/nested"), { recursive: true });
  const file = path.join(root, "generated/a.txt");
  fs.writeFileSync(file, "opened bytes");
  fs.writeFileSync(path.join(root, "generated/nested/b.txt"), "nested bytes");
  const replacement = replaceAfterOpen(t, file, "replacement bytes");
  const snapshot = snapshotGenerated(root, ["generated", "absent.txt"]);
  assert.equal(snapshot.get("generated/a.txt").toString(), "opened bytes");
  assert.equal(snapshot.get("generated/nested/b.txt").toString(), "nested bytes");
  assert.equal(snapshot.size, 2);
  assert.throws(() => fs.fstatSync(replacement.descriptor()), { code: "EBADF" });
  assert.equal(fs.readFileSync(file, "utf8"), "replacement bytes");
});

test("snapshot closes files on read failure and propagates directory permission errors", (t) => {
  const root = temporaryRoot(t);
  const file = path.join(root, "artifact");
  fs.writeFileSync(file, "bytes");
  const read = fs.readFileSync;
  let descriptor;
  t.mock.method(fs, "readFileSync", (...args) => {
    if (typeof args[0] === "number") { descriptor = args[0]; throw new Error("read failed"); }
    return read(...args);
  });
  assert.throws(() => snapshotGenerated(root, ["artifact"]), /read failed/);
  assert.throws(() => fs.fstatSync(descriptor), { code: "EBADF" });
  t.mock.method(fs, "opendirSync", () => { throw Object.assign(new Error("denied"), { code: "EACCES" }); });
  assert.throws(() => snapshotGenerated(root, ["artifact"]), { code: "EACCES" });
});

test("generated writes preserve unchanged metadata and permissions and truncate UTF-8 output correctly", (t) => {
  const root = temporaryRoot(t);
  const file = path.join(root, "nested/artifact");
  assert.equal(writeIfChanged(file, "long previous content"), true);
  const before = fs.statSync(file);
  assert.equal(writeIfChanged(file, "long previous content"), false);
  assert.equal(fs.statSync(file).mtimeMs, before.mtimeMs);
  assert.equal(writeIfChanged(file, "中文"), true);
  assert.equal(fs.readFileSync(file, "utf8"), "中文");
  assert.equal(fs.statSync(file).mode, before.mode);
  assert.equal(writeIfChanged(file, ""), true);
  assert.equal(fs.statSync(file).size, 0);
  assert.equal(writeIfChanged(file, ""), false);
  assert.equal(writeIfChanged(path.join(root, "empty"), ""), true);
});

test("generated writes preserve the replaced path and reject success for a detached artifact", (t) => {
  const root = temporaryRoot(t);
  const file = path.join(root, "artifact");
  fs.writeFileSync(file, "old original");
  const replacement = replaceAfterOpen(t, file, "leave replacement intact");
  assert.throws(() => writeIfChanged(file, "new"), /Generated artifact path changed while open/);
  assert.throws(() => fs.fstatSync(replacement.descriptor()), { code: "EBADF" });
  assert.equal(fs.readFileSync(replacement.moved, "utf8"), "new");
  assert.equal(fs.readFileSync(file, "utf8"), "leave replacement intact");
});

test("exclusive creation retries an intervening creator without truncating unchanged output", (t) => {
  const root = temporaryRoot(t);
  const file = path.join(root, "artifact");
  const open = fs.openSync;
  let createdByOther = false;
  t.mock.method(fs, "openSync", (...args) => {
    if (args[0] === file && args[1] === "wx+" && !createdByOther) {
      createdByOther = true;
      fs.writeFileSync(file, "other generator bytes");
    }
    return open(...args);
  });
  assert.equal(writeIfChanged(file, "other generator bytes"), false);
  assert.equal(createdByOther, true);
  assert.equal(fs.readFileSync(file, "utf8"), "other generator bytes");
});

test("generated writes complete short writes and close their descriptor on failure", (t) => {
  const root = temporaryRoot(t);
  const file = path.join(root, "artifact");
  fs.writeFileSync(file, "old bytes");
  const write = fs.writeSync;
  t.mock.method(fs, "writeSync", (descriptor, buffer, offset, length, position) => write(descriptor, buffer, offset, Math.min(length, 2), position));
  assert.equal(writeIfChanged(file, "中文 bytes"), true);
  assert.equal(fs.readFileSync(file, "utf8"), "中文 bytes");
  let descriptor;
  t.mock.method(fs, "writeSync", (opened) => { descriptor = opened; throw new Error("disk full"); });
  assert.throws(() => writeIfChanged(file, "replacement"), /disk full/);
  assert.throws(() => fs.fstatSync(descriptor), { code: "EBADF" });
});

test("write errors and repeated creation contention do not report generation success", (t) => {
  const root = temporaryRoot(t);
  const file = path.join(root, "artifact");
  fs.writeFileSync(file, "bytes");
  assert.throws(() => writeIfChanged(root, "content"));
  const open = fs.openSync;
  t.mock.method(fs, "openSync", (...args) => {
    if (args[0] === file) throw Object.assign(new Error("denied"), { code: "EACCES" });
    return open(...args);
  });
  assert.throws(() => writeIfChanged(file, "new"), { code: "EACCES" });
  t.mock.method(fs, "openSync", (...args) => {
    if (args[0] === file) throw Object.assign(new Error("contended"), { code: args[1] === "wx+" ? "EEXIST" : "ENOENT" });
    return open(...args);
  });
  assert.throws(() => writeIfChanged(file, "new"), { code: "EEXIST" });
});

test("unchanged read-only artifacts remain valid but changed output requires write permission", (t) => {
  const root = temporaryRoot(t);
  const file = path.join(root, "artifact");
  fs.writeFileSync(file, "read-only bytes");
  const open = fs.openSync;
  t.mock.method(fs, "openSync", (...args) => {
    if (args[0] === file && args[1] === (fs.constants.O_RDWR | (fs.constants.O_NONBLOCK ?? 0))) {
      throw Object.assign(new Error("read-only file"), { code: "EACCES" });
    }
    return open(...args);
  });
  assert.equal(writeIfChanged(file, "read-only bytes"), false);
  assert.throws(() => writeIfChanged(file, "changed"), { code: "EACCES" });
  assert.equal(fs.readFileSync(file, "utf8"), "read-only bytes");
});

test("route coverage creates and updates outputs and preserves unchanged files", (t) => {
  const { root, output } = coverageFixture(t);
  const document = writeRouteCoverage(root);
  assert.deepEqual(JSON.parse(fs.readFileSync(output, "utf8")), document);
  const before = fs.statSync(output).mtimeMs;
  assert.deepEqual(writeRouteCoverage(root), document);
  assert.equal(fs.statSync(output).mtimeMs, before);
  fs.writeFileSync(output, "outdated bytes and tail".repeat(100));
  assert.deepEqual(writeRouteCoverage(root), document);
  assert.equal(fs.readFileSync(output, "utf8"), `${JSON.stringify(document, null, 2)}\n`);
});

test("route coverage rejects replaced output and propagates write failures", (t) => {
  const { root, output } = coverageFixture(t);
  fs.writeFileSync(output, "outdated");
  const replacement = replaceAfterOpen(t, output, "unrelated replacement");
  assert.throws(() => writeRouteCoverage(root), /Generated artifact path changed while open/);
  assert.equal(JSON.parse(fs.readFileSync(replacement.moved, "utf8")).counts.openapi, 1);
  assert.equal(fs.readFileSync(output, "utf8"), "unrelated replacement");
  t.mock.method(fs, "writeSync", () => { throw new Error("coverage write failed"); });
  assert.throws(() => writeRouteCoverage(root), /coverage write failed/);
});
