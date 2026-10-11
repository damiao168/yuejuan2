import test from "node:test";
import assert from "node:assert/strict";
import { redactStructuredIdentityValues } from "../src/datasets/jorgpt.js";

function assertRedaction(scenario) {
  assert.equal(scenario.synthetic, true);
  assert.deepEqual(redactStructuredIdentityValues(scenario.input), {
    text: scenario.expected,
    redactions: scenario.redactions
  });
}

test("batch27 structured identity redaction preserves prose and all four identity keys", () => {
  assertRedaction({
    synthetic: true,
    input: "Example: {name: Synthetic A, surname: Synthetic B, first_name: Sample, last_name: Person, grade: 9}. A descriptive name: useful.",
    expected: "Example: {name: [redacted], surname: [redacted], first_name: [redacted], last_name: [redacted], grade: 9}. A descriptive name: useful.",
    redactions: 4
  });
});

test("batch27 quoted keys, Unicode case folding, and punctuation keep their original boundaries", () => {
  for (const scenario of [
    { synthetic: true, input: "{\"FIRST_NAME\": \"Sample\", 'Last_Name': 'Person'}", expected: "{\"FIRST_NAME\": [redacted], 'Last_Name': [redacted]}", redactions: 2 },
    { synthetic: true, input: "{\u017furname: Synthetic}", expected: "{\u017furname: [redacted]}", redactions: 1 },
    { synthetic: true, input: "{name': Synthetic}", expected: "{name': [redacted]}", redactions: 1 },
    { synthetic: true, input: "{name: A: [b] {c}, surname: Person}", expected: "{name: [redacted]}, surname: [redacted]}", redactions: 2 },
    { synthetic: true, input: '{name: "Synthetic, Person"}', expected: '{name: [redacted], Person"}', redactions: 1 }
  ]) assertRedaction(scenario);
});

test("batch27 identity values retain the 80 Unicode-code-point limit", () => {
  for (const character of ["x", "\u{1f680}"]) {
    const accepted = character.repeat(80);
    const rejected = character.repeat(81);
    assertRedaction({ synthetic: true, input: `{name: ${accepted}}`, expected: "{name: [redacted]}", redactions: 1 });
    assertRedaction({ synthetic: true, input: `{name: ${rejected}}`, expected: `{name: ${rejected}}`, redactions: 0 });
  }
});

test("batch27 whitespace-only identity values retain prefix greediness and CR/LF exclusions", () => {
  // 原规则必须留下一个值字符；全空白时仅替换最后一个可捕获字符，CR/LF 留在尾部。
  for (const scenario of [
    { synthetic: true, input: "{name:}", expected: "{name:}", redactions: 0 },
    { synthetic: true, input: "{name: }", expected: "{name:[redacted]}", redactions: 1 },
    { synthetic: true, input: "{name:  }", expected: "{name: [redacted]}", redactions: 1 },
    { synthetic: true, input: "{name:\r\n}", expected: "{name:\r\n}", redactions: 0 },
    { synthetic: true, input: "{name: \r\n}", expected: "{name:[redacted]\r\n}", redactions: 1 },
    { synthetic: true, input: "{name: \r\n\t\r\n}", expected: "{name: \r\n[redacted]\r\n}", redactions: 1 },
    { synthetic: true, input: "{name:\ufeff\u00a0}", expected: "{name:\ufeff[redacted]}", redactions: 1 }
  ]) assertRedaction(scenario);
});

test("batch27 newline tails and leading whitespace remain separate from captured identity values", () => {
  for (const scenario of [
    { synthetic: true, input: "{name: Synthetic\r\n  , surname: Person}", expected: "{name: [redacted]\r\n  , surname: [redacted]}", redactions: 2 },
    { synthetic: true, input: "{name: Alpha\nBeta}", expected: "{name: Alpha\nBeta}", redactions: 0 },
    { synthetic: true, input: "{name: \nAlpha}", expected: "{name: \n[redacted]}", redactions: 1 },
    { synthetic: true, input: "{name:\u00a0Alpha\u00a0}", expected: "{name:\u00a0[redacted]}", redactions: 1 }
  ]) assertRedaction(scenario);
});

test("batch27 captured values keep bounded greedy whitespace and leave the remaining tail", () => {
  const tail = " ".repeat(1000);
  assertRedaction({ synthetic: true, input: `{name: ${"x".repeat(79)}${tail}}`, expected: `{name: [redacted]${tail.slice(1)}}`, redactions: 1 });
  assertRedaction({ synthetic: true, input: `{name: ${"x".repeat(80)}${tail}}`, expected: `{name: [redacted]${tail}}`, redactions: 1 });
  const unchanged = `{name: ${"x".repeat(81)}${tail}}`;
  assertRedaction({ synthetic: true, input: unchanged, expected: unchanged, redactions: 0 });
});

test("batch27 nested starts and delimiters preserve search order without inventing new starts", () => {
  const overlong = "x".repeat(81);
  for (const scenario of [
    { synthetic: true, input: "name: Sample, surname: Person}", expected: "name: [redacted], surname: [redacted]}", redactions: 2 },
    { synthetic: true, input: "{}name: Sample, prose name: Person, [name: Sample], {name: Sample", expected: "{}name: Sample, prose name: Person, [name: Sample], {name: Sample", redactions: 0 },
    { synthetic: true, input: `{name: ${overlong} {surname: Sample}}`, expected: `{name: ${overlong} {surname: [redacted]}}`, redactions: 1 },
    { synthetic: true, input: "{name: {surname: Sample}}", expected: "{name: [redacted]}}", redactions: 1 },
    { synthetic: true, input: "{name: A}{surname: B, last_name: C}", expected: "{name: [redacted]}{surname: [redacted], last_name: [redacted]}", redactions: 3 }
  ]) assertRedaction(scenario);
});

test("batch27 long whitespace and missing delimiters remain bounded synthetic inputs", () => {
  const whitespace = " ".repeat(100000);
  const unterminated = `{name:${whitespace}`;
  assertRedaction({ synthetic: true, input: unterminated, expected: unterminated, redactions: 0 });
  assertRedaction({ synthetic: true, input: `{name:${whitespace}}`, expected: `{name:${whitespace.slice(1)}[redacted]}`, redactions: 1 });
  const invalid = `{name:${whitespace}${"x".repeat(81)}}`;
  assertRedaction({ synthetic: true, input: invalid, expected: invalid, redactions: 0 });
});

test("batch27 identity redaction keeps nullish and String conversion behavior", () => {
  for (const scenario of [
    { synthetic: true, input: null, expected: "", redactions: 0 },
    { synthetic: true, input: undefined, expected: "", redactions: 0 },
    { synthetic: true, input: 42, expected: "42", redactions: 0 },
    { synthetic: true, input: { synthetic: true, toString() { return "{name: Sample}"; } }, expected: "{name: [redacted]}", redactions: 1 }
  ]) assertRedaction(scenario);
});
