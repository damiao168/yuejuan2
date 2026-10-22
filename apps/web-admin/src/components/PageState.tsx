import { Alert, Button, Empty, Result, Spin } from "antd";
import "./page-loading.css";
import { Home, RefreshCw } from "lucide-react";
import { usePageLoading } from "./pageReadiness";

type ForbiddenScope = "platform" | "school" | "exam" | "general";

function permissionContact(scope: ForbiddenScope) {
  switch (scope) {
    case "platform": return "平台负责人";
    case "exam": return "本场考试负责人";
    case "school": return "学校管理员";
    default: return "所属学校或平台的账号管理员";
  }
}

export function LoadingState({ label = "加载中" }: Readonly<{ label?: string }>) {
  usePageLoading();
  return (
    <div className="state-panel state-panel-loading" aria-busy="true">
      <div className="page-loading-status" role="status"><Spin size="small" /><span>{label}</span></div>
      <div className="page-loading-skeleton" aria-hidden="true">
        <i className="page-loading-heading" /><i className="page-loading-toolbar" />
        <i /><i /><i /><i />
      </div>
    </div>
  );
}

export function EmptyState({ title = "暂无数据", description = "暂时还没有相关记录。" }: Readonly<{ title?: string; description?: string }>) {
  return (
    <div className="state-panel">
      <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description={<span>{title}</span>} />
      <span className="muted">{description}</span>
    </div>
  );
}

export function ErrorState({ message, onRetry }: Readonly<{ message: string; onRetry?: () => void }>) {
  return (
    <Alert
      type="error"
      showIcon
      message="加载失败"
      description={message || "网络异常，请稍后重试。"}
      action={
        onRetry ? (
          <Button icon={<RefreshCw size={16} />} onClick={onRetry}>
            重试
          </Button>
        ) : undefined
      }
    />
  );
}

export function ForbiddenState({ onBack, scope = "general", feature }: Readonly<{ onBack?: () => void; scope?: ForbiddenScope; feature?: string }>) {
  const contact = permissionContact(scope);
  return <Result status="403" title={feature ? `暂时不能使用${feature}` : "当前账号没有访问权限"} subTitle={`请联系${contact}核对账号职责与权限。可以先返回工作台，继续使用已开放的功能。`} extra={onBack ? <Button icon={<Home size={16} />} onClick={onBack}>返回工作台</Button> : undefined} />;
}

export function NotFoundState({ onBack }: Readonly<{ onBack?: () => void }>) {
  return <Result status="404" title="未找到" subTitle="该页面不存在，或功能尚未开放。" extra={onBack ? <Button icon={<Home size={16} />} onClick={onBack}>返回首页</Button> : undefined} />;
}
