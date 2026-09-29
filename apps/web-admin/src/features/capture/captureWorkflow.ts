export class LatestRequestGate {
  // 只判断回填是否过期，不会取消网络请求；切换作用域或重新加载时需主动使旧序号失效。
  private revision = 0;

  begin() {
    this.revision += 1;
    return this.revision;
  }

  isCurrent(revision: number) {
    return revision === this.revision;
  }

  invalidate() {
    this.revision += 1;
  }
}

export class ExclusiveCommandGate {
  // 这是当前页面实例内的同步互斥，跨刷新和跨设备的幂等性仍由业务命令键与服务端负责。
  private readonly active = new Set<string>();

  enter(key: string) {
    if (this.active.has(key)) return false;
    this.active.add(key);
    return true;
  }

  leave(key: string) {
    this.active.delete(key);
  }
}

export async function runCaptureCommand({
  key,
  gate,
  command,
  reload,
  onStart,
  onSettled
}: {
  key: string;
  gate: ExclusiveCommandGate;
  command: () => Promise<void>;
  reload?: () => Promise<void>;
  onStart?: () => void;
  onSettled?: () => void;
}) {
  if (!gate.enter(key)) return "ignored" as const;
  onStart?.();
  try {
    await command();
    await reload?.();
    return "completed" as const;
  } finally {
    gate.leave(key);
    onSettled?.();
  }
}
