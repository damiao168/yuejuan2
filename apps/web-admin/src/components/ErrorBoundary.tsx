import { Component, type ErrorInfo, type ReactNode } from "react";
import { Button, Result } from "antd";
import { Home, RefreshCw } from "lucide-react";

interface ErrorBoundaryProps {
  children: ReactNode;
  fallback?: ReactNode;
  /**
   * 此值变化时会清除已捕获的错误。传入当前路由，
   * 使导航能够恢复页面。优先使用它而不是边界上的 `key`：
   * 使用 `key` 会在每次导航时重新挂载整个子树，
   * 即使没有发生错误，也会丢弃未保存的编辑状态，
   * 并重新发起页面的数据请求。
   */
  resetKey?: string;
}

interface ErrorBoundaryState {
  hasError: boolean;
}

export class ErrorBoundary extends Component<ErrorBoundaryProps, ErrorBoundaryState> {
  state: ErrorBoundaryState = { hasError: false };

  static getDerivedStateFromError(): ErrorBoundaryState {
    return { hasError: true };
  }

  componentDidCatch(error: Error, errorInfo: ErrorInfo) {
    console.error("页面渲染异常", error, errorInfo.componentStack);
  }

  componentDidUpdate(prevProps: ErrorBoundaryProps) {
    if (this.state.hasError && prevProps.resetKey !== this.props.resetKey) {
      this.setState({ hasError: false });
    }
  }

  private readonly reload = () => {
    window.location.reload();
  };

  // 这里完全重载页面，不仅更换路由并调用 setState：
  // 故障页面可能就是仪表盘，而 lazy() 会持续缓存被拒绝的模块加载，
  // 所以原地重新渲染仍会抛出同一个错误。
  private readonly backToDashboard = () => {
    window.history.replaceState(window.history.state, "", "/dashboard");
    window.location.reload();
  };

  render() {
    if (this.state.hasError) {
      if (this.props.fallback !== undefined) {
        return this.props.fallback;
      }
      return (
        <Result
          status="error"
          title="页面出现异常"
          subTitle="页面出现了意外错误，可能是网络不稳定或系统刚完成更新。请重新加载；若多次出现，请联系管理员。"
          extra={[
            <Button key="reload" type="primary" icon={<RefreshCw size={16} />} onClick={this.reload}>
              重新加载
            </Button>,
            <Button key="dashboard" icon={<Home size={16} />} onClick={this.backToDashboard}>
              返回工作台
            </Button>
          ]}
        />
      );
    }
    return this.props.children;
  }
}
