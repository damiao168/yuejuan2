import { readFile, readdir } from "node:fs/promises";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const appRoot = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const tauriConfig = JSON.parse(await readFile(join(appRoot, "src-tauri", "tauri.conf.json"), "utf8"));
const capability = JSON.parse(await readFile(join(appRoot, "src-tauri", "capabilities", "default.json"), "utf8"));
const cargoManifest = await readFile(join(appRoot, "src-tauri", "Cargo.toml"), "utf8");
const apiClient = await readFile(join(appRoot, "src", "api", "client.ts"), "utf8");
const localRuntime = await readFile(join(appRoot, "src", "lib", "localRuntime.ts"), "utf8");
const failures = [];

function fail(message) {
  failures.push(message);
}

function sources(policy, directive) {
  const value = policy?.[directive];
  if (Array.isArray(value)) return value;
  return typeof value === "string" ? value.split(/\s+/).filter(Boolean) : [];
}

function requireSources(policy, directive, expected) {
  const actual = sources(policy, directive);
  for (const source of expected) {
    if (!actual.includes(source)) fail(`${directive} must include ${source}`);
  }
  if (actual.includes("*")) fail(`${directive} must not contain a wildcard source`);
}

const security = tauriConfig.app?.security;
if (!security || !security.csp || !security.devCsp) {
  fail("production and development CSP must both be configured");
} else {
  requireSources(security.csp, "default-src", ["'self'"]);
  requireSources(security.csp, "connect-src", ["ipc:", "http://ipc.localhost", "http:", "https:"]);
  requireSources(security.csp, "img-src", ["'self'", "blob:", "data:"]);
  requireSources(security.csp, "object-src", ["'none'"]);
  requireSources(security.csp, "base-uri", ["'none'"]);
  requireSources(security.csp, "frame-ancestors", ["'none'"]);
  requireSources(security.csp, "script-src-attr", ["'none'"]);
  if (sources(security.csp, "script-src").some((source) => source.includes("unsafe-"))) {
    fail("production script-src must not allow unsafe-inline or unsafe-eval");
  }
  requireSources(security.devCsp, "connect-src", ["ipc:", "http://ipc.localhost", "ws://127.0.0.1:5180"]);
}

if (security?.dangerousDisableAssetCspModification !== false) {
  fail("Tauri CSP asset modification must remain enabled");
}
if (JSON.stringify(security?.capabilities) !== JSON.stringify(["default"])) {
  fail("tauri.conf.json must explicitly enable only the default capability");
}

const mainWindows = (tauriConfig.app?.windows ?? []).filter((window) => window.label === "main");
if (mainWindows.length !== 1) fail("exactly one explicitly labelled main window is required");
if (capability.local !== true || capability.remote !== undefined) {
  fail("the default capability must be local-only");
}
if (JSON.stringify(capability.windows) !== JSON.stringify(["main"])) {
  fail("the default capability must apply only to the main window");
}
if (JSON.stringify(capability.platforms) !== JSON.stringify(["windows"])) {
  fail("the default capability must be Windows-only");
}
if (!Array.isArray(capability.permissions) || capability.permissions.length !== 0) {
  fail("no Tauri core or plugin permission is currently required");
}

const expectedCommands = new Set([
  "archive_durable_scan_queue_items",
  "append_local_log",
  "begin_spool_local_asset",
  "bind_durable_session",
  "capability_statuses",
  "claim_legacy_durable_store",
  "clear_durable_session",
  "clear_local_logs",
  "complete_spool_local_asset",
  "delete_desktop_credentials",
  "delete_scanner_profile",
  "list_durable_drafts",
  "list_durable_scan_queue",
  "list_scanner_devices",
  "list_scanner_profiles",
  "load_durable_draft",
  "load_desktop_credentials",
  "persist_durable_scan_queue_item",
  "purge_expired_durable_drafts",
  "read_durable_local_asset",
  "read_durable_local_asset_chunk",
  "runtime_diagnostics",
  "run_scanner_preflight",
  "save_durable_draft",
  "save_desktop_credentials",
  "save_scanner_profile",
  "scanner_integration_status",
  "update_durable_draft_status",
  "write_spool_local_asset_chunk"
]);
const invokedCommands = new Set();

async function inspectSources(directory) {
  for (const entry of await readdir(directory, { withFileTypes: true })) {
    const path = join(directory, entry.name);
    if (entry.isDirectory()) {
      await inspectSources(path);
      continue;
    }
    if (!/\.(?:ts|tsx)$/.test(entry.name)) continue;
    const source = await readFile(path, "utf8");
    for (const match of source.matchAll(/\b(?:invoke|invokeOptional|invokeScoped)(?:<[^>]+>)?\(\s*["']([^"']+)["']/g)) {
      invokedCommands.add(match[1]);
    }
    if (source.includes("@tauri-apps/plugin-")) {
      fail(`${path} imports a Tauri plugin but the capability grants no plugin permission`);
    }
  }
}

await inspectSources(join(appRoot, "src"));
for (const command of invokedCommands) {
  if (!expectedCommands.has(command)) fail(`unexpected application command invoked by the frontend: ${command}`);
}
for (const command of expectedCommands) {
  if (!invokedCommands.has(command)) fail(`expected application command is not invoked by the frontend: ${command}`);
}

if (!/cfg\(windows\)[\s\S]*keyring[\s\S]*windows-native/.test(cargoManifest)) {
  fail("saved desktop credentials must use the Windows-native system credential store");
}
if (!/requireTauriCredentialStore/.test(localRuntime)
    || !/系统凭据库仅在 Windows 桌面客户端中可用；已拒绝不安全降级/.test(localRuntime)) {
  fail("credential persistence must fail closed outside the native Windows runtime");
}
if (/saveStoredCredentials[\s\S]{0,500}localStorage\.setItem/.test(localRuntime)) {
  fail("credential persistence must never fall back to localStorage");
}
if (!/远程 API 必须使用 HTTPS；HTTP 仅允许本机开发地址/.test(apiClient)) {
  fail("desktop API credentials must not be sent over non-loopback HTTP");
}

if (failures.length) {
  console.error("Desktop security configuration check failed:");
  failures.forEach((failure) => console.error(`- ${failure}`));
  process.exit(1);
}

console.log("Desktop security configuration checks passed.");
