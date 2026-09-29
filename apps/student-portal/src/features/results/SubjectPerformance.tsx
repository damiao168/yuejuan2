import { useState } from "react";
import type { StudentResult } from "../../api";
import { displaySubject } from "./ResultOverview";

type BalanceItem = NonNullable<StudentResult["subject_balance"]>[number];
type Point = { x: number; y: number };

export function SubjectPerformance({ items }: { items: BalanceItem[] }) {
  const [studentVisible, setStudentVisible] = useState(true);
  const [averageVisible, setAverageVisible] = useState(true);
  const [radarVisible, setRadarVisible] = useState(false);
  if (items.length === 0) return null;
  const orderedItems = [...items].sort((left, right) => subjectOrder(left.subject) - subjectOrder(right.subject));
  return <section className="subject-performance" aria-labelledby="subject-performance-title">
    <header><h2 id="subject-performance-title">学科均衡</h2></header>
    <div className="chart-legend" aria-label="图表图例">
      <button type="button" className={studentVisible ? "active student" : "student"} onClick={() => setStudentVisible((value) => !value)}><i />我的得分率</button>
      <button type="button" className={averageVisible ? "active average" : "average"} onClick={() => setAverageVisible((value) => !value)}><i />学校平均</button>
    </div>
    <div className="subject-chart-grid">
      {orderedItems.length >= 3 ? <button type="button" className="secondary-button" aria-expanded={radarVisible} onClick={() => setRadarVisible((value) => !value)}>{radarVisible ? "收起雷达图" : "展开学科雷达图"}</button> : null}
      {radarVisible && orderedItems.length >= 3 ? <RadarChart items={orderedItems} studentVisible={studentVisible} averageVisible={averageVisible} /> : null}
      <BarChart items={orderedItems} studentVisible={studentVisible} averageVisible={averageVisible} />
    </div>
    <table className="subject-numeric-list"><caption>各科得分率明细</caption><thead><tr><th scope="col">科目</th><th scope="col">我的得分率</th><th scope="col">学校平均</th></tr></thead><tbody>{orderedItems.map((item) => <tr key={item.subject}><th scope="row">{displaySubject(item.subject)}</th><td>{Number.isFinite(item.student_score_rate) ? percent(item.student_score_rate) : "暂无数据"}</td><td>{Number.isFinite(item.school_mean_score_rate) ? percent(item.school_mean_score_rate) : "暂无数据"}</td></tr>)}</tbody></table>
  </section>;
}

function RadarChart({ items, studentVisible, averageVisible }: { items: BalanceItem[]; studentVisible: boolean; averageVisible: boolean }) {
  const width = 560, height = 430, cx = 280, cy = 225, radius = 145;
  const axes = items.map((item, index) => {
    const angle = -Math.PI / 2 + index * Math.PI * 2 / items.length;
    return { item, angle, edge: point(cx, cy, radius, angle), label: point(cx, cy, radius + 36, angle) };
  });
  const polygon = (rate: (item: BalanceItem) => number) => axes.map(({ item, angle }) => {
    const value = clamp(rate(item));
    return point(cx, cy, radius * value, angle);
  });
  const student = polygon((item) => item.student_score_rate);
  const average = polygon((item) => item.school_mean_score_rate);
  return <figure className="subject-chart radar-chart">
    <figcaption><strong>学科均衡</strong><span>各科使用统一得分率刻度，越靠外表现越高。</span></figcaption>
    <svg viewBox={`0 0 ${width} ${height}`} role="img" aria-label="我的各科得分率与学校平均雷达图">
      {[1, .8, .6, .4, .2].map((level, index) => <polygon key={level} points={axes.map(({ angle }) => point(cx, cy, radius * level, angle)).map(pair).join(" ")} className={`radar-ring ring-${index}`} />)}
      {axes.map(({ item, edge, label }) => <g key={item.subject}><line x1={cx} y1={cy} x2={edge.x} y2={edge.y} className="radar-axis" /><text x={label.x} y={label.y} textAnchor={anchor(label.x, cx)} dominantBaseline="middle" className="radar-label">{displaySubject(item.subject)}</text></g>)}
      {averageVisible ? <><polygon points={average.map(pair).join(" ")} className="radar-series average" />{average.map((value, index) => <circle key={index} cx={value.x} cy={value.y} r="4" className="radar-dot average"><title>{displaySubject(items[index].subject)} 学校平均 {percent(items[index].school_mean_score_rate)}</title></circle>)}</> : null}
      {studentVisible ? <><polygon points={student.map(pair).join(" ")} className="radar-series student" />{student.map((value, index) => <g key={index}><circle cx={value.x} cy={value.y} r="4" className="radar-dot student"><title>{displaySubject(items[index].subject)} 我的得分率 {percent(items[index].student_score_rate)}</title></circle><text x={value.x} y={value.y - 11} textAnchor="middle" className="radar-value">{Math.round(items[index].student_score_rate * 100)}</text></g>)}</> : null}
    </svg>
  </figure>;
}

function BarChart({ items, studentVisible, averageVisible }: { items: BalanceItem[]; studentVisible: boolean; averageVisible: boolean }) {
  const width = 660, height = 430, left = 54, right = 20, top = 54, bottom = 62;
  const plotWidth = width - left - right, plotHeight = height - top - bottom;
  const groupWidth = plotWidth / items.length;
  const barWidth = Math.min(28, Math.max(12, groupWidth * .26));
  const bar = (rate: number) => ({ y: top + plotHeight * (1 - clamp(rate)), height: plotHeight * clamp(rate) });
  return <figure className="subject-chart bar-chart">
    <figcaption><strong>学科得分对比</strong><span>逐科比较我的得分率与学校平均。</span></figcaption>
    <svg viewBox={`0 0 ${width} ${height}`} role="img" aria-label="各科得分率分组柱状图">
      {[0, 25, 50, 75, 100].map((tick) => { const y = top + plotHeight * (1 - tick / 100); return <g key={tick}><line x1={left} y1={y} x2={width - right} y2={y} className="bar-grid-line" /><text x={left - 10} y={y} textAnchor="end" dominantBaseline="middle" className="bar-axis-label">{tick}</text></g>; })}
      <line x1={left} y1={top} x2={left} y2={top + plotHeight} className="bar-axis-line" /><line x1={left} y1={top + plotHeight} x2={width - right} y2={top + plotHeight} className="bar-axis-line" />
      {items.map((item, index) => {
        const center = left + groupWidth * (index + .5), student = bar(item.student_score_rate), average = bar(item.school_mean_score_rate);
        return <g key={item.subject}>
          {studentVisible ? <g><rect x={center - barWidth - 2} y={student.y} width={barWidth} height={student.height} rx="3" className="subject-bar student" tabIndex={0} role="img" aria-label={`${displaySubject(item.subject)}，我的得分率 ${percent(item.student_score_rate)}，学校平均 ${percent(item.school_mean_score_rate)}`}><title>{displaySubject(item.subject)} 我的得分率 {percent(item.student_score_rate)}，与学校平均相差 {signedPercent(item.student_score_rate - item.school_mean_score_rate)}</title></rect><text x={center - barWidth / 2 - 2} y={student.y - 8} textAnchor="middle" className="bar-value student">{Math.round(item.student_score_rate * 100)}</text></g> : null}
          {averageVisible ? <g><rect x={center + 2} y={average.y} width={barWidth} height={average.height} rx="3" className="subject-bar average" tabIndex={0} role="img" aria-label={`${displaySubject(item.subject)}，学校平均 ${percent(item.school_mean_score_rate)}，我的得分率 ${percent(item.student_score_rate)}`}><title>{displaySubject(item.subject)} 学校平均 {percent(item.school_mean_score_rate)}，与我的得分率相差 {signedPercent(item.school_mean_score_rate - item.student_score_rate)}</title></rect><text x={center + barWidth / 2 + 2} y={average.y - 8} textAnchor="middle" className="bar-value average">{Math.round(item.school_mean_score_rate * 100)}</text></g> : null}
          <text x={center} y={top + plotHeight + 27} textAnchor="middle" className="bar-subject-label">{displaySubject(item.subject)}</text>
        </g>;
      })}
      <text x={18} y={top - 18} className="bar-unit">%</text>
    </svg>
  </figure>;
}

function point(cx: number, cy: number, radius: number, angle: number): Point { return { x: cx + Math.cos(angle) * radius, y: cy + Math.sin(angle) * radius }; }
function pair(value: Point) { return `${value.x},${value.y}`; }
// 接口得分率使用 0～1；绘图时限制范围，数字明细另行显示缺失数据，避免产生无效 SVG 坐标。
function clamp(value: number) { return Math.max(0, Math.min(1, Number.isFinite(value) ? value : 0)); }
function percent(value: number) { return `${Math.round(clamp(value) * 1000) / 10}%`; }
function signedPercent(value: number) { const amount = Math.round(value * 1000) / 10; return `${amount > 0 ? "+" : ""}${amount}%`; }
function anchor(value: number, center: number) { return Math.abs(value - center) < 8 ? "middle" : value > center ? "start" : "end"; }
function subjectOrder(subject: string) {
  const normalized = subject.trim().toLowerCase();
  const order = ["生物", "biology", "数学", "math", "mathematics", "语文", "chinese", "物理", "physics", "英语", "english", "化学", "chemistry", "历史", "history", "地理", "geography", "政治", "politics"];
  const index = order.indexOf(normalized);
  return index < 0 ? order.length : index;
}
