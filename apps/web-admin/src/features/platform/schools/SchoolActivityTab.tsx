import { useEffect, useState } from "react";
import { Alert, Empty, Spin, Timeline } from "antd";
import { getPlatformSchoolActivity, type SchoolActivityResponse } from "../../../api/platformSchools";
import { fullDate, numberText } from "./schoolPresentation";

export function SchoolActivityTab({ tenantId }: { tenantId: string }) {
  const [data, setData] = useState<SchoolActivityResponse | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState(false);
  useEffect(() => {
    // 切换学校或卸载后关闭本轮回填，防止旧学校活动在新面板中显示。
    let active = true;
    setLoading(true); setError(false);
    void getPlatformSchoolActivity(tenantId).then((result) => { if (active) setData(result); }).catch(() => { if (active) setError(true); }).finally(() => { if (active) setLoading(false); });
    return () => { active = false; };
  }, [tenantId]);
  if (loading) return <div className="platform-school-panel-loading"><Spin /></div>;
  if (error) return <Alert type="error" showIcon message="活动记录加载失败" />;
  return <div className="platform-school-detail-sections">
    <section><h3>账号安全</h3><dl className="platform-school-facts">
      <div><dt>活跃管理员</dt><dd>{numberText(data?.security.active_admins ?? 0)}</dd></div>
      <div><dt>启用 MFA</dt><dd>{numberText(data?.security.mfa_enabled ?? 0)} / {numberText(data?.security.active_admins ?? 0)}</dd></div>
      <div><dt>活跃会话</dt><dd>{numberText(data?.security.active_sessions ?? 0)}</dd></div>
      <div><dt>最近登录</dt><dd>{fullDate(data?.security.last_login_at)}</dd></div>
    </dl></section>
    <section><h3>业务活动</h3>{data?.activities.length ? <Timeline items={data.activities.map((item) => ({ color: item.severity === "warning" ? "orange" : item.severity === "success" ? "green" : "blue", children: <div className="platform-school-timeline-item"><time>{fullDate(item.happened_at)}</time><strong>{item.title}</strong><span>{item.actor_name ? `${item.actor_name} · ` : ""}{item.summary}</span></div> }))} /> : <Empty description="尚无业务活动" image={Empty.PRESENTED_IMAGE_SIMPLE} />}</section>
    <p className="platform-school-readonly-note">这里展示已投影的关键业务事件；完整安全操作请到平台审计查看。</p>
  </div>;
}
