/** Serializes saves per account and task, while allowing different tasks to save independently. */
export class TaskDraftSaveQueue {
  private readonly tails = new Map<string, Promise<void>>();
  private readonly revisions = new Map<string, number>();

  revision(key: string): number {
    return this.revisions.get(key) ?? 0;
  }

  observeRevision(key: string, revision: number): void {
    this.revisions.set(key, Math.max(this.revision(key), revision));
  }

  async whenIdle(key: string): Promise<void> {
    await this.tails.get(key);
  }

  hasPending(key: string): boolean {
    return this.tails.has(key);
  }

  enqueue(key: string, save: (expectedRevision: number) => Promise<number | null>): Promise<number | null> {
    const previous = this.tails.get(key) ?? Promise.resolve();
    const result = previous.then(async () => {
      const revision = await save(this.revision(key));
      if (revision !== null) this.observeRevision(key, revision);
      return revision;
    });
    const tail = result.then(() => undefined, () => undefined);
    // 对调用方保留本次错误，但队尾吞掉拒绝，让同一草稿后续保存仍能继续排队。
    this.tails.set(key, tail);
    void tail.then(() => { if (this.tails.get(key) === tail) this.tails.delete(key); });
    return result;
  }
}
