// 可重试错误沿用当前学生会话的重新登录入口。
import { useStudentSession } from "../studentSession";

export function ErrorNotice({ message, onRetry, onBack, loginRequired }: Readonly<{ message: string; onRetry: () => void; onBack?: () => void; loginRequired?: boolean }>) {
  const { reauthenticate, submitting } = useStudentSession();
  return <div className="error-notice" role="alert"><strong>暂时无法显示内容</strong><p>{message}</p><div>{loginRequired ? <button className="secondary-button" type="button" disabled={submitting} onClick={reauthenticate}>重新登录</button> : <button className="secondary-button" type="button" onClick={onRetry}>重试</button>}{onBack ? <button className="text-button" type="button" onClick={onBack}>返回</button> : null}</div></div>;
}
