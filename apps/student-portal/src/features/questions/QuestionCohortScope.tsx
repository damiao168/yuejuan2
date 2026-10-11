import type { StudentQuestion } from "../../api";

export function QuestionCohortScope({ question }: Readonly<{ question: StudentQuestion }>) {
  if (!question.cohort) return null;
  const count = question.cohort.sample_size;
  // 这是服务端本题统计的人数，不能拿考试总人数或班级人数补齐；未公开统计时也不推算人数。
  const population = Number.isInteger(count) && count > 0 ? `参比 ${count} 人` : "参比人数暂未提供";
  return <small className="muted" style={{ display: "block", whiteSpace: "normal" }} title="参比范围为当前成绩版本中，本次考试本题已发布成绩的参试学生；不是班级人数。">本版考试本题 · {population}</small>;
}
