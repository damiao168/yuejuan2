import { describe, expect, it, vi } from "vitest";
import {
  clearPublicComputerData,
  isPublicComputerIdle,
  isWithinUtf8ByteLimit,
  PUBLIC_COMPUTER_IDLE_LOCK_MS,
  validateNewPassword,
  newPasswordValidator,
  passwordMinimumForRoles
} from "./loginSecurity";

describe("new password length validation", () => {
  it("counts Unicode characters the same way as the API", async () => {
    await expect(validateNewPassword(undefined, "😀".repeat(7))).rejects.toThrow("至少 8");
    await expect(validateNewPassword(undefined, "😀".repeat(8))).resolves.toBeUndefined();
    await expect(validateNewPassword(undefined, "只有老师本人知道的这句很长密码短语")).resolves.toBeUndefined();
  });

  it("uses the administrator minimum for mixed roles and permits common numeric passwords", async () => {
    expect(passwordMinimumForRoles(["grader", "arbitrator"])).toBe(8);
    expect(passwordMinimumForRoles(["grader", "school_admin"])).toBe(12);
    await expect(validateNewPassword(undefined, "12345678")).resolves.toBeUndefined();
    await expect(newPasswordValidator(12)(undefined, "12345678901")).rejects.toThrow("至少 12");
    await expect(newPasswordValidator(12)(undefined, "123456789012")).resolves.toBeUndefined();
    await expect(validateNewPassword(undefined, "123456789012345")).resolves.toBeUndefined();
    await expect(validateNewPassword(undefined, " ".repeat(8))).rejects.toThrow("不能全部为空格");
  });

  it("allows long passphrases but applies the UTF-8 byte ceiling", async () => {
    await expect(validateNewPassword(undefined, "a".repeat(1024))).resolves.toBeUndefined();
    await expect(validateNewPassword(undefined, "a".repeat(1025))).rejects.toThrow("过长");
    expect(isWithinUtf8ByteLimit("汉".repeat(342), 1024)).toBe(false);
  });
});

describe("public computer cleanup", () => {
	it("locks only after the full idle window", () => {
		const lastActivity = 1_000;
		expect(isPublicComputerIdle(lastActivity, lastActivity + PUBLIC_COMPUTER_IDLE_LOCK_MS - 1)).toBe(false);
		expect(isPublicComputerIdle(lastActivity, lastActivity + PUBLIC_COMPUTER_IDLE_LOCK_MS)).toBe(true);
	});

  it("removes only local data scoped to the current tenant and user", () => {
    const values = new Map([
      ["business-command:demo:user-1:publish:exam", "secret"],
      ["exam-create-draft:demo:user-1", "draft"],
      ["business-command:demo:user-2:publish:exam", "other"],
      ["edugrade.navigation.collapsed", "true"]
    ]);
    const storage = {
      get length() { return values.size; },
      key: (index: number) => [...values.keys()][index] ?? null,
      removeItem: (key: string) => values.delete(key)
    };
    vi.stubGlobal("window", { localStorage: storage });
    clearPublicComputerData("demo", "user-1");
    expect([...values.keys()]).toEqual(["business-command:demo:user-2:publish:exam", "edugrade.navigation.collapsed"]);
    vi.unstubAllGlobals();
  });
});
