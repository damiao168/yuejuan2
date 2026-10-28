// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, expect, it, vi } from "vitest";
import { CalibrationPolicyReuse } from "./CalibrationPolicyReuse";
import type { CalibrationPolicy } from "../../api/calibration";
const api = vi.hoisted(() => ({ questions: vi.fn(), policy: vi.fn() }));
vi.mock("../../api/papers", () => ({ listQuestions: api.questions }));
vi.mock("../../api/calibration", () => ({ getCalibrationPolicy: api.policy }));
vi.mock("antd", () => ({ Button: ({ children, disabled, loading, onClick }: { children: ReactNode; disabled?: boolean; loading?: boolean; onClick: () => void }) => <button disabled={disabled || loading} onClick={onClick}>{children}</button>, Space: ({ children }: { children: ReactNode }) => <div>{children}</div>, Select: ({ options, onChange }: { options: {value:string;label:string}[];onChange:(id:string)=>void }) => <select aria-label="可复用试评标准" onChange={event=>onChange(event.target.value)}><option/>{options.map(option=><option key={option.value} value={option.value}>{option.label}</option>)}</select>, Alert: ({ message }: { message: string }) => <p role="alert">{message}</p> }));
afterEach(() => { cleanup(); vi.clearAllMocks(); });
const source = { id:"policy-source",exam_id:"exam",question_id:"source",revision:12,archetype_code:"extended_response",max_score:10,minimum_samples:5,maximum_mae:0,minimum_exact_agreement:1,minimum_within_one_agreement:1,maximum_severe_rate:0,severe_error_threshold:2,qualification_validity_days:7 } as CalibrationPolicy;
it("只复用本场同类型同满分已保存标准，保留目标修订且读取不自动保存", async () => {
  api.questions.mockResolvedValue({ questions:[{id:"target",exam_id:"exam",question_no:"1",score:10},{id:"source",exam_id:"exam",question_no:"2",score:10},{id:"other-score",exam_id:"exam",question_no:"3",score:5}] });
  api.policy.mockResolvedValue({policy:source});
  const apply=vi.fn(),facts=vi.fn();
  render(<CalibrationPolicyReuse examId="exam" questionId="target" disabled={false} readCurrent={()=>({archetype_code:"extended_response",max_score:10,expected_revision:4})} onKnownQuestion={facts} onApply={apply}/>);
  fireEvent.click(screen.getByText("读取可复用标准")); await screen.findByLabelText("可复用试评标准");
  expect(api.policy).toHaveBeenCalledExactlyOnceWith("exam","source"); expect(apply).not.toHaveBeenCalled();
  fireEvent.change(screen.getByLabelText("可复用试评标准"),{target:{value:source.id}}); fireEvent.click(screen.getByText("带入本题待核对"));
  expect(apply).toHaveBeenCalledWith(expect.objectContaining({expected_revision:4,minimum_samples:5,maximum_mae:0}));
  expect(apply.mock.calls[0][0]).not.toHaveProperty("id"); expect(apply.mock.calls[0][0]).not.toHaveProperty("question_id");
});
it("来源读取后目标类型改变不能带入；未知本题事实只从同场题目目录补齐", async () => {
  api.questions.mockResolvedValue({questions:[{id:"target",exam_id:"exam",question_no:"1",score:10,assessment_archetype:"extended_response"},{id:"source",exam_id:"exam",question_no:"2",score:10}]}); api.policy.mockResolvedValue({policy:source});
  let current:{archetype_code?:string;max_score?:number;expected_revision?:number}={expected_revision:3};
  const apply=vi.fn(),facts=vi.fn(values=>{current={...current,...values};});
  render(<CalibrationPolicyReuse examId="exam" questionId="target" disabled={false} readCurrent={()=>current} onKnownQuestion={facts} onApply={apply}/>);
  fireEvent.click(screen.getByText("读取可复用标准")); await screen.findByLabelText("可复用试评标准");
  expect(facts).toHaveBeenCalledWith({archetype_code:"extended_response",max_score:10});
  current={...current,max_score:12}; fireEvent.change(screen.getByLabelText("可复用试评标准"),{target:{value:source.id}}); fireEvent.click(screen.getByText("带入本题待核对"));
  await waitFor(()=>expect(screen.getByRole("alert").textContent).toContain("已改变")); expect(apply).not.toHaveBeenCalled();
});
