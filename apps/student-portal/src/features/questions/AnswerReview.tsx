import { useState, type CSSProperties } from "react";
import type { StudentQuestion, StudentQuestionAnnotation } from "../../api";

export function AnswerReview({ question, annotations, imageURL }: {
  question: StudentQuestion;
  annotations: StudentQuestionAnnotation[];
  imageURL: string;
}) {
  const [activeAnnotation, setActiveAnnotation] = useState<string | null>(annotations[0]?.id ?? null);
  const [imageAvailable, setImageAvailable] = useState(true);
  const rate = Math.round((question.score_rate ?? (question.max_score > 0 ? question.score / question.max_score : 0)) * 100);
  // 批注坐标是相对图片的比例，转成百分比后随原图一同缩放，不能直接当像素使用。
  return <div className="answer-review">
    <section className="answer-canvas" aria-label="我的原始答卷">
      <div className="answer-canvas-header"><strong>我的原始答卷</strong><span>{annotations.length ? `${annotations.length} 条教师批注` : "原卷"}</span></div>
      {imageAvailable ? <div className="answer-image-stage">
        <img src={imageURL} alt={`第 ${question.question_no} 题答题区域`} onError={() => setImageAvailable(false)} />
        {annotations.map((annotation) => <button
          type="button"
          key={annotation.id}
          aria-label={`查看批注：${annotation.content || "教师标注"}`}
          className={`annotation-overlay ${activeAnnotation === annotation.id ? "active" : ""}`}
          style={{ left: `${annotation.geometry.x * 100}%`, top: `${annotation.geometry.y * 100}%`, width: `${annotation.geometry.width * 100}%`, height: `${annotation.geometry.height * 100}%` }}
          onClick={() => setActiveAnnotation(annotation.id)}
        />)}
      </div> : <div className="answer-unavailable"><strong>原卷图暂时不可用</strong><p>你仍然可以查看本题得分与公开反馈。</p></div>}
    </section>
    <aside className="answer-inspector">
      <div className="question-result-line"><div><span>本题得分</span><strong>{question.score} / {question.max_score}</strong></div><i style={{ "--score-rate": `${rate}%` } as CSSProperties} /></div>
      {question.cohort && Number.isFinite(question.cohort.mean_score_rate) ? <div className="question-comparison"><span>我的得分率 {rate}%</span><span>对比群体平均 {Math.round(question.cohort.mean_score_rate * 100)}%</span></div> : null}
      {question.actual_answer ? <section><h3>我的答案</h3><p>{question.actual_answer}</p></section> : null}
      {question.correct_answer ? <section><h3>参考答案</h3><p>{question.correct_answer}</p></section> : null}
      {annotations.length > 0 ? <section><h3>教师批注</h3><div className="annotation-notes">{annotations.map((annotation, index) => <button type="button" key={annotation.id} className={activeAnnotation === annotation.id ? "active" : ""} onClick={() => setActiveAnnotation(annotation.id)}><span>{index + 1}</span>{annotation.content || "教师标注"}</button>)}</div></section> : null}
      {question.feedback ? <section><h3>教师反馈</h3><p>{question.feedback}</p></section> : null}
      {question.rubric_summary?.length ? <section><h3>评分要点</h3><ul>{question.rubric_summary.map((item) => <li key={item}>{item}</li>)}</ul></section> : null}
      {question.knowledge_points?.length ? <section><h3>涉及知识</h3><div className="knowledge-tags">{question.knowledge_points.map((item) => <span key={item}>{item}</span>)}</div></section> : null}
      {!question.feedback && !question.rubric_summary?.length && !annotations.length ? <p className="muted">本题未公开文字反馈。</p> : null}
    </aside>
  </div>;
}
