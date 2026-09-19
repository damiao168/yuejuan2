import assert from "node:assert/strict";
import { readFileSync, readdirSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const root = join(dirname(fileURLToPath(import.meta.url)), "..");
const read = (...parts) => readFileSync(join(root, ...parts), "utf8");

const service = read("services", "api-gateway", "internal", "onboarding", "service.go");
const route = read("services", "api-gateway", "internal", "server", "routes_onboarding.go");
const openapi = read("services", "api-gateway", "openapi", "edugrade-api.openapi.json");
const appShell = read("apps", "web-admin", "src", "AppShell.tsx");
const routes = read("apps", "web-admin", "src", "router", "routes.tsx");
const organizationSetup = read("apps", "web-admin", "src", "pages", "OrganizationSetupPage.tsx");
const migrations = readdirSync(join(root, "services", "api-gateway", "migrations"), { recursive: true }).map(String);

assert.ok(route.includes("GET /api/v1/onboarding/readiness"), "readiness route must be registered");
assert.ok(openapi.includes('"operationId": "getOnboardingReadiness"'), "readiness route must be in OpenAPI");
assert.ok(routes.includes('path: "/platform/getting-started"'), "platform getting-started route must exist");
assert.ok(appShell.includes("<OnboardingGate"), "application shell must include the fail-open onboarding gate");
assert.ok(service.includes("SeverityRecommended") && service.includes('"first_exam"'), "first exam must remain recommended");
assert.ok(organizationSetup.includes("BUSINESS_ROLES"), "organization setup must identify business users by role");
assert.equal(organizationSetup.includes("localStorage"), false, "organization readiness must not use localStorage as authority");
assert.equal(migrations.some((name) => name.toLowerCase().includes("setup_progress")), false, "onboarding must not add a setup_progress migration");
assert.equal(appShell.includes("EDUGRADE_POSTGRES_PASSWORD"), false, "the web application must not expose deployment secrets");

console.log("Onboarding/readiness invariants passed");
