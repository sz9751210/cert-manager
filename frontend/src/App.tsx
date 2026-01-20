// src/App.tsx
import React, { useState, useEffect } from "react";
import { ConfigProvider, theme } from "antd";
import { BrowserRouter, Routes, Route, Navigate } from "react-router-dom";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";

import MainLayout from "./components/Layout/MainLayout"; // 抽離的 Layout
import Login from "./pages/Login";
import Dashboard from "./pages/Dashboard";
import CertDecoder from "./pages/CertDecoder";
import Settings from "./pages/Settings";

// 初始化 QueryClient
const queryClient = new QueryClient();

// 保護路由
const ProtectedRoute: React.FC<{ children: React.ReactNode }> = ({ children }) => {
  const token = localStorage.getItem("token");
  return token ? <>{children}</> : <Navigate to="/login" replace />;
};

const App: React.FC = () => {
  // 主題狀態管理
  const [isDarkMode, setIsDarkMode] = useState(() => localStorage.getItem("theme") === "dark");

  const toggleTheme = () => {
    const nextMode = !isDarkMode;
    setIsDarkMode(nextMode);
    localStorage.setItem("theme", nextMode ? "dark" : "light");
  };

  useEffect(() => {
    document.body.style.backgroundColor = isDarkMode ? "#000000" : "#f0f2f5";
  }, [isDarkMode]);

  return (
    <QueryClientProvider client={queryClient}>
      <ConfigProvider
        theme={{
          algorithm: isDarkMode ? theme.darkAlgorithm : theme.defaultAlgorithm,
          token: { colorPrimary: "#1890ff" },
        }}
      >
        <BrowserRouter>
          <Routes>
            <Route path="/login" element={<Login />} />
            
            <Route path="/*" element={
              <ProtectedRoute>
                <MainLayout isDarkMode={isDarkMode} toggleTheme={toggleTheme}>
                  <Routes>
                    <Route path="/" element={<Dashboard ignoredFilter="false" showCharts={true} isDarkMode={isDarkMode} />} />
                    <Route path="/ignored" element={<Dashboard ignoredFilter="true" showCharts={false} isDarkMode={isDarkMode} />} />
                    <Route path="/unresolvable" element={<Dashboard ignoredFilter="false" showCharts={false} isDarkMode={isDarkMode} />} /> {/* 可傳入 status filter */}
                    <Route path="/tools/decoder" element={<CertDecoder />} />
                    <Route path="/settings/*" element={<Settings />} />
                  </Routes>
                </MainLayout>
              </ProtectedRoute>
            } />
          </Routes>
        </BrowserRouter>
      </ConfigProvider>
    </QueryClientProvider>
  );
};

export default App;