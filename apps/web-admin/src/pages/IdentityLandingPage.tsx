import { Alert, Button, Tag } from "antd";
import { ClipboardList, GraduationCap } from "lucide-react";
import type { SessionUser } from "../auth/session";
import type { ProductExperience } from "../router/experience";

export function IdentityLandingPage({ user, experience, onNavigate }: Readonly<{ user: SessionUser; experience: ProductExperience; onNavigate: (path: string) => void }>) {
  const auditor = experience === "auditor";
  const title = auditor ? "审计工作台" : "学生端";
  const description = auditor
    ? "在授权范围内查询操作记录，核对变更、发布和导出。"
    : "";
  const studentPortalUrl = import.meta.env.VITE_STUDENT_PORTAL_URL;
  let studentDestination = "";
  if (studentPortalUrl) {
    try {
      const target = new URL(studentPortalUrl, window.location.origin);
      // 只带学校定位，不复制认证凭据；学生端继续按自己的同源 Cookie 和登录流程核实身份。
      if (["https:", "http:"].includes(target.protocol)) { target.searchParams.set("school", user.tenant); studentDestination = target.href; }
    } catch { /* 无效的部署地址按未配置处理，避免学生进入错误入口。 */ }
  }
  return (
    <div className="page-stack identity-workspace">
      <header className="identity-heading">{auditor ? <ClipboardList size={28} /> : <GraduationCap size={28} />}<div><h1>{title}</h1>{description ? <p>{description}</p> : null}</div></header>
      <section className="identity-content">
        <div className="identity-account">
          <Tag color="blue">{user.displayName || user.username}</Tag>
          <span>{user.tenant}{user.school && user.school !== user.tenant ? ` · ${user.school}` : ""}</span>
        </div>
        {auditor ? (
          <div className="identity-start">
            <h2>从操作记录开始</h2>
            <p>可按考试、操作人和时间筛选，再打开单条记录查看变更详情。导出遵循当前账号权限。</p>
            <Button type="primary" onClick={() => onNavigate("/audit")}>查询操作记录</Button>
          </div>
        ) : (
          <div className="identity-start">
            <h2>前往学校学生端</h2>
            {studentDestination ? <><p>进入学生端，查看已发布成绩和学习反馈。</p><Button type="primary" href={studentDestination}>进入学生端</Button></> : <Alert type="info" showIcon message="请使用学校发放的学生端网址，或联系学校管理员获取。" />}
          </div>
        )}
      </section>
    </div>
  );
}
