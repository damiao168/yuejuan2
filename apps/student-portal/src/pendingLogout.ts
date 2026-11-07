const key = "edugrade.student.logout-pending";

export function hasPendingLogout(): boolean {
  let durable = false;
  for (const name of ["localStorage", "sessionStorage"] as const) {
    try {
      const storage = window[name];
      if (storage.getItem(key) === "1") return true;
      // 只读检查发现不了配额耗尽，必须确认退出意图可以跨刷新保存。
      storage.setItem(`${key}.probe`, "1");
      durable ||= storage.getItem(`${key}.probe`) === "1";
      storage.removeItem(`${key}.probe`);
    } catch { /* 继续尝试另一种跨刷新存储。 */ }
  }
  // 无法保存退出意图时，每次启动先撤销 Cookie；不能默认恢复上一名学生。
  return !durable;
}

export function rememberPendingLogout(pending: boolean): void {
  // 双写无身份标记，持久化存储被禁用时仍可用会话存储保留退出意图。
  for (const name of ["localStorage", "sessionStorage"] as const) {
    try { if (pending) window[name].setItem(key, "1"); else window[name].removeItem(key); } catch { /* 完全不可写时由启动检查先撤销会话。 */ }
  }
}
