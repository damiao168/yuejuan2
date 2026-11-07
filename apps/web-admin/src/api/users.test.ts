import { afterEach, describe, expect, it, vi } from "vitest";
import { createManagedUser, listAllActiveGraders, listAllManagedUsers, type ManagedUser } from "./users";

afterEach(() => vi.unstubAllGlobals());
const grader = (id: string, status = "active"): ManagedUser => ({ id, username: id, display_name: id, status, roles: ["grader"] });
const response = (users: ManagedUser[], has_more: boolean, next_cursor: string) => new Response(JSON.stringify({ users, has_more, next_cursor }));

describe("quality reviewer pagination", () => {
  it("教师账号的初始密码和确认原样传递，不把确认或首改标记混入返回凭据", async () => {
    const fetch = vi.fn().mockResolvedValue(new Response(JSON.stringify({ user: { ...grader("new"), must_change_password: true } })));
    vi.stubGlobal("fetch", fetch);
    const password = " 保留空格的初始密码123 ";
    await createManagedUser({ username: "new", display_name: "教师", school_id: "school", teaching_subjects: ["mathematics"], role_codes: ["teacher", "grader", "arbitrator"], password, password_confirm: password });
    expect(JSON.parse(fetch.mock.calls[0][1].body)).toMatchObject({ password, password_confirm: password });
  });
  it("完整人员列表读取超过 200 人，保留服务器搜索条件并去重", async () => {
    const fetch = vi.fn()
      .mockResolvedValueOnce(response(Array.from({ length: 200 }, (_, i) => grader(`g-${i}`)), true, "page-2"))
      .mockResolvedValueOnce(response([grader("g-199"), grader("g-200")], false, ""));
    vi.stubGlobal("fetch", fetch);
    const users = await listAllManagedUsers({ q: "教师" });
    expect(users).toHaveLength(201);
    expect(new URL(fetch.mock.calls[1][0], "http://local").searchParams.get("cursor")).toBe("page-2");
    expect(fetch.mock.calls.map(([url]) => new URL(url, "http://local").searchParams.get("q"))).toEqual(["教师", "教师"]);
  });

  it("完整人员列表拒绝重复游标，不能悄悄用部分名单确认管理员人数", async () => {
    vi.stubGlobal("fetch", vi.fn().mockImplementation(async () => response([grader("g-1")], true, "same")));
    await expect(listAllManagedUsers()).rejects.toThrow("分页异常");
  });
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
