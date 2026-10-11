import { describe, expect, it, vi } from "vitest";
import type { SessionUser } from "./session";
import { createSessionLifecycle } from "./sessionLifecycle";

function user(id: string): SessionUser {
  return { id, tenant: "school", username: id, displayName: id, name: id, role: "teacher", roles: ["teacher"], school: "School", currentExam: "", permissions: [], publicComputer: false, organizationScope: { resolved: true, tenantWide: false, schoolIds: [], gradeIds: [], classIds: [] } };
}

function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((onResolve) => { resolve = onResolve; });
  return { promise, resolve };
}

function setup() {
  const publishUser = vi.fn();
  const bindCommandScope = vi.fn();
  const session = createSessionLifecycle({ publishUser, bindCommandScope, onTransition: vi.fn() });
  return { session, publishUser, bindCommandScope };
}

describe("session transition ownership", () => {
  it("binds the command identity before publishing a restored user", () => {
    const events: string[] = [];
    const session = createSessionLifecycle({ publishUser: (value) => events.push(`user:${value?.id ?? ""}`), bindCommandScope: (_tenant, actor) => events.push(`scope:${actor}`), onTransition: () => undefined });
    const restore = session.begin();
    events.length = 0;
    expect(session.commit(restore, user("A"))).toBe(true);
    expect(events).toEqual(["scope:A", "user:A"]);
  });

  it("does not restore a user or bind a command scope after logout invalidates restoration", () => {
    const { session, publishUser, bindCommandScope } = setup();
    const restore = session.begin();
    session.begin();
    publishUser.mockClear();
    bindCommandScope.mockClear();
    expect(session.commit(restore, user("A"))).toBe(false);
    expect(session.user).toBeNull();
    expect(publishUser).not.toHaveBeenCalled();
    expect(bindCommandScope).not.toHaveBeenCalled();
  });

  it("serializes rapid A/B authentication and publishes only B", async () => {
    const { session, publishUser } = setup();
    const first = deferred<SessionUser>();
    const second = deferred<SessionUser>();
    const writes: string[] = [];
    let cookie = "";
    const login = async (name: string, response: Promise<SessionUser>) => {
      const generation = session.begin();
      const result = await session.enqueueAuthentication(async () => {
        session.assertCurrent(generation);
        writes.push(name);
        const next = await response;
        cookie = next.id;
        return next;
      });
      session.commit(generation, result);
    };
    const A = login("A", first.promise);
    await Promise.resolve();
    await Promise.resolve();
    const B = login("B", second.promise);
    expect(writes).toEqual(["A"]);
    first.resolve(user("A"));
    await A;
    expect(session.user).toBeNull();
    second.resolve(user("B"));
    await B;
    expect(writes).toEqual(["A", "B"]);
    expect(cookie).toBe("B");
    expect(session.user?.id).toBe("B");
    expect(publishUser.mock.calls.filter(([value]) => value?.id === "A")).toHaveLength(0);
  });

  it("clears local identity immediately and orders logout between an in-flight login and the next login", async () => {
    const { session } = setup();
    const response = deferred<void>();
    const writes: string[] = [];
    const A = session.begin();
    const first = session.enqueueAuthentication(async () => { writes.push("login:A"); await response.promise; session.commit(A, user("A")); });
    await Promise.resolve();
    await Promise.resolve();
    session.begin();
    expect(session.user).toBeNull();
    const logout = session.enqueueAuthentication(async () => { writes.push("logout"); });
    const B = session.begin();
    const second = session.enqueueAuthentication(async () => { session.assertCurrent(B); writes.push("login:B"); session.commit(B, user("B")); });
    response.resolve();
    await Promise.all([first, logout, second]);
    expect(writes).toEqual(["login:A", "logout", "login:B"]);
    expect(session.user?.id).toBe("B");
  });

  it("keeps the scope blocked while locked and rejects an old unlock after a new identity commits", () => {
    const { session, bindCommandScope } = setup();
    session.commit(session.begin(), user("A"));
    const lock = session.begin(true);
    expect(session.user?.id).toBe("A");
    expect(bindCommandScope).toHaveBeenLastCalledWith("", "");
    session.commit(session.begin(), user("B"));
    bindCommandScope.mockClear();
    expect(() => session.unlock(lock)).toThrow("登录状态已变化");
    expect(bindCommandScope).not.toHaveBeenCalled();
    expect(session.user?.id).toBe("B");
  });

  it("does not replace a different tenant identity with late school metadata", () => {
    const { session, publishUser } = setup();
    const first = session.begin();
    session.commit(first, user("same"));
    session.commit(session.begin(), { ...user("same"), tenant: "other-school" });
    publishUser.mockClear();
    session.updateUser(first, { ...user("same"), school: "Late" });
    expect(session.user?.tenant).toBe("other-school");
    expect(publishUser).not.toHaveBeenCalled();
  });
});
