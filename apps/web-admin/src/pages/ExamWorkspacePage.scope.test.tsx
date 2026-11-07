// @vitest-environment jsdom
import { act, cleanup, render, screen } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, expect, it, vi } from "vitest";
import { ApiClientError } from "../api/client";
import { sessionAuthorizationKey, type SessionUser } from "../auth/session";
import { examWorkspaceKeys } from "../query/examWorkspace";
import { ExamWorkspacePage } from "./ExamWorkspacePage";

const api = vi.hoisted(() => ({ workspace: vi.fn() }));
vi.mock("../api/workspace", () => ({ getExamWorkspace: api.workspace }));
vi.mock("../features/exams/workspace/ExamWorkspaceLayout", () => ({ ExamWorkspaceLayout: ({ moduleContent }: { moduleContent: React.ReactNode }) => <div>{moduleContent}</div> }));
vi.mock("../components/PageState", () => ({ LoadingState: () => <p>加载授权考试</p>, ErrorState: () => <p>无权读取考试</p> }));
const user: SessionUser = { tenant: "tenant", id: "admin", username: "school-admin", displayName: "学校管理员", name: "学校管理员", role: "school_admin", school: "本校", currentExam: "未选择考试", roles: ["school_admin"], permissions: ["exam:manage"], organizationScope: {
  resolved: true, tenantWide: false, schoolIds: ["local", "foreign"], gradeIds: [], classIds: []
}, publicComputer: false };
afterEach(() => { cleanup(); vi.clearAllMocks(); });

it("同账号学校范围收紧后，不复用外校考试缓存，也不挂载业务模块", async () => {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: 30_000 } } });
  client.setQueryData(examWorkspaceKeys.detail("foreign-exam", sessionAuthorizationKey(user)), { exam_status: "grading" });
  let reject!: (reason: unknown) => void;
  api.workspace.mockReturnValue(new Promise((_resolve, fail) => { reject = fail; }));
  const module = vi.fn(() => <p>外校成绩模块</p>);
  const page = (currentUser: SessionUser) => <QueryClientProvider client={client}><ExamWorkspacePage examId="foreign-exam" section="scores" experience="admin" currentUser={currentUser} moduleContent={module} onNavigate={vi.fn()} /></QueryClientProvider>;
  const view = render(page(user));
  expect(screen.getByText("外校成绩模块")).toBeTruthy();
  const localUser = { ...user, organizationScope: { ...user.organizationScope, schoolIds: ["local"] } };
  view.rerender(page(localUser));
  expect(screen.queryByText("外校成绩模块")).toBeNull();
  expect(module).toHaveBeenCalledTimes(1);
  await act(async () => { reject(new ApiClientError(403, "resource_scope_forbidden", "forbidden")); });
  await screen.findByText("无权读取考试");
  expect(module).toHaveBeenCalledTimes(1);
  client.clear();
});

it("重新读取被拒绝时，已有缓存不能继续展示或挂载业务模块", async () => {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: 30_000 } } });
  const key = examWorkspaceKeys.detail("exam", sessionAuthorizationKey(user));
  client.setQueryData(key, { exam_status: "grading" });
  api.workspace.mockRejectedValue(new ApiClientError(403, "resource_scope_forbidden", "forbidden"));
  render(<QueryClientProvider client={client}><ExamWorkspacePage examId="exam" section="scores" experience="admin" currentUser={user} moduleContent={<p>旧成绩模块</p>} onNavigate={vi.fn()} /></QueryClientProvider>);
  expect(screen.getByText("旧成绩模块")).toBeTruthy();
  await act(async () => { await client.invalidateQueries({ queryKey: key }); });
  await screen.findByText("无权读取考试");
  expect(screen.queryByText("旧成绩模块")).toBeNull();
  client.clear();
});
