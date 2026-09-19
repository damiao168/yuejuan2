import assert from "node:assert/strict";
import { test } from "node:test";

import {
  checkModuleBoundaries, countBroadAuthStoreConsumers, findLargeInterfaces,
  hookWarnings, MAX_BROAD_AUTH_STORE_CONSUMERS, pageWarning
} from "../check-architecture-boundaries.mjs";

test("module constructors must use capabilities instead of whole modules", () => {
  const source = `type IdentityModule struct {}
func NewGradingModule(identity *IdentityModule) *GradingModule { return nil }
`;
  assert.match(checkModuleBoundaries(source).join("\n"), /accepts \*IdentityModule/);
});

test("dependency structs reject HTTP handlers but permit application ports", () => {
  const bad = `type GradingDependencies struct {
  Image http.HandlerFunc
}`;
  const good = `type GradingDependencies struct {
  Image segment.CropImageReader
}`;
  assert.match(checkModuleBoundaries(bad).join("\n"), /http\.HandlerFunc/);
  assert.deepEqual(checkModuleBoundaries(good), []);
});

test("size signals remain warnings", () => {
  const methods = Array.from({ length: 21 }, (_, index) => `  Method${index}() error`).join("\n");
  assert.equal(findLargeInterfaces(`type Store interface {\n${methods}\n}`, "store.go").length, 1);
  assert.match(pageWarning("line\n".repeat(701), "Page.tsx"), /Page\.tsx/);
  assert.equal(pageWarning("line\n".repeat(700), "Page.tsx"), null);
  assert.equal(hookWarnings("const x = useState(0);\n".repeat(16), "useExample.ts").length, 1);
  assert.equal(hookWarnings("line\n".repeat(351), "useExample.ts").length, 1);
});

test("broad audit store consumers cannot grow beyond the debt budget", () => {
  const source = `type Handler struct {
  audit auth.Store
}
func NewHandler(audit auth.Store) {}
type Dependencies struct { Audit auth.Store }
`;
  assert.equal(countBroadAuthStoreConsumers([source]), 1);
  assert.ok(MAX_BROAD_AUTH_STORE_CONSUMERS < 20);
});
