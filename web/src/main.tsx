// 改编自 Claude-Hydra（MIT）app/src/main.tsx，v1.1.6：去掉了 Tauri 窗口相关的部分。
import React from "react";
import ReactDOM from "react-dom/client";
import App from "./App";
import { ToastProvider } from "./components/ui/toast";
import { applyTheme } from "./lib/theme";
import "./index.css";

applyTheme();

ReactDOM.createRoot(document.getElementById("root")!).render(
  <React.StrictMode>
    <ToastProvider>
      <App />
    </ToastProvider>
  </React.StrictMode>,
);
