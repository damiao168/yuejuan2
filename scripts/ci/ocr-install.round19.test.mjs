import assert from "node:assert/strict";
import { existsSync, readFileSync } from "node:fs";
import { spawnSync } from "node:child_process";
import test from "node:test";

const dockerfile = readFileSync(new URL("../../services/ocr-worker/Dockerfile", import.meta.url), "utf8");
const run = dockerfile.match(/RUN --mount=type=cache,target=\/root\/\.cache\/pip,sharing=locked ([\s\S]*?)(?:\r?\n){2}/)?.[1].replace(/\\\r?\n/g, " ");
assert.ok(run, "actual dependency RUN must be tested");
const shell = process.platform === "win32" ? "C:/Program Files/Git/usr/bin/sh.exe" : "/bin/sh";
assert.ok(existsSync(shell), "install a POSIX shell to test the real Docker RUN");
const stubs = `
python() {
  case "$*" in
    *pip==*) step=bootstrap ;;
    *paddleocr==*) step=paddle ;;
    *ftfy==*) step=base ;;
    *) return 99 ;;
  esac
  printf '%s\\n' "$step"
  if [ "$FAIL_STEP" = "$step" ]; then return 37; fi
}
paddlex() {
  printf '%s\\n' hpi
  if [ "$FAIL_STEP" = hpi ]; then return 37; fi
}
`;
const execute = (failStep, paddle = "true", hpi = "true") => spawnSync(shell, ["-c", stubs + run], {
  encoding: "utf8", env: { ...process.env, FAIL_STEP: failStep, INSTALL_PADDLEOCR: paddle, INSTALL_PADDLEOCR_HPI: hpi, PIP_INDEX_URL: "https://synthetic.invalid" },
});

test("actual OCR RUN stops at every failing dependency command", () => {
  for (const [step, expected] of [["bootstrap", ["bootstrap"]], ["paddle", ["bootstrap", "paddle"]], ["hpi", ["bootstrap", "paddle", "hpi"]], ["base", ["bootstrap", "paddle", "hpi", "base"]]]) {
    const result = execute(step);
    assert.equal(result.status, 37, `${step}: ${result.stderr}`);
    assert.deepEqual(result.stdout.trim().split(/\r?\n/), expected);
  }
});

test("actual OCR RUN retains Paddle/HPI flags and unconditional base libraries", () => {
  for (const [paddle, hpi, expected] of [["true", "true", ["bootstrap", "paddle", "hpi", "base"]], ["true", "false", ["bootstrap", "paddle", "base"]], ["false", "true", ["bootstrap", "base"]]]) {
    const result = execute("", paddle, hpi);
    assert.equal(result.status, 0, result.stderr);
    assert.deepEqual(result.stdout.trim().split(/\r?\n/), expected);
  }
});
