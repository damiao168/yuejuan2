export interface RequestTicket {
  readonly key: string;
  isCurrent(): boolean;
}

/**
 * Coordinates async projections that are scoped to a selected resource. A
 * response may update React state only while its ticket is current.
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
