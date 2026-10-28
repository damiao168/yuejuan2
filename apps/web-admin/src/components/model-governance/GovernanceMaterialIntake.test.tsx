// @vitest-environment jsdom
import type { ReactNode } from "react";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { GovernanceMaterialIntake } from "./GovernanceMaterialIntake";

vi.mock("antd", () => ({
 Button:({children,onClick,disabled}:{children:ReactNode;onClick:()=>void;disabled?:boolean})=><button onClick={onClick} disabled={disabled}>{children}</button>,
 Drawer:({children,onClose}:{children:ReactNode;onClose:()=>void})=><section><button onClick={onClose}>关闭材料</button>{children}</section>,
 Alert:({message}:{message:ReactNode})=><p role="alert">{message}</p>,
 Space:({children}:{children:ReactNode})=><div>{children}</div>
}));
afterEach(cleanup);
const evaluation={key:"math-v1",display_name:"数学验证",model_reference:"model-v1",prompt_version:"prompt-v1",rubric_version:"rubric-v1",dataset_reference:"dataset-v1",dataset_sha256:"a".repeat(64)};
const observation={response_key:"answer-1",response_fingerprint:"b".repeat(64),reference_kind:"gold",subject:"mathematics",archetype:"short_constructed",reference_score:3,model_score:2,max_score:5,ocr_quality:"high",answer_length:"short",rubric_complexity:"low",error_source:"none",needs_human_review:false,reference_reviewer_count:1,reference_adjudicated:false};

it("同一材料入口先预览来源和版本，再将逐卷证据交给登记流程",async()=>{
 const ready=vi.fn(),dirty=vi.fn();
 render(<GovernanceMaterialIntake tenantID="school-a" disabled={false} onReady={ready} onDirtyChange={dirty}/>);
 fireEvent.click(screen.getByRole("button",{name:"导入评测材料"}));
 const file=Object.assign(new File([""],"原报告.json"),{text:async()=>JSON.stringify({evaluation,observations:[observation]})});
 fireEvent.change(screen.getByLabelText("评测材料文件"),{target:{files:[file]}});
 await screen.findByText("逐卷评分证据：数学验证");
 expect(screen.getByText(/模型 model-v1 · 提示词 prompt-v1 · 细则 rubric-v1/)).toBeTruthy();
 expect(ready).not.toHaveBeenCalled();
 fireEvent.click(screen.getByRole("button",{name:"继续登记"}));
 expect(ready).toHaveBeenCalledWith({kind:"scoring_records",material:{evaluation,observations:[observation]}});
});

it("关闭后到达的文件读取不能带入旧学校材料",async()=>{
 let resolve!:(value:string)=>void;
 const ready=vi.fn();
 const view=render(<GovernanceMaterialIntake tenantID="school-a" disabled={false} onReady={ready} onDirtyChange={vi.fn()}/>);
 fireEvent.click(screen.getByRole("button",{name:"导入评测材料"}));
 const file=Object.assign(new File([""],"old.json"),{text:()=>new Promise<string>(done=>{resolve=done})});
 fireEvent.change(screen.getByLabelText("评测材料文件"),{target:{files:[file]}});
 view.rerender(<GovernanceMaterialIntake tenantID="school-b" disabled={false} onReady={ready} onDirtyChange={vi.fn()}/>);
 resolve(JSON.stringify({evaluation,observations:[observation]}));
 fireEvent.click(screen.getByRole("button",{name:"导入评测材料"}));
 expect(screen.queryByText("逐卷评分证据：数学验证")).toBeNull();
 expect(ready).not.toHaveBeenCalled();
});
