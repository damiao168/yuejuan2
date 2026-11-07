import fs from "node:fs";
import path from "node:path";
import { execFileSync } from "node:child_process";
import { fileURLToPath } from "node:url";

const root=path.resolve(path.dirname(fileURLToPath(import.meta.url)),"../..");
const workflow=fs.readFileSync(path.join(root,".github/workflows/ci.yml"),"utf8");
const targets=[...workflow.matchAll(/(?:command:|run:) python -m pytest -q ([^\r\n]+)/g)].flatMap(m=>m[1].trim().split(/\s+/)).filter(t=>!t.startsWith("-"));
const files=execFileSync("git",["ls-files","-z"],{cwd:root,maxBuffer:10*1024*1024}).toString().split("\0").filter(p=>/(?:^|\/)(?:test_.*|.*_test)\.py$/.test(p));
const missing=files.filter(file=>!targets.some(target=>file===target || file.startsWith(target.replace(/\/$/,"")+"/")));
if(missing.length) throw new Error(`Python test files absent from CI pytest targets:\n${missing.join("\n")}`);
console.log(`All ${files.length} tracked Python test files are covered by CI pytest targets.`);
