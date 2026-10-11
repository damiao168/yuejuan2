import { Tag } from "antd";
import type { StatusTone } from "../types";

const colors: Record<StatusTone, string> = {
  success: "success",
  warning: "warning",
  danger: "error",
  info: "cyan",
  processing: "processing",
  neutral: "default"
};

export function StatusTag({ tone, children }: Readonly<{ tone: StatusTone; children: string }>) {
  return <Tag color={colors[tone]}>{children}</Tag>;
}
