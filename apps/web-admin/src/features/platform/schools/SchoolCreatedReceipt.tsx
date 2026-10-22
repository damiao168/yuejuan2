import { App, Button, Space } from "antd";
import type { Tenant } from "../../../api/org";
import "./schoolCreatedReceipt.css";

export interface SchoolDelivery {
  tenant: Tenant;
  adminUsername: string;
  adminDisplayName: string;
}

export function schoolLoginURL(code: string, location: Pick<Location, "origin" | "pathname">): string {
  // 交接入口固定在登录页，不能沿用当前平台管理页的路径。
  return `${location.origin}/?tenant=${encodeURIComponent(code)}`;
}

export function SchoolCreatedReceipt({ delivery, onDone }: Readonly<{ delivery: SchoolDelivery; onDone: () => void }>) {
  const { message } = App.useApp();
  const loginURL = schoolLoginURL(delivery.tenant.code, window.location);
  const copy = async () => {
    try {
      // 交接回执只复制入口和账号；初始密码不进入回执、剪贴板或后续页面状态。
      await navigator.clipboard.writeText(`学校：${delivery.tenant.name}\n登录入口：${loginURL}\n学校代码：${delivery.tenant.code}\n管理员：${delivery.adminDisplayName}\n账号：${delivery.adminUsername}\n初始密码请通过约定的安全渠道单独交接。`);
      message.success("登录资料已复制，初始密码请单独交接");
    } catch { message.warning("无法自动复制，请从下方选择并复制登录资料"); }
  };
  return <section className="school-created-receipt" aria-label="学校创建回执">
    <h3>学校已创建，可以交接给学校管理员</h3>
    <dl>
      <div><dt>学校</dt><dd>{delivery.tenant.name}</dd></div>
      <div><dt>学校代码</dt><dd>{delivery.tenant.code}</dd></div>
      <div><dt>管理员</dt><dd>{delivery.adminDisplayName}</dd></div>
      <div><dt>登录账号</dt><dd>{delivery.adminUsername}</dd></div>
      <div><dt>登录入口</dt><dd><a href={loginURL} target="_blank" rel="noopener noreferrer">{loginURL}</a></dd></div>
    </dl>
    <p>请将上述资料交给学校管理员，并通过约定的安全渠道单独告知刚设置的初始密码。</p>
    <p>学校管理员登录后，先建立年级与班级，再导入学生、邀请教师，最后创建考试。</p>
    <Space wrap><Button onClick={() => void copy()}>复制登录资料</Button><Button type="primary" onClick={onDone}>已完成交接</Button></Space>
  </section>;
}
