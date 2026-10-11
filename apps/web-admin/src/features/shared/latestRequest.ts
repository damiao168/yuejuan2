export interface RequestTicket {
  readonly key: string;
  isCurrent(): boolean;
}

/**
 * 协调限定到当前所选资源的异步投影。
 * 响应只有在其请求凭据仍有效时才能更新 React 状态。
 */
export class LatestRequestController {
  private revision = 0;

  begin(key: string): RequestTicket {
    const revision = ++this.revision;
    return {
      key,
      isCurrent: () => revision === this.revision
    };
  }

  invalidate(): void {
    this.revision += 1;
  }
}
