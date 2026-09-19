import { useState } from "react";
import { App, Button, Form, Input, Space } from "antd";
import { useQueryClient } from "@tanstack/react-query";
import { createTenant, type Tenant } from "../../../api/org";
import { validateNewPassword } from "../../../auth/loginSecurity";
import { onboardingQueryKey } from "../../onboarding/queries";
import { isStepUpCancelledError, useStepUp } from "../../../auth/stepUpContext";

export interface CreateSchoolValues {
  name: string;
  code: string;
  admin_username: string;
  admin_display_name: string;
  admin_password: string;
}

export function CreateSchoolForm({ compact = false, submitLabel = "创建", onCreated, onCancel }: {
  compact?: boolean;
  submitLabel?: string;
  onCreated: (tenant: Tenant) => Promise<void> | void;
  onCancel?: () => void;
}) {
  const { message } = App.useApp();
  const { runWithStepUp } = useStepUp();
  const queryClient = useQueryClient();
  const [form] = Form.useForm<CreateSchoolValues>();
  const [saving, setSaving] = useState(false);

  const submit = async (values: CreateSchoolValues) => {
    setSaving(true);
    try {
      const response = await runWithStepUp({
        reason: `创建学校“${values.name.trim()}”及其首位管理员`,
        description: "此操作将创建完整机构和管理员登录凭据，因此需要验证平台管理员身份。验证后将自动继续。",
        action: () => createTenant({
          name: values.name.trim(), code: values.code.trim().toLowerCase(),
          admin_username: values.admin_username.trim(), admin_display_name: values.admin_display_name.trim(),
          admin_password: values.admin_password
        })
      });
      form.resetFields();
      message.success("学校已创建");
      void queryClient.invalidateQueries({ queryKey: onboardingQueryKey });
      await onCreated(response.tenant);
    } catch (error) {
      if (!isStepUpCancelledError(error)) message.error("学校创建失败，请检查学校代码是否重复");
    } finally {
      setSaving(false);
    }
  };

  return (
    <Form form={form} className={compact ? "create-school-form compact" : "create-school-form"} layout="vertical" requiredMark={false} onFinish={(values) => void submit(values)}>
      <div className={compact ? "create-school-grid" : undefined}>
        <Form.Item name="name" label="学校名称" rules={[{ required: true, message: "请输入学校名称" }]}><Input maxLength={128} autoFocus /></Form.Item>
        <Form.Item name="code" label="学校代码" extra="用于登录，例如 fuzhou-no1" rules={[{ required: true, message: "请输入学校代码" }, { pattern: /^[a-z0-9][a-z0-9-]{1,62}[a-z0-9]$/, message: "使用 3–64 位小写字母、数字或连字符" }]}><Input maxLength={64} /></Form.Item>
        <Form.Item name="admin_display_name" label="管理员姓名" rules={[{ required: true, message: "请输入管理员姓名" }]}><Input maxLength={128} /></Form.Item>
        <Form.Item name="admin_username" label="管理员账号" rules={[{ required: true, message: "请输入管理员账号" }]}><Input maxLength={64} autoComplete="off" /></Form.Item>
        <Form.Item name="admin_password" label="初始密码" extra="建议使用至少 15 个字符的长密码短语" rules={[{ required: true, message: "请输入初始密码" }, { validator: validateNewPassword }]}><Input.Password autoComplete="new-password" /></Form.Item>
      </div>
      <Space>{onCancel ? <Button onClick={onCancel}>取消</Button> : null}<Button type="primary" htmlType="submit" loading={saving}>{submitLabel}</Button></Space>
    </Form>
  );
}
