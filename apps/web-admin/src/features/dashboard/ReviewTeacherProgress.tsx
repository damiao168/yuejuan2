import { useMemo, useState } from "react";
import { Button, Segmented, Select } from "antd";
import type { DashboardReviewTeacher, DashboardReviewTeacherSummary } from "../../api/dashboard";
import { getSafeUserText } from "../../api/client";
import { displayNameOrUsername } from "../../auth/session";
import { normalizeTeachingSubjects, teachingSubjectOptions, teachingSubjectsLabel } from "../../constants/teachingSubjects";
import "./review-teacher-progress.css";

// 已交和剩余都为 0 的教师默认隐藏，避免全校名单淹没正在阅卷的人；任务已全部交完的教师仍显示。
function hasNoTasks(teacher: DashboardReviewTeacher) {
  return teacher.submitted_count + teacher.remaining_count === 0;
}

function emptyText(filteredCount: number, onlineOnly: boolean) {
  if (filteredCount) return "当前筛选下的教师都没有阅卷任务";
  return onlineOnly ? "当前筛选下暂无在线阅卷教师" : "当前筛选下暂无阅卷教师";
}

/** 工作台“阅卷进度”区块内的按老师小节，只对学校管理员渲染。 */
export function ReviewTeacherProgress({ teachers, summary }: Readonly<{
  teachers: DashboardReviewTeacher[];
  summary: DashboardReviewTeacherSummary;
}>) {
  const [onlineOnly, setOnlineOnly] = useState(false);
  const [subject, setSubject] = useState("");
  const [showIdle, setShowIdle] = useState(false);
  const filtered = useMemo(() => teachers.filter(teacher => (!onlineOnly || teacher.is_online)
    && (!subject || normalizeTeachingSubjects(teacher.teaching_subjects).includes(subject))), [onlineOnly, subject, teachers]);
  // 按钮上的人数跟随当前学科和在线筛选，点开后多出的行数与按钮一致。
  const idleCount = filtered.filter(hasNoTasks).length;
  // 剩余多的在前，先看到卡住的人；同数时在线优先，再按用户名排，保证 30 秒刷新后顺序不变。
  const visible = useMemo(() => filtered.filter(teacher => showIdle || !hasNoTasks(teacher))
    .sort((left, right) => right.remaining_count - left.remaining_count || Number(right.is_online) - Number(left.is_online) || left.username.localeCompare(right.username)), [filtered, showIdle]);
  return <section className="dashboard-subsection review-teacher-progress" aria-labelledby="review-teacher-progress-title">
    <div className="school-dashboard-section-head">
      <div className="school-dashboard-section-title"><div><h3 id="review-teacher-progress-title">按老师</h3><p>本校 {summary.teacher_count} 人 · 在线 {summary.online_count} 人</p></div></div>
      <div className="review-teacher-filters">
        <Select aria-label="按任教学科查看进度" allowClear value={subject || undefined} placeholder="全部学科" options={teachingSubjectOptions} onChange={value => setSubject(value || "")} />
        <Segmented aria-label="教师在线筛选" value={onlineOnly ? "online" : "all"} options={[{ label: "本校全部", value: "all" }, { label: "只看在线", value: "online" }]} onChange={value => setOnlineOnly(value === "online")} />
        {idleCount ? <Button type="link" className="review-teacher-idle-toggle" onClick={() => setShowIdle(value => !value)}>{showIdle ? "隐藏无任务教师" : `显示无任务教师（${idleCount}）`}</Button> : null}
      </div>
    </div>
    <p className="review-teacher-online-note">近 {Math.max(1, Math.ceil(summary.online_window_seconds / 60))} 分钟有活动且会话有效视为在线；当前页面每 30 秒更新。</p>
    {visible.length ? <div className="dashboard-table-wrap review-teacher-table-wrap"><table className="dashboard-operation-table review-teacher-table">
      <thead><tr><th>教师</th><th>任教学科</th><th>在线状态</th><th>已交 / 总任务</th><th className="number-cell">待阅</th><th className="number-cell">进行中</th></tr></thead>
      <tbody>{visible.map(teacher => {
        // remaining 已含进行中与退回；总数只加已交，不能再把 assigned 重复累计。
        const total = teacher.submitted_count + teacher.remaining_count;
        const percent = total ? Math.round(teacher.submitted_count / total * 100) : 0;
        const pending = Math.max(0, teacher.remaining_count - teacher.in_progress_count);
        const name = displayNameOrUsername(teacher.display_name, teacher.username);
        return <tr key={teacher.user_id}>
          <td><strong>{name}</strong>{name !== teacher.username ? <small className="review-teacher-username">{teacher.username}</small> : null}</td>
          <td>{teachingSubjectsLabel(teacher.teaching_subjects)}</td>
          <td><span className={`review-teacher-online ${teacher.is_online ? "is-online" : ""}`}>{teacher.is_online ? "在线" : "离线"}</span></td>
          <td>{total ? <div className="review-teacher-completion"><span>{teacher.submitted_count} / {total}<small>{percent}%</small></span><progress value={teacher.submitted_count} max={total} aria-label={`${name}阅卷完成进度`} /></div> : <span className="muted">暂无任务</span>}</td>
          <td className="number-cell" title={teacher.returned_count ? `含退回待阅 ${teacher.returned_count} 项` : undefined}>{pending}</td>
          <td className="number-cell">{teacher.in_progress_count}</td>
        </tr>;
      })}</tbody>
    </table></div> : <p className="review-teacher-empty">{emptyText(filtered.length, onlineOnly)}</p>}
    <details className="review-teacher-counting-note"><summary>统计说明</summary><p>{getSafeUserText(summary.task_counting_note, "按教师的小题阅卷任务计数；双评时每位教师各计一项任务。")}</p><p>{getSafeUserText(summary.online_note, "仅统计近 5 分钟有活动的有效登录会话。")}</p></details>
  </section>;
}
