const key = "edugrade.web.logout-pending";

export function hasPendingLogout(): boolean {
  let durable = false;
  for (const name of ["localStorage", "sessionStorage"] as const) {
    try {
      const storage = window[name];
      if (storage.getItem(key) === "1") return true;
      // getItem 成功不代表可以保存退出意图，例如配额耗尽时 setItem 仍会失败。
      storage.setItem(`${key}.probe`, "1");
      durable ||= storage.getItem(`${key}.probe`) === "1";
      storage.removeItem(`${key}.probe`);
    } catch { /* 继续尝试另一种跨刷新存储。 */ }
  }
  // 两种存储都不可写时无法区分首次进入和退出后的刷新，启动先撤销 Cookie 会话。
  return !durable;
}

export function rememberPendingLogout(pending: boolean): void {
  // 标记不含身份或凭据；双写使一类存储不可用时仍能在刷新后先重试撤销。
  for (const name of ["localStorage", "sessionStorage"] as const) {
    try { if (pending) window[name].setItem(key, "1"); else window[name].removeItem(key); } catch { /* 启动检查对完全不可写的环境保守撤销会话。 */ }
  }
}
