import { QueryClient } from "@tanstack/react-query";
import { ApiClientError } from "../api/client";

export function shouldRetryQuery(failureCount: number, error: unknown): boolean {
  if (error instanceof ApiClientError && error.status < 500) {
    return false;
  }
  return failureCount < 2;
}

export function createAppQueryClient(): QueryClient {
  return new QueryClient({
    defaultOptions: {
      queries: {
        staleTime: 30_000,
        gcTime: 5 * 60_000,
        retry: shouldRetryQuery,
        refetchOnWindowFocus: false
      },
      // 写操作不自动重试，避免响应丢失时重复产生业务变更。
      mutations: {
        retry: false
      }
    }
  });
}

export const appQueryClient = createAppQueryClient();
