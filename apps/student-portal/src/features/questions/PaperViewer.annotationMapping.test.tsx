// @vitest-environment jsdom
import { render, screen, waitFor } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import type { StudentQuestion, StudentQuestionAnnotation } from "../../api";
import { StudentSessionContext } from "../../studentSession";
import { makeQuestion, makeResult } from "../../test/studentAppBatch8Fixtures";
import { PaperViewer } from "./PaperViewer";

const mocks = vi.hoisted(() => ({ annotations: vi.fn() }));
vi.mock("../../api", () => ({ listQuestionAnnotations: mocks.annotations, getResult: vi.fn(), studentPaperPageImageURL: () => "/synthetic-page.png" }));

describe("whole-paper annotation placement", () => {
  it.each([true, false])("keeps the originating crop mapping or explicitly omits unmapped overlays: mapping=%s", async available => {
    const annotation = { id: "annotation", type: "rectangle", content: "检查此步", geometry: { coordinate_space: "canonical_image_normalized", x: .25, y: .5, width: .5, height: .25 } } as StudentQuestionAnnotation;
    mocks.annotations.mockResolvedValue({ annotations: [annotation] });
    const question: StudentQuestion = { ...makeQuestion(), page_no: 1,
      answer_geometry: available ? { x: .2, y: .3, width: .4, height: .2 } : undefined };
    const result = { ...makeResult([question]), paper_pages: [{ page_no: 1, question_id: question.question_id }] };
    const view = render(<StudentSessionContext.Provider value={{ draftOwner: "synthetic", submitting: false, setSubmitting: vi.fn(), reauthenticate: vi.fn() }}>
      <PaperViewer examID="exam-1" result={result} onViewCurrent={vi.fn()} />
    </StudentSessionContext.Provider>);
    await waitFor(() => expect(screen.queryByText("正在加载本页批注…")).toBeNull());
    const overlay = view.container.querySelector<HTMLElement>(".paper-annotation");
    if (available) {
      expect(Number.parseFloat(overlay!.style.left)).toBeCloseTo(30);
      expect(overlay!.style.top).toBe("40%");
      expect(overlay!.style.width).toBe("20%");
      expect(overlay!.style.height).toBe("5%");
      expect(overlay!.textContent).toBe("检查此步");
    } else {
      expect(overlay).toBeNull();
      expect(screen.getByText(/部分教师批注缺少可靠的整页位置/)).toBeTruthy();
    }
  });
});
