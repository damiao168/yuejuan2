// @vitest-environment jsdom
import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { AnswerGroup, AnswerGroupMemberContext } from "../../api/answerGroups";
import { AnswerGroupReviewWorkspace } from "./AnswerGroupReviewWorkspace";
const evidence = vi.hoisted(() => ({ current: null as null | { segmentId: string; autoOpen: boolean; showReference: boolean; onRead: (value: AnswerGroupMemberContext) => void; onContext: (value: AnswerGroupMemberContext) => void } }));
vi.mock("./AnswerGroupSampleEvidence", () => ({ AnswerGroupSampleEvidence: (props: NonNullable<typeof evidence.current>) => { evidence.current = props; return <p>当前实际样本 {props.segmentId}</p>; } }));
vi.mock("../../components/TaskScoringReference", () => ({ TaskScoringReference: ({ question }: { question: { stem?: string } }) => <div data-testid="shared-reference">{question.stem}</div> }));
vi.mock("./AnswerGroupManualHandoff", () => ({ AnswerGroupManualHandoff: () => <p>交接单份人工复核</p> }));
vi.mock("antd", () => ({
  Space: ({ children }: { children: ReactNode }) => <div>{children}</div>, Tag: ({ children }: { children: ReactNode }) => <span>{children}</span>,
  Alert: ({ message }: { message: ReactNode }) => <p role="alert">{message}</p>,
  Button: ({ children, disabled, onClick }: { children: ReactNode; disabled?: boolean; onClick: () => void }) => <button disabled={disabled} onClick={onClick}>{children}</button>,
  Select: ({ value, disabled, options, onChange }: { value: string; disabled?: boolean; options: { value: string; label: string }[]; onChange: (value: string) => void }) => <select aria-label="选择抽检样本" value={value} disabled={disabled} onChange={event => onChange(event.target.value)}>{options.map(option => <option key={option.value} value={option.value}>{option.label}</option>)}</select>
}));
beforeEach(() => { evidence.current = null; }); afterEach(cleanup);
function group(): AnswerGroup {
  return { id: "group", tenant_id: "tenant", exam_id: "exam", question_id: "question", exam_question_snapshot_id: "frozen", algorithm_version: "v1", representation_version: "v1", member_count: 2, representative_submission_id: "submission-a", homogeneity: 1, status: "sampling", minimum_sample: 2, reviewed_sample_count: 0, can_confirm: false, created_at: "", updated_at: "",
    members: ["a", "b"].map((id, index) => ({ segment_id: id, submission_id: `submission-${id}`, representation_hash: `hash-${id}`, representative: index === 0, boundary: false, outlier: index === 1, similarity: 1, outlier_score: 0 })) };
}
function material(id = "a"): AnswerGroupMemberContext {
  return { group_id: "group", member: group().members.find(member => member.segment_id === id)!, expected_revision: 0, answer_text: `实际答案${id}`, segment_image_url: "",
    question: { id: "question", exam_id: "exam", question_no: "1", question_type: "short_answer", score: 2, stem: "同一冻结题干", rubric: { id: "rubric", version: "v1", status: "active", question_id: "question", max_score: 2, points: [{ id: "p", description: "评分点", score: 2 }] } } };
}
describe("逐份分组抽检", () => {
  it("默认加载当前样本，仅显示共用依据不会解锁，接受成功后才进入下一份", async () => {
    const read = vi.fn(); let resolve!: (value: boolean) => void; const review = vi.fn(() => new Promise<boolean>(done => { resolve = done; }));
    render(<AnswerGroupReviewWorkspace group={group()} canReview acting={false} canReadReview={false} onRead={read} onReview={review} />);
    expect(evidence.current).toMatchObject({ segmentId: "a", autoOpen: true, showReference: false });
    const accept = screen.getByRole("button", { name: "接受并下一份" }) as HTMLButtonElement; expect(accept.disabled).toBe(true);
    act(() => evidence.current!.onContext(material())); expect(screen.getAllByTestId("shared-reference")).toHaveLength(1); expect(accept.disabled).toBe(true); expect(read).not.toHaveBeenCalled();
    act(() => evidence.current!.onRead(material())); expect(accept.disabled).toBe(false);
    fireEvent.click(accept); fireEvent.click(accept); expect(review).toHaveBeenCalledExactlyOnceWith("a", "accepted"); expect(evidence.current?.segmentId).toBe("a");
    await act(async () => resolve(true)); expect(evidence.current?.segmentId).toBe("b"); expect(accept.disabled).toBe(true);
    act(() => evidence.current!.onRead(material("b"))); expect(screen.getAllByTestId("shared-reference")).toHaveLength(1); expect(read).toHaveBeenCalledTimes(2);
  });
  it("失败保留当前样本与实际材料，不自动登记其他样本", async () => {
    const review = vi.fn().mockResolvedValue(false); const read = vi.fn();
    render(<AnswerGroupReviewWorkspace group={group()} canReview acting={false} canReadReview={false} onRead={read} onReview={review} />);
    act(() => evidence.current!.onRead(material())); fireEvent.click(screen.getByRole("button", { name: "拒绝并下一份" }));
    await waitFor(() => expect(review).toHaveBeenCalledOnce()); expect(evidence.current?.segmentId).toBe("a"); expect(read).toHaveBeenCalledOnce();
  });
  it("A到B到A拒绝旧显示回调，冻结版本号相同但正文变化也不复用依据", () => {
    const read = vi.fn(); render(<AnswerGroupReviewWorkspace group={group()} canReview acting={false} canReadReview={false} onRead={read} onReview={vi.fn()} />);
    const oldRead = evidence.current!.onRead; act(() => oldRead(material()));
    fireEvent.change(screen.getByLabelText("选择抽检样本"), { target: { value: "b" } });
    fireEvent.change(screen.getByLabelText("选择抽检样本"), { target: { value: "a" } });
    act(() => oldRead(material())); expect(read).toHaveBeenCalledTimes(1); expect((screen.getByRole("button", { name: "接受并下一份" }) as HTMLButtonElement).disabled).toBe(true);
    const changed = material(); changed.question = { ...changed.question, stem: "不同冻结正文" }; act(() => evidence.current!.onRead(changed));
    expect(screen.getByRole("alert").textContent).toContain("冻结评分依据与本组不一致"); expect(read).toHaveBeenCalledTimes(1);
  });
});
