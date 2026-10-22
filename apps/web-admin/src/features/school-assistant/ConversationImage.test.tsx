// @vitest-environment jsdom
import { ConfigProvider } from "antd";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { SessionUser } from "../../auth/session";
import { canPreviewConversationImage, ConversationImage } from "./ConversationImage";
import { ConversationMessages } from "./SchoolConversationDisplay";

const image = { name: "合成图片.png", media_type: "image/png", content: "data:image/png;base64,c3ludGhldGlj", size: 9 };
const user: SessionUser = {
  id: "owner", username: "owner", displayName: "管理员", name: "管理员", role: "school_admin", roles: ["school_admin"],
  tenant: "tenant", school: "学校", currentExam: "", permissions: [], publicComputer: false,
  organizationScope: { resolved: true, tenantWide: false, schoolIds: ["school"], gradeIds: [], classIds: [] },
};

beforeEach(() => {
  vi.stubGlobal("matchMedia", () => ({ matches: false, addListener() {}, removeListener() {}, addEventListener() {}, removeEventListener() {} }));
  const computedStyle = window.getComputedStyle.bind(window);
  vi.spyOn(window, "getComputedStyle").mockImplementation((element) => computedStyle(element));
});
afterEach(() => { cleanup(); vi.restoreAllMocks(); vi.unstubAllGlobals(); });

describe("学校对话的本地图片预览", () => {
  it("只允许带本地图片正文的图像附件打开预览", () => {
    expect(canPreviewConversationImage(image)).toBe(true);
    expect(canPreviewConversationImage({ ...image, content: "blob:http://localhost/synthetic" })).toBe(true);
    for (const attachment of [
      { ...image, content: "" },
      { ...image, content: "https://example.invalid/image.png" },
      { ...image, media_type: "text/plain" },
    ]) {
      expect(canPreviewConversationImage(attachment)).toBe(false);
      const view = render(<ConversationImage attachment={attachment} />);
      expect(view.container.querySelector("img,button")).toBeNull();
      view.unmount();
    }
  });

  it("点击缩略图放大，按 Escape 关闭后回到原按钮并保留对话滚动位置", async () => {
    render(<ConfigProvider theme={{ token: { motion: false } }}><div data-testid="conversation-flow"><ConversationImage attachment={image} /></div></ConfigProvider>);
    const flow = screen.getByTestId("conversation-flow");
    flow.scrollTop = 120;
    const thumbnail = screen.getByRole("button", { name: `查看图片：${image.name}` }) as HTMLButtonElement;
    expect(thumbnail.type).toBe("button");
    expect(thumbnail.getAttribute("aria-haspopup")).toBe("dialog");
    thumbnail.focus();
    fireEvent.click(thumbnail);
    const dialog = await screen.findByRole("dialog", { name: image.name });
    expect(dialog.querySelector("img")?.getAttribute("src")).toBe(image.content);
    expect(flow.contains(dialog)).toBe(false);
    fireEvent.keyDown(dialog, { key: "Escape", keyCode: 27 });
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    await waitFor(() => expect(document.activeElement).toBe(thumbnail));
    expect(flow.scrollTop).toBe(120);
  });

  it("已发送图片可查看，历史剥离图片正文和非图片附件仍显示文件名", async () => {
    render(<ConfigProvider theme={{ token: { motion: false } }}><ConversationMessages
      thread={{ id: "synthetic-thread", title: "图片", updatedAt: 1, messages: [{ role: "user", content: "查看附件", attachments: [image, { ...image, name: "历史图片.png", content: "" }, { ...image, name: "说明.txt", media_type: "text/plain", content: "合成文字" }] }] }}
      evidence={new Map()} user={user} reasoningExpanded={false} copiedMessage="" onCopy={async () => {}} />
    </ConfigProvider>);
    expect(screen.getByText("历史图片.png")).toBeDefined();
    expect(screen.getByText("说明.txt")).toBeDefined();
    expect(screen.queryByRole("button", { name: "查看图片：历史图片.png" })).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: `查看图片：${image.name}` }));
    expect(await screen.findByRole("dialog", { name: image.name })).toBeDefined();
  });

  it("最新完成回复收起思考时通知父级，旧回复的开合不改变跟随策略", () => {
    const toggle = vi.fn();
    render(<ConversationMessages thread={{ id: "synthetic-thread", title: "思考", updatedAt: 1, messages: [
      { role: "assistant", content: "先前回答", reasoning_content: "先前思考" },
      { role: "assistant", content: "最新回答", reasoning_content: "最新思考" },
    ] }} evidence={new Map()} user={user} reasoningExpanded copiedMessage="" onCopy={async () => {}} onReasoningToggle={toggle} />);
    const disclosures = screen.getAllByRole("button", { name: /思考过程/ });
    fireEvent.click(disclosures[0]);
    expect(toggle).not.toHaveBeenCalled();
    fireEvent.click(disclosures[1]);
    expect(toggle).toHaveBeenCalledExactlyOnceWith(false);
  });
});
