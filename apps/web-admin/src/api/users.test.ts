import { afterEach, describe, expect, it, vi } from "vitest";
import { listAllActiveGraders, type ManagedUser } from "./users";

afterEach(() => vi.unstubAllGlobals());
const grader = (id: string, status = "active"): ManagedUser => ({ id, username: id, display_name: id, status, roles: ["grader"] });
const response = (users: ManagedUser[], has_more: boolean, next_cursor: string) => new Response(JSON.stringify({ users, has_more, next_cursor }));

describe("quality reviewer pagination", () => {
  it("loads beyond 200 graders with the server role filter and removes inactive/duplicate users", async () => {
    const fetch = vi.fn()
      .mockResolvedValueOnce(response(Array.from({ length: 200 }, (_, i) => grader(`g-${i}`)), true, "page-2"))
      .mockResolvedValueOnce(response([grader("g-199"), grader("g-200"), grader("disabled", "disabled"), { ...grader("admin"), roles: ["school_admin"] }], false, ""));
    vi.stubGlobal("fetch", fetch);
    const users = await listAllActiveGraders();
    expect(users).toHaveLength(201);
    expect(users[users.length - 1]?.id).toBe("g-200");
    expect(fetch.mock.calls.map(([url]) => new URL(url, "http://local").searchParams.get("role"))).toEqual(["grader", "grader"]);
    expect(new URL(fetch.mock.calls[1][0], "http://local").searchParams.get("cursor")).toBe("page-2");
  });

  it("reports a broken cursor instead of silently returning an incomplete reviewer list", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(response([grader("g-1")], true, "")));
    await expect(listAllActiveGraders()).rejects.toThrow("分页异常");
  });
});
