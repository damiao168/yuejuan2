import { test } from "node:test";
import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { mkdtempSync, writeFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, resolve, dirname } from "node:path";

test("处理能力仅首次询问，复用已保存选择，显式单项开关保持原范围", () => {
  const dir = mkdtempSync(join(tmpdir(), "edugrade-processing-mode-"));
  const helper = resolve("scripts/local-processing-mode.ps1");
  const runner = join(dir, "fixture.ps1");
  writeFileSync(runner, `param([string]$Helper,[string]$Requested,[string]$Saved,[string]$Explicit,[string]$Choice)\n. $Helper\nResolve-LocalProcessingMode -Requested $Requested -Saved $Saved -ExplicitWorkers ($Explicit -eq 'yes') -Choose { if ($Choice -eq 'must-not-prompt') { throw 'unexpected prompt' }; $Choice }\n`);
  const read = (requested, saved, explicit, choice) => execFileSync("powershell.exe", ["-NoProfile", "-ExecutionPolicy", "Bypass", "-File", runner, "-Helper", helper, "-Requested", requested, "-Saved", saved, "-Explicit", explicit ? "yes" : "no", "-Choice", choice], { encoding: "utf8" }).trim();
  try {
    assert.equal(read("Auto", "", false, "1"), "Enabled");
    assert.equal(read("Auto", "", false, "2"), "Disabled");
    assert.equal(read("Auto", "Enabled", false, "must-not-prompt"), "Enabled");
    assert.equal(read("Auto", "Disabled", false, "must-not-prompt"), "Disabled");
    assert.equal(read("Auto", "Enabled", true, "must-not-prompt"), "Explicit");
    assert.equal(read("Enabled", "Disabled", false, "must-not-prompt"), "Enabled");
    assert.equal(read("Disabled", "Enabled", false, "must-not-prompt"), "Disabled");
    assert.throws(() => read("Disabled", "", true, "must-not-prompt"));
  } finally {
    assert.equal(dirname(resolve(dir)), resolve(tmpdir()));
    assert.ok(dir.includes("edugrade-processing-mode-"));
    rmSync(dir, { recursive: true });
  }
});

test("切换仅管理模式停止上次处理服务，失败不得显示禁用已生效", () => {
  const dir = mkdtempSync(join(tmpdir(), "edugrade-processing-stop-"));
  const runner = join(dir, "fixture.ps1");
  writeFileSync(runner, `param([string]$Helper,[string]$Mode,[string]$Failure)\n. $Helper\n$global:recorded=@()\nfunction global:docker { $global:recorded += ,@($args); $global:LASTEXITCODE=if($Failure -eq 'yes'){1}else{0} }\nStop-DisabledLocalProcessingWorkers -Mode $Mode -EnvFile 'fixture.env' -ComposeFile 'fixture.yml'\nConvertTo-Json -InputObject @($global:recorded) -Compress\n`);
  const run = (mode, failure = "no") => execFileSync("powershell.exe", ["-NoProfile", "-ExecutionPolicy", "Bypass", "-File", runner, "-Helper", resolve("scripts/local-processing-mode.ps1"), "-Mode", mode, "-Failure", failure], { encoding: "utf8" }).trim();
  try {
    assert.deepEqual(JSON.parse(run("Disabled")), [["compose", "--env-file", "fixture.env", "-f", "fixture.yml", "stop", "ocr-worker", "image-quality-worker", "page-processing-worker"]]);
    assert.deepEqual(JSON.parse(run("Enabled")), []);
    assert.deepEqual(JSON.parse(run("Explicit")), []);
    assert.throws(() => run("Disabled", "yes"));
  } finally {
    assert.equal(dirname(resolve(dir)), resolve(tmpdir()));
    assert.ok(dir.includes("edugrade-processing-stop-"));
    rmSync(dir, { recursive: true });
  }
});
