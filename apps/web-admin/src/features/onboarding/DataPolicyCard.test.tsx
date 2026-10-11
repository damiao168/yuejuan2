// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen, within } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { OnboardingCheck } from "../../api/onboarding";
import { DataPolicyCard } from "./DataPolicyCard";

vi.mock("antd", () => ({
  Button: ({ children, onClick }: { children: ReactNode; onClick: () => void }) => <button type="button" onClick={onClick}>{children}</button>
}));

afterEach(cleanup);
const check: OnboardingCheck = { key: "data-policy", title: "数据使用政策", state: "warning", severity: "blocking" };

function policyValue(title: string) {
  return within(screen.getByText(title).parentElement!).getByRole("definition").textContent;
}

describe("平台默认数据政策摘要", () => {
  it.each([
    [true, "已允许"], [false, "关闭"], [undefined, "尚未核实"],
    ["true", "尚未核实"], [1, "尚未核实"], [null, "尚未核实"]
  ])("外部能力标记 %s 只按明确布尔值显示", (value, label) => {
    render(<DataPolicyCard check={{ ...check, metadata: { external_enabled: value, text_export_enabled: false, image_export_enabled: true } }} onNavigate={vi.fn()} />);
    expect(policyValue("外部 AI")).toBe(label);
    expect(policyValue("答题文本外发")).toBe("关闭");
    expect(policyValue("答题图片外发")).toBe("已允许");
    expect(screen.getByText("当前检查：平台默认数据政策。它不代表任何学校已完成数据外发授权。")).toBeTruthy();
  });

  it.each([
    ["manual_only", "人工处理"], ["approved_deployment_only", "已批准模型"], ["unknown", "尚未核实"]
  ])("失败回退模式 %s 保留明确含义", (value, label) => {
    render(<DataPolicyCard check={{ ...check, metadata: { fallback_mode: value } }} onNavigate={vi.fn()} />);
    expect(policyValue("失败回退")).toBe(label);
  });

  it("没有元数据时显示尚未核实，检查入口仍要求选择学校", () => {
    const navigate = vi.fn();
    render(<DataPolicyCard check={check} onNavigate={navigate} />);
    expect(screen.getAllByRole("definition").map((item) => item.textContent)).toEqual(["尚未核实", "尚未核实", "尚未核实", "尚未核实"]);
    fireEvent.click(screen.getByRole("button", { name: "选择学校检查使用策略" }));
    expect(navigate).toHaveBeenCalledWith("/platform/model-config?tab=governance&view=policy&choose_school=1");
  });
});
