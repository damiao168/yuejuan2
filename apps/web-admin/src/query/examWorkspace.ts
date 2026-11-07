import { queryOptions, useQuery, type QueryClient } from "@tanstack/react-query";
import { getExamWorkspace } from "../api/workspace";

export const examWorkspaceKeys = {
  all: ["exam-workspace"] as const,
  // 不带 scope 的前缀只供整场失效使用；正式读取还包含当前身份的完整授权边界。
  detail: (examId: string, scopeKey?: string) => scopeKey ? ["exam-workspace", examId, scopeKey] as const : ["exam-workspace", examId] as const
};

export function examWorkspaceQueryOptions(examId: string, scopeKey?: string) {
  return queryOptions({
    queryKey: examWorkspaceKeys.detail(examId, scopeKey),
    queryFn: async () => (await getExamWorkspace(examId)).workspace
  });
}

export function useExamWorkspace(examId: string, scopeKey: string) {
  return useQuery(examWorkspaceQueryOptions(examId, scopeKey));
}

export function invalidateExamWorkspace(queryClient: QueryClient, examId: string) {
  return queryClient.invalidateQueries({ queryKey: examWorkspaceKeys.detail(examId) });
}
