// @vitest-environment jsdom
import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { Form, type FormInstance } from "antd";
import { afterEach, expect, it, vi } from "vitest";
import { MemberCreationFields } from "./TeacherManagementViews";
import type { School } from "../../../api/org";

afterEach(cleanup);
it("真实教师表单要求初始密码及一致确认，管理员邀请不出现密码字段", async () => {
  window.matchMedia = vi.fn().mockReturnValue({ matches: false, addListener: vi.fn(), removeListener: vi.fn() });
  const submit = vi.fn();
  const invalid = vi.fn();
  let form!: FormInstance;
  const school = { id: "school", name: "测试学校", code: "school-code" } as School;
  function Harness({ mode }: { mode: "teacher" | "administrator" }) {
    [form] = Form.useForm();
    return <Form form={form} onFinish={submit} onFinishFailed={invalid} initialValues={{ username: "teacher", display_name: "教师", school_id: "school", teaching_subjects: ["mathematics"] }}><MemberCreationFields schools={[school]} mode={mode} /></Form>;
  }
  const view = render(<Harness mode="teacher" />);
  expect(screen.getByLabelText("初始密码")).toBeDefined();
  await act(async () => { form.submit(); });
  await waitFor(() => expect(invalid).toHaveBeenCalled());
  expect(invalid.mock.calls[0][0].errorFields).toEqual(expect.arrayContaining([expect.objectContaining({ name: ["password"] }), expect.objectContaining({ name: ["password_confirm"] })]));
  expect(submit).not.toHaveBeenCalled();
  fireEvent.change(screen.getByLabelText("初始密码"), { target: { value: "InitialPass123!" } });
  fireEvent.change(screen.getByLabelText("确认初始密码"), { target: { value: "other" } });
  await act(async () => { form.submit(); });
  await screen.findByText("两次输入的密码不一致"); expect(submit).not.toHaveBeenCalled();
  fireEvent.change(screen.getByLabelText("确认初始密码"), { target: { value: "InitialPass123!" } });
  await act(async () => { form.submit(); });
  await waitFor(() => expect(submit).toHaveBeenCalled());
  view.unmount(); render(<Harness mode="administrator" />);
  expect(screen.queryByLabelText("初始密码")).toBeNull(); expect(screen.queryByLabelText("任教学科")).toBeNull();
});
