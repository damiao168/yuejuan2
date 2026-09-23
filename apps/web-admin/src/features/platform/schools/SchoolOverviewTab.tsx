import type { PlatformSchoolSummary } from "../../../api/platformSchools";
import { fullDate, numberText, relativeTime, tokenText } from "./schoolPresentation";

export function SchoolOverviewTab({ school, focus }: { school: PlatformSchoolSummary; focus: "created" | "status" | null }) {
  return <div className="platform-school-detail-sections">
    <section><h3>基本信息</h3><dl className="platform-school-facts">
      <div><dt>学校名称</dt><dd>{school.name}</dd></div>
      <div><dt>学校代码</dt><dd className="mono">{school.code}</dd></div>
      <div className={focus === "created" ? "platform-school-fact-focused" : undefined}><dt>创建时间</dt><dd>{fullDate(school.created_at)}</dd></div>
      <div className={focus === "status" ? "platform-school-fact-focused" : undefined}><dt>当前状态</dt><dd>{school.status === "active" ? "使用中" : "已停用"}</dd></div>
      <div><dt>最近业务活动</dt><dd>{relativeTime(school.last_activity_at)}</dd></div>
    </dl></section>
    <section><h3>成员规模</h3><dl className="platform-school-facts">
      <div><dt>管理员</dt><dd>{numberText(school.members.administrators)}</dd></div>
      <div><dt>教师</dt><dd>{numberText(school.members.teachers)}</dd></div>
      <div><dt>阅卷员</dt><dd>{numberText(school.members.graders)}</dd></div>
      <div><dt>学生</dt><dd>{numberText(school.members.students)}</dd></div>
      <div><dt>班级</dt><dd>{numberText(school.members.classes)}</dd></div>
    </dl></section>
    <section><h3>近 {school.usage.window_days} 天</h3><dl className="platform-school-facts">
      <div><dt>考试</dt><dd>{numberText(school.exam_count)}</dd></div>
      <div><dt>AI 请求</dt><dd>{numberText(school.usage.requests)}</dd></div>
      <div><dt>Token</dt><dd>{tokenText(school.usage.total_tokens)}</dd></div>
      <div><dt>仲裁调用</dt><dd>{numberText(school.usage.arbitration_requests)}</dd></div>
    </dl></section>
    {school.attention_reasons?.length ? <section className="platform-school-attention"><h3>需要关注</h3><ul>{school.attention_reasons.map((reason) => <li key={reason}>{reason}</li>)}</ul></section> : null}
  </div>;
}
