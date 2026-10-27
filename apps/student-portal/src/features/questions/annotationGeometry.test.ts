import { describe, expect, it } from "vitest";
import type { StudentQuestion, StudentQuestionAnnotation } from "../../api";
import { annotationOnPaper } from "./annotationGeometry";

const annotation = { id: "annotation", content: "复核此处", geometry: { coordinate_space: "canonical_image_normalized", x: .25, y: .5, width: .5, height: .25 } } as StudentQuestionAnnotation;

describe("question annotation to whole paper coordinates", () => {
  it("maps through the originating question crop without mutating the stored annotation", () => {
    const question = { answer_geometry: { x: .2, y: .3, width: .4, height: .2 } } as StudentQuestion;
    const mapped = annotationOnPaper(annotation, question)!.geometry;
    expect(mapped).toMatchObject({ coordinate_space: "canonical_image_normalized", y: .4, width: .2, height: .05 });
    expect(mapped.x).toBeCloseTo(.3);
    expect(annotation.geometry.x).toBe(.25);
  });
  it.each([undefined, { x: 0, y: 0, width: 0, height: .3 }, { x: .8, y: 0, width: .4, height: .2 }, { x: NaN, y: 0, width: .4, height: .2 }])("omits overlays when the page mapping cannot be trusted: %s", crop => {
    expect(annotationOnPaper(annotation, { answer_geometry: crop } as StudentQuestion)).toBeNull();
  });
});
