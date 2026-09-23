import assert from "node:assert/strict";
import { test } from "node:test";

import { coverageFailures, parseCoverage } from "./check-go-coverage.mjs";

test("coverage ratchet checks every required package and rejects regressions", () => {
  const report = `ok  edugrade-enterprise/services/api-gateway/internal/auth  1.2s  coverage: 57.1% of statements
ok  edugrade-enterprise/services/api-gateway/internal/capture  1.2s  coverage: 11.3% of statements
`;
  const failures = coverageFailures(parseCoverage(report), { auth: 57.1, capture: 11.4, subjective: 63.4 });
  assert.deepEqual(failures, [
    "capture: 11.3% is below the 11.4% floor",
    "subjective: no coverage result",
  ]);
});
