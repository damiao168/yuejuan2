import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

const backendProxy = {
  "/api": "http://127.0.0.1:8080",
  "/ready": "http://127.0.0.1:8080"
};

export default defineConfig({
  plugins: [react()],
  build: {
    manifest: true,
    rollupOptions: {
      output: {
        // OCR Worker按eng.traineddata.gz拼接语言路径；只对此模型保留固定文件名，禁止回落公网CDN。
        assetFileNames: (asset) => asset.names?.includes("eng.traineddata.gz")
          ? "ocr/eng.traineddata.gz" : "assets/[name]-[hash][extname]",
        manualChunks: {
          antd: ["antd"],
          charts: ["recharts"],
          motion: ["framer-motion"]
        }
      }
    }
  },
  server: {
    port: 5173,
    proxy: backendProxy
  },
  preview: {
    port: 4173,
    proxy: backendProxy
  }
});
