// 已发布考试列表独立维护刷新代次，返回页面时重新检查当前发布。
import { useCallback, useEffect, useRef, useState } from "react";
import { listPublishedExams, type PublishedExam } from "../../api";
import { displaySubject } from "./ResultOverview";
import { friendlyError, sessionExpired } from "../../errors";
import { formatDate, formatDateTime, formatScore } from "../../formatting";
import { ErrorNotice } from "../../components/PortalFeedback";

export function ExamList({ userName, onOpen }: Readonly<{ userName: string; onOpen: (examID: string) => void }>) {
  const [items, setItems] = useState<PublishedExam[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [loginRequired, setLoginRequired] = useState(false);

  const [checkedAt, setCheckedAt] = useState<string>();
  const requestRef = useRef(0);

  const load = useCallback(async () => {
    const requestID = ++requestRef.current;
    setLoading(true);
    setError("");
    setLoginRequired(false);
    try {
      const response = await listPublishedExams();
      if (requestID !== requestRef.current) return;
      setItems([...response.exams].sort((a, b) => (Date.parse(b.published_at ?? "") || 0) - (Date.parse(a.published_at ?? "") || 0)));
      setCheckedAt(new Date().toISOString());
    } catch (reason) {
      if (requestID !== requestRef.current) return;
      setError(friendlyError(reason));
      setLoginRequired(sessionExpired(reason));
    } finally {
      if (requestID === requestRef.current) setLoading(false);
    }
  }, []);
  useEffect(() => {
    void load();
    // 回到标签页时重新查询学校的当前发布，页面不承诺在后台持续实时更新。
    const onFocus = () => { if (document.visibilityState !== "hidden") void load(); };
    window.addEventListener("focus", onFocus);
    return () => { requestRef.current += 1; window.removeEventListener("focus", onFocus); };
  }, [load]);

  const latest = items[0];
  const latestRate = latest?.max_score ? Math.round((latest.total_score ?? 0) / latest.max_score * 1000) / 10 : undefined;
  return (
    <section className="exam-list-page" aria-labelledby="exam-list-title">
      <div className="page-heading">
        <div><p className="eyebrow">学习概览</p><h1 id="exam-list-title">{greeting(userName)}</h1><p>从最近发布的考试开始复盘。</p></div>
        <button type="button" className="secondary-button" onClick={() => void load()} disabled={loading}>刷新</button>
      </div>
      {checkedAt ? <p className="muted">最近检查：{formatDateTime(checkedAt)}。回到此页面或点击刷新时会检查新成绩。</p> : null}
      {error ? <ErrorNotice message={error} onRetry={load} loginRequired={loginRequired} /> : null}
      {loading ? <div className="inline-status">正在加载已发布考试…</div> : null}
      {!loading && !error && items.length === 0 ? <EmptyState /> : null}
      {!loading && !error && latest ? <section className="latest-release" aria-labelledby="latest-release-title">
        <div><p>最近发布 · {displaySubject(latest.subject)}</p><h2 id="latest-release-title">{latest.name}</h2><span>{formatDate(latest.published_at)} · 第 {latest.release_version} 版</span></div>
        {latest.total_score !== undefined && latest.max_score ? <div className="latest-score"><strong>{formatScore(latest.total_score)}<small> / {formatScore(latest.max_score)}</small></strong><span>得分率 {latestRate}%</span></div> : null}
        <button type="button" className="primary-action" onClick={() => onOpen(latest.exam_id)}>查看考试分析 <span>→</span></button>
      </section> : null}
      {!loading && !error && items.length > 0 ? <section className="published-exams"><div className="section-heading"><div><p className="section-kicker">我的考试</p><h2>已发布成绩</h2></div><span>{items.length} 场考试</span></div><div className="exam-list">
        {items.map((exam) => <button className="exam-row" type="button" key={exam.exam_id} onClick={() => onOpen(exam.exam_id)}>
          <div><strong>{exam.name}</strong><span>{displaySubject(exam.subject)} · 已发布 {formatDate(exam.published_at)}</span></div>
          <div className="exam-row-action"><small>第 {exam.release_version} 版</small><span>查看成绩 →</span></div>
        </button>)}
      </div></section> : null}
    </section>
  );
}

function greeting(name: string) {
  const hour = new Date().getHours();
  if (hour < 6) return `${name}，夜深了`;
  if (hour < 11) return `${name}，早上好`;
  if (hour < 14) return `${name}，中午好`;
  if (hour < 18) return `${name}，下午好`;
  return `${name}，晚上好`;
}

function EmptyState() {
  return <div className="empty-state">
    <p className="empty-state-kicker">当前状态</p>
    <h2>学校尚未发布你的成绩</h2>
    <p>学校正式发布成绩后，回到此页面或点击刷新即可检查新成绩，无需重复提交资料。</p>
    <ul>
      <li>如果老师还未通知发布，请等待学校完成阅卷与成绩确认。</li>
      <li>如果已经收到发布通知但仍看不到，请联系学校管理员核对学号绑定。</li>
    </ul>
  </div>;
}
