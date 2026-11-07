// @vitest-environment jsdom
import type { ReactNode } from "react";
import { act, cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { ManagedUser } from "../../../api/users";
import { MemberProfileDialog } from "./MemberProfileDialog";

const mocks = vi.hoisted(() => ({
  update: vi.fn(), info: vi.fn(), success: vi.fn(), saved: vi.fn(), values: {} as Record<string, unknown>
}));
vi.mock("../../../api/users", () => ({ updateManagedUserProfile: mocks.update }));
vi.mock("antd", () => ({
  App: { useApp: () => ({ message: { info: mocks.info, success: mocks.success } }) },
  Form: Object.assign(({ initialValues, onFinish, children }: { initialValues: Record<string, unknown>; onFinish: (values: Record<string, unknown>) => void; children: ReactNode }) => <form onSubmit={event => { event.preventDefault(); onFinish({ ...initialValues, ...mocks.values }); }}>{children}</form>, {
    Item: ({ label, children }: { label: string; children: ReactNode }) => <label>{label}{children}</label>
  }),
  Modal: ({ children }: { children: ReactNode }) => <div role="dialog">{children}</div>,
  Input: ({ value }: { value?: string }) => <input readOnly value={value || ""} />,
  Select: () => <select multiple />,
  Space: ({ children }: { children: ReactNode }) => <div>{children}</div>,
  Button: ({ children, htmlType }: { children: ReactNode; htmlType?: "submit" | "button" }) => <button type={htmlType || "button"}>{children}</button>,
  Alert: ({ message }: { message: string }) => <div role="alert">{message}</div>
}));

const teacher: ManagedUser = { id: "teacher", username: "teacher", display_name: "王老师", employee_no: "T01", phone_masked: "138****0000", status: "active", roles: ["grader"], teaching_subjects: ["mathematics"] };
beforeEach(() => {
  vi.clearAllMocks(); mocks.values = {};
  mocks.update.mockResolvedValue({ user: teacher, login_identity_changed: false });
});
afterEach(cleanup);

describe("人员资料中的任教学科", () => {
  async function save(user = teacher) {
    render(<MemberProfileDialog user={user} onCancel={vi.fn()} onSaved={mocks.saved} />);
    await act(async () => { fireEvent.submit(screen.getByRole("button", { name: "保存资料" }).closest("form")!); });
  }
  it.each([
    { display_name: "新姓名" },
    { employee_no: "T02" },
    { phone: "13900000001" }
  ])("修改资料直接保存，无需密码验证上下文：%o", async values => {
    mocks.values = values;
    await save();
    expect(mocks.update).toHaveBeenCalledWith("teacher", expect.objectContaining(values));
    expect(mocks.saved).toHaveBeenCalledWith(teacher);
    expect(screen.queryByText("确认你的身份")).toBeNull();
  });
  it("仅修改学科也保存，携带打开资料时的学科防止覆盖并发修改", async () => {
    mocks.values = { teaching_subjects: ["physics", "math"] };
    await save();
    expect(screen.getByText("任教学科")).toBeTruthy();
    expect(mocks.update).toHaveBeenCalledWith("teacher", expect.objectContaining({ teaching_subjects: ["mathematics", "physics"], expected_teaching_subjects: ["mathematics"] }));
    expect(mocks.saved).toHaveBeenCalled();
    expect(mocks.info).not.toHaveBeenCalled();
    expect(mocks.success).toHaveBeenCalledWith("资料已保存，登录信息或阅卷资格已变更，请通知该人员重新登录");
  });
  it("待激活教师只改学科也提示重新生成已经失效的邀请", async () => {
    mocks.values = { teaching_subjects: ["physics"] };
    mocks.update.mockResolvedValue({ user: { ...teacher, status: "invited" }, login_identity_changed: false });
    await save({ ...teacher, status: "invited" });
    expect(mocks.success).toHaveBeenCalledWith("资料已保存，请点击“重新生成邀请”，原邀请已失效");
  });
  it("别名或顺序变化不被误认为学科修改", async () => {
    mocks.values = { teaching_subjects: [" MATH "] };
    await save();
    expect(mocks.update).not.toHaveBeenCalled();
    expect(mocks.info).toHaveBeenCalledWith("资料未修改");
  });
  it.each(["school_admin", "tenant_admin", "platform_admin"])("%s兼任教师也不展示或提交学科字段", async role => {
    mocks.values = { display_name: "新姓名", teaching_subjects: ["english"] };
    await save({ ...teacher, roles: [role, "grader"] });
    expect(screen.queryByText("任教学科")).toBeNull();
    const payload = mocks.update.mock.calls[0][1];
    expect(payload).not.toHaveProperty("teaching_subjects");
    expect(payload).not.toHaveProperty("expected_teaching_subjects");
    expect(payload).not.toHaveProperty("class_ids");
  });
});
