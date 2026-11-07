import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

const backendProxy = {
  "/api": "http://127.0.0.1:8080",
  "/ready": "http://127.0.0.1:8080"
};

export default defineConfig(({ command }) => ({
  // 生产门户与教师端同源，静态资源必须留在 /student/；开发服务仍从根路径访问。
  base: command === "build" ? "/student/" : "/",
  plugins: [react()],
  server: { port: 5174, proxy: backendProxy },
  preview: { port: 4174, proxy: backendProxy }
}));
