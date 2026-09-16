import type { BackmarkBatchListResponse } from "@edugrade/sdk";

export const emptyBackmarkBatchPage = {
  backmark_batches: [], next_cursor: "", has_more: false
} satisfies BackmarkBatchListResponse;
