import { createContext } from "react";

// 顶栏容器由外壳提供，阅卷操作通过 Portal 放入同一行；事件与未保存保护仍由工作台管理。
export const GradingToolbarContext = createContext<HTMLElement | null>(null);
