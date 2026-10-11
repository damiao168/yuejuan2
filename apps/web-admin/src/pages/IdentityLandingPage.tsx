import { Alert, Button, Tag } from "antd";
import { ClipboardList, GraduationCap } from "lucide-react";
import type { SessionUser } from "../auth/session";
import type { ProductExperience } from "../router/experience";

export function IdentityLandingPage({ user, experience, onNavigate }: Readonly<{ user: SessionUser; experience: ProductExperience; onNavigate: (path: string) => void }>) {
  const auditor = experience === "auditor";
  const title = auditor ? "审计工作台" : "学生端";
  const description = auditor
    ? "在授权范围内查询操作记录，核对变更、发布和导出。"
    : "这里提供学校学生端入口；成绩与学习反馈是否可见，以学生端实际发布状态为准。";
  const studentPortalUrl = import.meta.env.VITE_STUDENT_PORTAL_URL;
  return (
    <div className="page-stack identity-workspace">
      <header className="identity-heading">{auditor ? <ClipboardList size={28} /> : <GraduationCap size={28} />}<div><h1>{title}</h1><p>{description}</p></div></header>
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
            <p>使用学校提供的学生端查看本人已发布的成绩和学习反馈。</p>
            {studentPortalUrl ? <Button type="primary" href={studentPortalUrl}>进入学生端</Button> : <Alert type="info" showIcon message="此站点尚未配置学生端网址" description="请使用学校发放的学生端网址；如不清楚入口，请联系学校管理员。" />}
          </div>
        )}
      </section>
    </div>
  );
}
