import { useRef, useState } from "react";
import { Alert, App, Button, Form, Input, Modal, Space } from "antd";
import { ApiClientError, getUserErrorMessage } from "../../../api/client";
import { updateManagedUserProfile, type ManagedUser } from "../../../api/users";
import { isStepUpCancelledError, useStepUp } from "../../../auth/stepUpContext";

interface MemberProfileValues {
  display_name: string;
  employee_no?: string;
  phone?: string;
}

export function MemberProfileDialog({ user, onCancel, onSaved }: Readonly<{
  user: ManagedUser;
  onCancel: () => void;
  onSaved: (user: ManagedUser) => void;
}>) {
  const { message } = App.useApp();
  const { runWithStepUp } = useStepUp();
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");
  const savingRef = useRef(false);

  async function save(values: MemberProfileValues) {
    if (savingRef.current) return;
    const displayName = values.display_name.trim();
    const employeeNo = values.employee_no?.trim() ?? "";
    const phone = values.phone?.trim();
    if (displayName === user.display_name && employeeNo === (user.employee_no ?? "") && !phone) {
      message.info("资料未修改");
      return;
    }
    savingRef.current = true;
    setSaving(true);
    setError("");
    try {
      // 带上打开弹窗时的资料，发生并发修改后保留输入，不自动覆盖另一位管理员的结果。
      const result = await runWithStepUp({
        reason: `修改“${user.display_name}”的人员资料`,
        description: "保存姓名、教职工号或新手机号。只有登录身份发生变化时，该人员才需要重新登录。",
        action: () => updateManagedUserProfile(user.id, {
          display_name: displayName,
          employee_no: employeeNo,
          ...(phone ? { phone } : {}),
          expected_display_name: user.display_name,
          expected_employee_no: user.employee_no ?? "",
          expected_phone_masked: user.phone_masked ?? ""
        })
      });
      onSaved(result.user);
      if (!result.login_identity_changed) message.success("人员资料已保存");
      else if (result.user.status === "invited") message.success("资料已保存，请点击“重新生成邀请”，原邀请已失效");
      else message.success("资料已保存，登录信息已变更，请通知该人员重新登录");
    } catch (reason) {
      if (!isStepUpCancelledError(reason)) {
        setError(reason instanceof ApiClientError && reason.code === "managed_user_profile_conflict"
          ? "人员资料已被更新，当前输入已保留。请关闭后刷新名单，再重新核对修改。"
          : getUserErrorMessage(reason, "人员资料暂时无法保存，当前输入已保留。"));
      }
    } finally {
      savingRef.current = false;
      setSaving(false);
    }
  }

  return <Modal title={`编辑资料 · ${user.display_name}`} open footer={null} destroyOnHidden maskClosable={!saving} closable={!saving} keyboard={!saving} onCancel={() => { if (!savingRef.current) onCancel(); }}>
    <Form<MemberProfileValues> layout="vertical" disabled={saving} initialValues={{ display_name: user.display_name, employee_no: user.employee_no ?? "", phone: "" }} onFinish={(values) => void save(values)}>
      {error ? <Alert type="error" showIcon message={error} /> : null}
      <Form.Item label="用户名"><Input value={user.username} readOnly /></Form.Item>
      <Form.Item name="display_name" label="姓名" rules={[{ required: true, whitespace: true, message: "请输入姓名" }, { max: 80, message: "姓名最多 80 个字符" }]}><Input maxLength={80} autoComplete="off" /></Form.Item>
      <Form.Item name="employee_no" label="教职工号（可选）" extra="留空可清除原教职工号。" rules={[{ max: 64, message: "教职工号最多 64 个字符" }]}><Input maxLength={64} autoComplete="off" /></Form.Item>
      <Form.Item label="当前手机号"><Input value={user.phone_masked || "未设置"} disabled /></Form.Item>
      <Form.Item name="phone" label="新手机号（可选）" extra="留空保留当前手机号；填写后替换为新号码。" rules={[{ pattern: /^(?:\+?86)?1\d{10}$/, message: "请输入有效的中国大陆手机号" }]}><Input inputMode="tel" autoComplete="off" maxLength={14} placeholder="不修改请留空" /></Form.Item>
      <Space><Button type="primary" htmlType="submit" loading={saving}>保存资料</Button><Button disabled={saving} onClick={onCancel}>取消</Button></Space>
    </Form>
  </Modal>;
}
