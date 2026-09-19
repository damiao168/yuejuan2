import assert from "node:assert/strict";
import { test } from "node:test";

import { checkModuleBoundaries, findLargeInterfaces, pageWarning } from "../check-architecture-boundaries.mjs";

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
  assert.match(pageWarning("line\n".repeat(1001), "Page.tsx"), /Page\.tsx/);
});
