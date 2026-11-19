// 改编自 Claude-Hydra（MIT）app/vite.config.ts，v1.1.6。
import path from "node:path";
import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

export default defineConfig({
  root: "src",
  plugins: [react()],
  base: "./",
  // 产物直接放进 Go 的嵌入目录；那里有一个纳入版本库的占位文件，所以不清空目录（由 make web 先删旧产物）。
  build: { outDir: "../../internal/web/dist", emptyOutDir: false, target: "es2021" },
  // 开发时页面由 vite 提供，接口转给本机正在运行的 cc-cluster web。
  server: { port: 3017, strictPort: true, proxy: { "/api": "http://127.0.0.1:7878" } },
  resolve: { alias: { "@": path.resolve(__dirname, "./src") } },
  clearScreen: false,
});
