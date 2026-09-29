import { createContext, useCallback, useContext, useEffect, useRef, useState, type FormEvent, type ReactNode } from "react";
import { Alert, Button, Input, Modal } from "antd";
import { ApiClientError, getUserErrorMessage, RECENT_AUTH_REQUIRED_EVENT } from "../api/client";

export interface StepUpOptions<T> {
  reason: string;
  description?: string;
  action: () => Promise<T>;
}

interface PendingStepUp<T = unknown> extends StepUpOptions<T> {
  resolve: (value: T) => void;
  reject: (reason?: unknown) => void;
}

export class StepUpCancelledError extends Error {
  constructor() {
    super("step-up authentication was cancelled");
    this.name = "StepUpCancelledError";
  }
}

export function isStepUpCancelledError(error: unknown): error is StepUpCancelledError {
  return error instanceof StepUpCancelledError;
}

function requiresRecentAuthentication(error: unknown) {
  return error instanceof ApiClientError && error.status === 428 && error.code === "recent_auth_required";
}

interface StepUpContextValue {
  runWithStepUp: <T>(options: StepUpOptions<T>) => Promise<T>;
  open: boolean;
  reason: string;
  description?: string;
  password: string;
  loading: boolean;
  error?: string;
  setPassword: (password: string) => void;
  complete: () => Promise<void>;
  cancel: () => void;
}

const StepUpContext = createContext<StepUpContextValue | null>(null);

export function StepUpProvider({ children, onReauthenticate }: { children: ReactNode; onReauthenticate: (password: string) => Promise<void> }) {
  const [open, setOpen] = useState(false);
  const [reason, setReason] = useState("关键操作");
  const [description, setDescription] = useState<string>();
  const [password, setPassword] = useState("");
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string>();
  const pendingRef = useRef<PendingStepUp | undefined>(undefined);
  const fallbackTimerRef = useRef<number | undefined>(undefined);
  const retryingRef = useRef(false);

  const clearPrompt = useCallback(() => {
    if (fallbackTimerRef.current !== undefined) {
      window.clearTimeout(fallbackTimerRef.current);
      fallbackTimerRef.current = undefined;
    }
    pendingRef.current = undefined;
    setOpen(false);
    setPassword("");
    setError(undefined);
    setDescription(undefined);
  }, []);

  const runWithStepUp = useCallback(async <T,>({ reason: nextReason, description: nextDescription, action }: StepUpOptions<T>) => {
    try {
      return await action();
    } catch (requestError) {
      if (!requiresRecentAuthentication(requestError)) throw requestError;
      if (fallbackTimerRef.current !== undefined) {
        window.clearTimeout(fallbackTimerRef.current);
        fallbackTimerRef.current = undefined;
      }
      return await new Promise<T>((resolve, reject) => {
        pendingRef.current = { reason: nextReason, description: nextDescription, action, resolve, reject } as PendingStepUp;
        setReason(nextReason);
        setDescription(nextDescription);
        setPassword("");
        setError(undefined);
        setOpen(true);
      });
    }
  }, []);

  useEffect(() => {
    const showFallbackPrompt = () => {
      if (retryingRef.current || pendingRef.current || fallbackTimerRef.current !== undefined) return;
      // Explicit runWithStepUp callers receive the rejected promise in the same
      // task and replace this fallback with a reason-aware prompt.
      fallbackTimerRef.current = window.setTimeout(() => {
        fallbackTimerRef.current = undefined;
        if (pendingRef.current || retryingRef.current) return;
        setReason("关键操作");
        setDescription("完成身份验证后，请重新执行刚才的操作。");
        setPassword("");
        setError(undefined);
        setOpen(true);
      }, 0);
    };
    window.addEventListener(RECENT_AUTH_REQUIRED_EVENT, showFallbackPrompt);
    return () => {
      window.removeEventListener(RECENT_AUTH_REQUIRED_EVENT, showFallbackPrompt);
      if (fallbackTimerRef.current !== undefined) window.clearTimeout(fallbackTimerRef.current);
    };
  }, []);

  const complete = useCallback(async () => {
    if (!password || loading) return;
    setLoading(true);
    setError(undefined);
    try {
      await onReauthenticate(password);
    } catch (authenticationError) {
      setError(getUserErrorMessage(authenticationError, "验证失败，请检查当前账号密码后重试。"));
      setLoading(false);
      return;
    }

    const pending = pendingRef.current;
    if (!pending) {
      clearPrompt();
      setLoading(false);
      return;
    }
    retryingRef.current = true;
    try {
      // 再认证后只重试一次；第二次仍失败直接交还调用方，避免反复弹窗或重放操作。
      const result = await pending.action();
      pending.resolve(result);
    } catch (retryError) {
      pending.reject(retryError);
    } finally {
      retryingRef.current = false;
      clearPrompt();
      setLoading(false);
    }
  }, [clearPrompt, loading, onReauthenticate, password]);

  const cancel = useCallback(() => {
    pendingRef.current?.reject(new StepUpCancelledError());
    clearPrompt();
  }, [clearPrompt]);

  return (
    <StepUpContext.Provider value={{ runWithStepUp, open, reason, description, password, loading, error, setPassword, complete, cancel }}>
      {children}
    </StepUpContext.Provider>
  );
}

export function useStepUp() {
  const value = useContext(StepUpContext);
  if (!value) throw new Error("useStepUp must be used inside StepUpProvider");
  return value;
}

export function StepUpDialog({ accountLabel, hidden = false }: { accountLabel: string; hidden?: boolean }) {
  const stepUp = useStepUp();
  const submit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    void stepUp.complete();
  };
  return (
    <Modal
      open={stepUp.open && !hidden}
      title="确认你的身份"
      closable={!stepUp.loading}
      keyboard={!stepUp.loading}
      maskClosable={false}
      footer={null}
      width={420}
      onCancel={stepUp.cancel}
    >
      <form className="public-computer-unlock" onSubmit={submit}>
        <p>为了确认是你本人，执行<strong>{stepUp.reason}</strong>前需要验证 <strong>{accountLabel}</strong> 的当前密码。</p>
        <p>{stepUp.description ?? "验证后 60 分钟内再次进行成绩发布、成绩回滚或其他关键操作，无需重复验证。"}</p>
        {stepUp.error ? <Alert type="error" showIcon message={stepUp.error} /> : null}
        <Input.Password
          value={stepUp.password}
          onChange={(event) => stepUp.setPassword(event.target.value)}
          placeholder="当前账号密码"
          autoComplete="current-password"
          autoFocus
        />
        <div className="public-computer-unlock-actions">
          <Button onClick={stepUp.cancel} disabled={stepUp.loading}>取消</Button>
          <Button type="primary" htmlType="submit" loading={stepUp.loading} disabled={!stepUp.password}>验证并继续</Button>
        </div>
      </form>
    </Modal>
  );
}
