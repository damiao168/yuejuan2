import { Button, type ButtonProps } from "antd";
import { useRouter } from "@tanstack/react-router";

/** 普通站内点击交给路由器，保留未保存提示和页面内存；新标签、下载沿用浏览器行为。 */
export function InternalRouteButton({ href, onClick, target, download, ...props }: ButtonProps) {
  const router = useRouter({ warn: false });
  return <Button {...props} href={href} target={target} download={download} onClick={(event) => {
    onClick?.(event);
    if (event.defaultPrevented || event.button !== 0 || event.metaKey || event.ctrlKey || event.shiftKey || event.altKey
      || (target && target !== "_self") || (download !== undefined && download !== false)
      || !href?.startsWith("/") || href.startsWith("//") || href.includes("\\") || !router?.history) return;
    event.preventDefault();
    router.history.push(href);
  }} />;
}
