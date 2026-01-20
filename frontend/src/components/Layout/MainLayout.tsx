// src/components/Layout/MainLayout.tsx
import React from "react";
import { Layout, Menu, theme, Typography, Button, Space, Tooltip } from "antd";
import {
  DashboardOutlined,
  StopOutlined,
  SafetyCertificateOutlined,
  SettingOutlined,
  BulbOutlined,
  BulbFilled,
  CloudSyncOutlined,
  ReloadOutlined,
  GlobalOutlined,
} from "@ant-design/icons";
import { Link, useLocation } from "react-router-dom";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { message } from "antd";
import { syncDomains, scanDomains } from "../../services/api"; // 確保路徑正確指向 api.ts

const { Header, Content, Sider } = Layout;
const { Title } = Typography;

interface MainLayoutProps {
  isDarkMode: boolean;
  toggleTheme: () => void;
  children: React.ReactNode; // 這是關鍵：用來顯示路由內容
}

const MainLayout: React.FC<MainLayoutProps> = ({
  isDarkMode,
  toggleTheme,
  children,
}) => {
  const {
    token: { colorBgContainer },
  } = theme.useToken();
  const location = useLocation();
  const queryClient = useQueryClient();

  // --- 全域按鈕邏輯 (同步與掃描) ---
  // 因為這些按鈕在 Header 上，屬於 Layout 的一部分
  const syncMutation = useMutation({
    mutationFn: syncDomains,
    onSuccess: () => {
      message.success("同步請求已發送");
      queryClient.invalidateQueries({ queryKey: ["domains"] });
    },
    onError: () => message.error("同步失敗"),
  });

  const scanMutation = useMutation({
    mutationFn: scanDomains,
    onSuccess: () => message.success("背景掃描已啟動"),
  });

  // 根據路徑決定標題
  let pageTitle = "監控儀表板";
  if (location.pathname === "/unresolvable") pageTitle = "DNS 解析異常列表";
  if (location.pathname === "/ignored") pageTitle = "已停止監控列表";
  if (location.pathname === "/tools/decoder") pageTitle = "憑證解碼器";
  if (location.pathname.includes("/settings")) pageTitle = "系統設定";

  return (
    <Layout style={{ minHeight: "100vh" }}>
      <Sider
        width={220}
        theme={isDarkMode ? "dark" : "dark"}
        style={{ background: isDarkMode ? "#001529" : "#001529" }}
      >
        <div
          style={{
            height: "64px",
            margin: "16px",
            display: "flex",
            alignItems: "center",
            justifyContent: "center",
          }}
        >
          <SafetyCertificateOutlined
            style={{ fontSize: "24px", color: "#1890ff", marginRight: "8px" }}
          />
          <span
            style={{ color: "white", fontSize: "18px", fontWeight: "bold" }}
          >
            CertManager
          </span>
        </div>
        <Menu
          theme="dark"
          mode="inline"
          selectedKeys={[location.pathname]}
          defaultOpenKeys={["settings"]}
          items={[
            {
              key: "/",
              icon: <DashboardOutlined />,
              label: <Link to="/">監控儀表板</Link>,
            },
            {
              key: "/ignored",
              icon: <StopOutlined />,
              label: <Link to="/ignored">已停止監控</Link>,
            },
            {
              key: "/tools/decoder",
              icon: <SafetyCertificateOutlined />,
              label: <Link to="/tools/decoder">憑證解碼器</Link>,
            },
            {
              key: "scanner",
              icon: <GlobalOutlined />,
              label: <Link to="/tools/scanner">域名檢測工具</Link>,
            },
            {
              key: "settings",
              icon: <SettingOutlined />,
              label: "系統設定",
              children: [
                {
                  key: "/settings/channels",
                  label: <Link to="/settings/channels">通知管道</Link>,
                },
                {
                  key: "/settings/templates",
                  label: <Link to="/settings/templates">模板設定</Link>,
                },
              ],
            },
          ]}
        />
      </Sider>
      <Layout>
        <Header
          style={{
            padding: "0 24px",
            background: colorBgContainer,
            display: "flex",
            alignItems: "center",
            justifyContent: "space-between",
          }}
        >
          <Title level={4} style={{ margin: 0 }}>
            {pageTitle}
          </Title>
          <Space>
            <Tooltip title="切換深色/淺色模式">
              <Button
                shape="circle"
                icon={isDarkMode ? <BulbFilled /> : <BulbOutlined />}
                onClick={toggleTheme}
              />
            </Tooltip>

            <Button
              icon={<CloudSyncOutlined />}
              onClick={() => syncMutation.mutate()}
              loading={syncMutation.isPending}
            >
              同步 CF
            </Button>
            <Button
              type="primary"
              icon={<ReloadOutlined />}
              onClick={() => scanMutation.mutate()}
              loading={scanMutation.isPending}
            >
              重新掃描
            </Button>
          </Space>
        </Header>
        <Content style={{ margin: "24px 16px", padding: 24, minHeight: 280 }}>
          {/* 這裡是用來渲染 Dashboard, Settings 等頁面的地方 */}
          {children}
        </Content>
      </Layout>
    </Layout>
  );
};

export default MainLayout;
