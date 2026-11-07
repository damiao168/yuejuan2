// @vitest-environment jsdom
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { hasPendingLogout, rememberPendingLogout } from "./pendingLogout";

beforeEach(() => { localStorage.clear(); sessionStorage.clear(); });
afterEach(() => { vi.restoreAllMocks(); });

it("normal storage restores sessions and a confirmed logout removes both markers", () => {
  expect(hasPendingLogout()).toBe(false);
  rememberPendingLogout(true);
  expect(hasPendingLogout()).toBe(true);
  rememberPendingLogout(false);
  expect(hasPendingLogout()).toBe(false);
});

it("session storage preserves logout intent when local storage is denied", () => {
  vi.spyOn(window, "localStorage", "get").mockImplementation(() => { throw new DOMException("blocked", "SecurityError"); });
  expect(hasPendingLogout()).toBe(false);
  rememberPendingLogout(true);
  expect(hasPendingLogout()).toBe(true);
  rememberPendingLogout(false);
  expect(hasPendingLogout()).toBe(false);
});

it("readable but unwritable storage requires cookie revocation before restoration", () => {
  vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => { throw new DOMException("quota", "QuotaExceededError"); });
  rememberPendingLogout(true);
  expect(hasPendingLogout()).toBe(true);
});

it("fully denied storage also requires cookie revocation", () => {
  for (const name of ["localStorage", "sessionStorage"] as const) vi.spyOn(window, name, "get").mockImplementation(() => { throw new DOMException("blocked", "SecurityError"); });
  expect(hasPendingLogout()).toBe(true);
});
