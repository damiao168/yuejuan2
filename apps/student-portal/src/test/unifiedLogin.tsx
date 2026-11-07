import { cleanup, render, screen } from "@testing-library/react";
import App from "../App";

export async function reenterAfterUnifiedLogin(): Promise<void> {
  await screen.findByText("正在前往登录…");
  // 统一登录在另一个页面完成；Cookie 更新后重新挂载门户，不能继续调用已卸载页面的登录回调。
  cleanup();
  render(<App />);
}
