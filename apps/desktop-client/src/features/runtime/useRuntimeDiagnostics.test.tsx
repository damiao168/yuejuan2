// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { DesktopApiClient } from "../../api/client";
import { useRuntimeDiagnostics } from "./useRuntimeDiagnostics";

vi.mock("../../lib/localRuntime", () => ({ getCapabilityStatuses: async () => [], getRuntimeDiagnostics: async () => null, scanLocalCacheSecurity: () => ({ issues: [] }) }));
describe("connection check feedback", () => {
  let root: Root; let container: HTMLDivElement; let state: ReturnType<typeof useRuntimeDiagnostics>;
  const log = vi.fn(async () => {}); const setServer = vi.fn();
  const client = new DesktopApiClient({ baseUrl: "https://old.example.edu", getToken: () => "old-token" });
  function Probe({ server, networkAllowed = true }: { server: string; networkAllowed?: boolean }) { state = useRuntimeDiagnostics(client, server, "scope", setServer, log, undefined, networkAllowed); return null; }
  beforeEach(() => {
    vi.clearAllMocks(); Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
    container = document.createElement("div"); root = createRoot(container);
  });
  afterEach(async () => { await act(async () => root.unmount()); vi.unstubAllGlobals(); });

  it("checks the draft server without credentials and shows busy then successful elapsed feedback", async () => {
    let finish!: (value: Response) => void;
    const fetch = vi.fn((_input: RequestInfo | URL, _init?: RequestInit) => new Promise<Response>((resolve) => { finish = resolve; })); vi.stubGlobal("fetch", fetch);
    await act(async () => root.render(<Probe server="https://school.example.edu" />));
    let pending!: Promise<void>;
    await act(async () => { pending = state.handleHealthCheck(); await state.handleHealthCheck(); });
    expect(state.isCheckingHealth).toBe(true); expect(fetch).toHaveBeenCalledTimes(1);
    expect(fetch.mock.calls[0][0]).toBe("https://school.example.edu/health");
    expect((fetch.mock.calls[0][1] as RequestInit).headers).toEqual(expect.any(Headers));
    expect(new Headers((fetch.mock.calls[0][1] as RequestInit).headers).has("Authorization")).toBe(false);
    await act(async () => { finish(new Response("{}", { status: 200 })); await pending; });
    expect(state.isCheckingHealth).toBe(false); expect(state.healthResult).toMatchObject({ ok: true, server: "https://school.example.edu" });
    expect(state.healthResult?.elapsedMs).toBeGreaterThanOrEqual(0);
  });

  it("keeps invalid input editable and discards a response from a superseded server", async () => {
    const fetch = vi.fn(); vi.stubGlobal("fetch", fetch);
    await act(async () => root.render(<Probe server="" />));
    await act(async () => state.handleHealthCheck());
    expect(state.healthResult?.ok).toBe(false); expect(fetch).not.toHaveBeenCalled();
    let finish!: (value: Response) => void;
    fetch.mockImplementation(() => new Promise<Response>((resolve) => { finish = resolve; }));
    await act(async () => root.render(<Probe server="https://first.example.edu" />));
    let pending!: Promise<void>; await act(async () => { pending = state.handleHealthCheck(); });
    await act(async () => root.render(<Probe server="https://second.example.edu" />));
    await act(async () => { finish(new Response("{}")); await pending; });
    expect(state.healthResult).toBeNull(); expect(state.isCheckingHealth).toBe(false);
  });
  it("does not send connection or system requests using only local offline authorization", async () => {
    const fetch = vi.fn(); vi.stubGlobal("fetch", fetch);
    await act(async () => root.render(<Probe server="https://school.example.edu" networkAllowed={false} />));
    await act(async () => { await state.handleHealthCheck(); await state.handleSystemStatusCheck(); });
    expect(fetch).not.toHaveBeenCalled(); expect(state.diagnosticError).toContain("离线本机模式");
  });
});
