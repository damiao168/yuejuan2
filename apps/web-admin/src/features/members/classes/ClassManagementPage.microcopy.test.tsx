// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import type { ReactNode } from "react";
import type { SessionUser } from "../../../auth/session";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ClassManagementPage } from "./ClassManagementPage";

const mocks = vi.hoisted(() => ({ updateClass: vi.fn() }));
vi.mock("../../../api/org", () => ({
  listSchools: async () => ({ schools: [{ id: "school-a", name: "合成学校", status: "active" }] }),
  listAcademicYears: async () => ({ academic_years: [{ school_id: "school-a", is_current: true, start_year: 2026 }] }),
  listGrades: async () => ({ grades: [{ id: "grade-a", school_id: "school-a", name: "高一", academic_year: "2026-2027", education_stage: "senior", level_no: 10, status: "active" }] }),
  listClasses: async () => ({ classes: [{ id: "class-a", school_id: "school-a", grade_id: "grade-a", name: "一班", code: "G10-01", status: "active" }] }),
  listStudents: async () => ({ students: [], has_more: false }),
  createClass: vi.fn(), createGrade: vi.fn(), updateGrade: vi.fn(), updateClass: mocks.updateClass, deleteClass: vi.fn(), deleteGrade: vi.fn()
}));
vi.mock("antd", async (importOriginal) => ({
  ...await importOriginal<typeof import("antd")>(),
  App: { useApp: () => ({ message: { success: vi.fn(), error: vi.fn() } }) },
  // 保留真实 Form/useWatch/Input，仅移除与字段状态无关的门户动画和滚动条测量。
  Modal: ({ open, title, children }: { open?: boolean; title?: ReactNode; children?: ReactNode }) => open ? <section role="dialog" aria-label={String(title)}>{children}</section> : null
}));

beforeEach(() => {
  vi.clearAllMocks();
  vi.stubGlobal("matchMedia", vi.fn(() => ({ matches: false, addListener: vi.fn(), removeListener: vi.fn(), addEventListener: vi.fn(), removeEventListener: vi.fn() })));
});
afterEach(() => {
  // Vitest 未启用全局钩子。先卸载表单，让它在 JSDOM 销毁前取消待执行的 debounce。
  cleanup();
  vi.unstubAllGlobals();
});

describe("班级代码变更提示", () => {
  it("只有编辑值改变时提示后续导入需同步，改回原代码后不再提示", async () => {
    const currentUser = { id: "admin", tenant: "tenant", username: "admin", displayName: "合成管理员", name: "合成管理员", role: "school_admin", school: "合成学校", currentExam: "", permissions: ["org:manage"], publicComputer: false, roles: ["school_admin"], organizationScope: { resolved: true, tenantWide: false, schoolIds: ["school-a"], gradeIds: [], classIds: [] } } as SessionUser;
    render(<ClassManagementPage currentUser={currentUser} />);
    fireEvent.click(await screen.findByRole("button", { name: "编辑" }));
    const dialog = await screen.findByRole("dialog", { name: "编辑班级" });
    const code = within(dialog).getByRole("textbox", { name: "班级代码" });
    expect((code as HTMLInputElement).value).toBe("G10-01");
    expect(within(dialog).queryByText("班级代码已改，后续学生导入文件请使用新代码。")).toBeNull();
    fireEvent.change(code, { target: { value: "G10-02" } });
    await within(dialog).findByText("班级代码已改，后续学生导入文件请使用新代码。");
    fireEvent.change(code, { target: { value: "G10-01" } });
    await waitFor(() => expect(within(dialog).queryByText("班级代码已改，后续学生导入文件请使用新代码。")).toBeNull());
    expect(mocks.updateClass).not.toHaveBeenCalled();
  });
});
