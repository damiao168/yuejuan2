import type { StudentQuestion, StudentQuestionAnnotation } from "../../api";

type Geometry = NonNullable<StudentQuestion["answer_geometry"]>;

function validGeometry(value: Geometry | undefined): value is Geometry {
  return Boolean(value && [value.x, value.y, value.width, value.height].every(Number.isFinite)
    && value.x >= 0 && value.y >= 0 && value.width > 0 && value.height > 0
    && value.x + value.width <= 1.000001 && value.y + value.height <= 1.000001);
}

// 教师批注以单题裁图归一化；整页展示时必须通过该题在页面中的裁剪矩形转换，不能直接复用百分比。
// 缺少可信映射时不画该批注，调用方保留提示和单题文字反馈入口。
export function annotationOnPaper(annotation: StudentQuestionAnnotation, question: StudentQuestion): StudentQuestionAnnotation | null {
  const crop = question.answer_geometry;
  const local = annotation.geometry;
  if (!validGeometry(crop) || local.coordinate_space !== "canonical_image_normalized" || !validGeometry(local)) return null;
  return { ...annotation, geometry: { ...local,
    x: crop.x + local.x * crop.width,
    y: crop.y + local.y * crop.height,
    width: local.width * crop.width,
    height: local.height * crop.height
  } };
}
