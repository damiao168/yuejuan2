import type { SessionUser } from "./session";

export class SessionTransitionChangedError extends Error {
  constructor() {
    super("登录状态已变化，请重新确认当前账号");
    this.name = "SessionTransitionChangedError";
  }
}

/** 会话结果先通过代次检查，再绑定命令范围和发布身份；认证写请求串行，避免迟到 Cookie 覆盖新登录。 */
export function createSessionLifecycle({ publishUser, bindCommandScope, onTransition }: {
  publishUser: (user: SessionUser | null) => void;
  bindCommandScope: (tenant: string, actor: string) => void;
  onTransition: () => void;
}) {
  let generation = 0;
  let user: SessionUser | null = null;
  let locked = false;
  let authenticationQueue: Promise<unknown> = Promise.resolve();
  const isCurrent = (expected: number) => generation === expected;
  const assertCurrent = (expected: number) => {
    if (!isCurrent(expected)) throw new SessionTransitionChangedError();
  };
  const begin = (keepUser = false) => {
    generation += 1;
    locked = keepUser;
    bindCommandScope("", "");
    if (!keepUser) { user = null; publishUser(null); }
    onTransition();
    return generation;
  };
  return {
    get generation() { return generation; },
    get user() { return user; },
    isCurrent,
    assertCurrent,
    begin,
    invalidate(): void {
      generation += 1;
      bindCommandScope("", "");
    },
    commit(expected: number, nextUser: SessionUser | null): boolean {
      if (!isCurrent(expected)) return false;
      bindCommandScope(nextUser?.tenant ?? "", nextUser?.id ?? "");
      user = nextUser;
      locked = false;
      publishUser(nextUser);
      return true;
    },
    updateUser(expected: number, nextUser: SessionUser): void {
      if (!isCurrent(expected) || user?.id !== nextUser.id || user.tenant !== nextUser.tenant) return;
      user = nextUser;
      publishUser(nextUser);
    },
    unlock(expected: number): void {
      assertCurrent(expected);
      if (!user) throw new SessionTransitionChangedError();
      if (locked) {
        bindCommandScope(user.tenant, user.id);
        locked = false;
        onTransition();
      }
    },
    // logout 必须排在已发送的 login 后面；后来 login 再排在 logout 后，最后一个 Cookie 才属于最后一次操作。
    enqueueAuthentication<T>(action: () => Promise<T>): Promise<T> {
      const work = authenticationQueue.catch(() => undefined).then(action);
      authenticationQueue = work;
      return work;
    },
  };
}
