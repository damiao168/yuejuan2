// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

beforeEach(() => vi.resetModules());
afterEach(() => { vi.unstubAllEnvs(); vi.unstubAllGlobals(); });

describe("student API configured base URL", () => {
  it.each([
    { name: "unset same-origin address", configured: undefined, expected: "" },
    { name: "blank same-origin address", configured: " \t\n ", expected: "" },
    { name: "all-slash same-origin address", configured: " /// ", expected: "" },
    { name: "absolute address", configured: " https://school.example/portal/// ", expected: "https://school.example/portal" },
    { name: "protocol-relative address", configured: " //school.example/portal// ", expected: "//school.example/portal" },
    { name: "root-relative path", configured: " /portal/proxy/// ", expected: "/portal/proxy" },
    { name: "relative path", configured: " ../portal/proxy// ", expected: "../portal/proxy" },
    { name: "long trailing slash suffix", configured: `https://school.example${"/".repeat(4096)}`, expected: "https://school.example" },
    { name: "long internal slash sequence", configured: `relative/${"/".repeat(4096)}leaf///`, expected: `relative/${"/".repeat(4096)}leaf` }
  ])("preserves request URL and cookie behavior for $name", async ({ configured, expected }) => {
    vi.stubEnv("VITE_API_BASE_URL", configured);
    const response = { user: { id: "student-1" } };
    const fetch = vi.fn().mockResolvedValue(new Response(JSON.stringify(response), { headers: { "Content-Type": "application/json" } }));
    vi.stubGlobal("fetch", fetch);
    const { currentUser } = await import("./api");
    expect(await currentUser()).toEqual(response);
    expect(fetch).toHaveBeenCalledExactlyOnceWith(`${expected}/api/v1/auth/me`, {
      credentials: "include", headers: { Accept: "application/json" }
    });
  });
});
