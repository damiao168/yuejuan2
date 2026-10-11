import type { StudentReviewAnnotation } from "../../../api/reviewAnnotations";
import { geometryStyle } from "./geometry";
import "./reviewAnnotations.css";

export interface StudentAnnotationOverlayProps {
  annotations: readonly StudentReviewAnnotation[];
}

// 该组件只接受适合学生端展示的 SDK DTO；教师批注
// 必须先经过显式且已审查的投影，才能传入。
export function StudentAnnotationOverlay({ annotations }: Readonly<StudentAnnotationOverlayProps>) {
  return (
    <div className="student-annotation-overlay" aria-label="教师公开批注">
      {annotations.map((annotation) => {
        const point = annotation.geometry.width === 0 && annotation.geometry.height === 0;
        return (
          <span
            key={annotation.id}
            className={point ? "review-annotation-mark review-annotation-mark--point" : "review-annotation-mark"}
            style={geometryStyle(annotation.geometry)}
            title={annotation.content}
          />
        );
      })}
    </div>
  );
}
