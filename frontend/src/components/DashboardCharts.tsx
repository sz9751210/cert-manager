// src/components/DashboardCharts.tsx
import React, { useMemo } from "react";
import { Card, Col, Row, Statistic } from "antd";
import {
  PieChart,
  Pie,
  Cell,
  Tooltip as RechartsTooltip,
  Legend,
  BarChart,
  Bar,
  XAxis,
  YAxis,
  CartesianGrid, // 記得引入這個
  ResponsiveContainer,
  Label,
  Sector,
} from "recharts";
import type { DashboardStats } from "../services/api";
import {
  SafetyCertificateOutlined,
  StopOutlined,
  CloseOutlined,
  CloseCircleOutlined,
  DisconnectOutlined,
  AlertOutlined,
  ClockCircleOutlined,
  SyncOutlined,
  GlobalOutlined,
  CheckCircleOutlined,
  ApiOutlined,
} from "@ant-design/icons";

// 顏色定義
const COLORS = {
  active: "#52c41a", // Green
  warning: "#faad14", // Orange
  expired: "#ff4d4f", // Red
  unresolvable: "#d4380d", // Dark Orange/Red
  pending: "#1890ff", // Blue
  mismatch: "#cf1322",
  // 如果有其他狀態，可以在這裡補上顏色
};

interface Props {
  stats: DashboardStats | undefined;
  loading: boolean;
  isDarkMode?: boolean;
}

const DashboardCharts: React.FC<Props> = ({ stats, loading, isDarkMode }) => {
  // 如果沒有數據，回傳 null 或 Skeleton (這裡回傳 null)
  if (!stats) return null;

  // 定義圖表文字顏色 (配合深色模式)
  const textColor = isDarkMode ? "#e6f7ff" : "#333";
  // const gridColor = isDarkMode ? "#444" : "#eee"; // 如果需要自訂格線顏色

  // 1. 準備 Pie Chart 資料 (狀態分佈)
  const pieData = useMemo(() => {
    if (!stats) return [];
    return Object.keys(stats.status_counts).map((key) => ({
      name: key,
      value: stats.status_counts[key],
    }));
  }, [stats]);

  // [新增] 自定義圓餅圖標籤渲染函式 (顯示 名稱: 數量)
  const renderCustomizedLabel = (props: any) => {
    const { cx, cy, midAngle, innerRadius, outerRadius, startAngle, endAngle, fill, payload, percent, value, name } = props;
    if (percent < 0.01) return null;
    const RADIAN = Math.PI / 180;
    // 計算三個點：起點(圓餅邊緣)、轉折點、終點(文字位置)
    const radius = innerRadius + (outerRadius - innerRadius) * 0.5;
    const x0 = cx + radius * Math.cos(-midAngle * RADIAN);
    const y0 = cy + radius * Math.sin(-midAngle * RADIAN);

    // 拉長半徑，確保轉折點在圓外
    const sin = Math.sin(-midAngle * RADIAN);
    const cos = Math.cos(-midAngle * RADIAN);
    const sx = cx + (outerRadius + 10) * cos;
    const sy = cy + (outerRadius + 10) * sin;
    const mx = cx + (outerRadius + 30) * cos;
    const my = cy + (outerRadius + 30) * sin;
    const ex = mx + (cos >= 0 ? 1 : -1) * 22;
    const ey = my;
    const textAnchor = cos >= 0 ? 'start' : 'end';
    // 只在比例大於 0 才顯示，避免重疊太嚴重

    return (
      <g>
        {/* 畫折線 */}
        <path d={`M${sx},${sy}L${mx},${my}L${ex},${ey}`} stroke={fill} fill="none" />
        {/* 畫圓點 */}
        <circle cx={ex} cy={ey} r={2} fill={fill} stroke="none" />
        <text
          x={ex + (cos >= 0 ? 1 : -1) * 12}
          y={ey}
          textAnchor={textAnchor}
          fill={textColor}
          dominantBaseline="central"
          fontSize={12}
        >
          {`${name}: ${value} (${(percent * 100).toFixed(0)}%)`}
        </text>
      </g>
    );
  };

  // 2. 準備 Bar Chart 資料 (發行商 Top 5)
  const barData = Object.keys(stats.issuer_counts)
    .map((key) => ({ name: key, count: stats.issuer_counts[key] }))
    .sort((a, b) => b.count - a.count)
    .slice(0, 5); // 只取前 5 名

  // 輔助函式：產生卡片
  const renderCard = (
    title: string,
    value: number | string,
    icon: React.ReactNode,
    color: string,
    suffix?: string,
    prefixColor?: string // icon 顏色
  ) => (
    <Col xs={24} sm={12} md={6}> {/* 響應式：手機全寬，平板半寬，桌機1/4 */}
      <Card
        bordered={false}
        loading={loading}
        bodyStyle={{ padding: "20px 24px" }}
      >
        <Statistic
          title={
            <span
              style={{
                fontSize: "14px",
                color: isDarkMode ? "#aaa" : "#666",
              }}
            >
              {title}
            </span>
          }
          value={value}
          valueStyle={{
            color: color,
            fontWeight: "bold",
            fontSize: "24px",
          }}
          prefix={
            <span style={{ marginRight: 8, fontSize: "20px", color: prefixColor || color }}>{icon}</span>
          }
          suffix={
            <span
              style={{
                fontSize: "12px",
                color: isDarkMode ? "#666" : "#999",
                marginLeft: 4,
              }}
            >
              {suffix}
            </span>
          }
        />
      </Card>
    </Col>
  );

  return (
    <div style={{ marginBottom: 24 }}>

      {/* --- Row 1: 資產概況 (Inventory) --- */}
      <Row gutter={[16, 16]} style={{ marginBottom: 16 }}>
        {/* [新增] 主域名總數 */}
        {renderCard(
          "主域名總數 (Zones)",
          // @ts-ignore
          stats.total_zones || 0,
          <GlobalOutlined />,
          isDarkMode ? "#d3adf7" : "#722ed1", // 紫色
          "個",
          "#722ed1"
        )}

        {/* 監控中子域名 (原 total_domains) */}
        {renderCard(
          "監控中子域名",
          stats.total_domains,
          <SafetyCertificateOutlined />,
          isDarkMode ? "#fff" : "#1890ff", // 藍色
          "個",
          "#1890ff"
        )}

        {/* 狀態正常 */}
        {renderCard(
          "狀態正常",
          stats.status_counts["active"] || 0,
          <CheckCircleOutlined />,
          "#52c41a", // 綠色
          "個健康"
        )}

        {/* [新增] 暫停監控 */}
        {renderCard(
          "暫停監控",
          // @ts-ignore
          stats.ignored_domains || 0,
          <StopOutlined />,
          "#8c8c8c", // 灰色
          "個",
          "#8c8c8c"
        )}
      </Row>

      {/* --- Row 2: 異常與警告 (Alerts) --- */}
      <Row gutter={[16, 16]} style={{ marginBottom: 24 }}>
        {renderCard(
          "無法解析/異常",
          stats.status_counts["unresolvable"] || 0,
          <DisconnectOutlined />,
          "#d4380d",
          "個錯誤"
        )}
        {renderCard(
          "憑證錯誤",
          // @ts-ignore
          stats.mismatch_count || 0,
          <CloseOutlined />,
          "#cf1322",
          "個危險"
        )}
        {renderCard(
          "連線錯誤",
          stats.connection_error || 0,
          <ApiOutlined />,
          "#820014",
          "個錯誤"
        )}
      </Row>

      {/* --- Row 3: 其他狀態 (Info) --- */}
      <Row gutter={[16, 16]} style={{ marginBottom: 24 }}>
        {renderCard(
          "等待掃描",
          stats.status_counts["pending"] || 0,
          <SyncOutlined spin={loading || (stats.status_counts["pending"] || 0) > 0} />,
          "#1890ff",
          "個排隊中"
        )}
        {renderCard(
          "已過期",
          stats.status_counts["expired"] || 0,
          <CloseCircleOutlined />,
          "#ff4d4f",
          "個需續簽"
        )}
        {renderCard(
          "15天內過期",
          stats.expiry_counts["d15"] || 0,
          <AlertOutlined />,
          "#fa541c",
          "個緊急"
        )}
        {/* 這裡留空 2 格，或者可以放其他統計 */}
        <Col xs={24} sm={12} md={12}></Col>
      </Row>

      {/* --- Row 4: 圖表區域 --- */}
      <Row gutter={[16, 16]}>
        <Col xs={24} lg={12}>
          <Card title="健康狀態分佈" bordered={false} style={{ minHeight: 300 }}>
            <ResponsiveContainer width="100%" height={250}>
              <PieChart margin={{ top: 30, right: 30, left: 30, bottom: 10 }}>
                <Pie
                  data={pieData}
                  cx="50%"
                  cy="50%"
                  innerRadius={60}
                  outerRadius={70}
                  paddingAngle={5}
                  dataKey="value"
                  label={renderCustomizedLabel}
                  labelLine={false}
                  isAnimationActive={false}
                >
                  {pieData.map((entry, index) => (
                    <Cell key={`cell-${index}`} fill={COLORS[entry.name as keyof typeof COLORS] || "#888"} />
                  ))}
                  <Label value={stats.total_domains.toString()} position="center" fill={textColor} style={{ fontSize: '24px', fontWeight: 'bold' }} />
                </Pie>
                <RechartsTooltip contentStyle={{ backgroundColor: isDarkMode ? "#1f1f1f" : "#fff", borderColor: isDarkMode ? "#333" : "#ccc", color: textColor }} itemStyle={{ color: textColor }} />
                <Legend wrapperStyle={{ paddingTop: "20px", color: textColor }} />
              </PieChart>
            </ResponsiveContainer>
          </Card>
        </Col>

        <Col xs={24} lg={12}>
          <Card title="SSL 發行商 Top 5" bordered={false} style={{ minHeight: 300 }}>
            <ResponsiveContainer width="100%" height={250}>
              <BarChart data={barData} layout="vertical" margin={{ top: 5, right: 30, left: 0, bottom: 5 }}>
                <CartesianGrid strokeDasharray="3 3" horizontal={false} stroke={isDarkMode ? "#444" : "#eee"} />
                <XAxis type="number" allowDecimals={false} tick={{ fill: textColor }} stroke={isDarkMode ? "#666" : "#ccc"} />
                <YAxis dataKey="name" type="category" width={100} tick={{ fontSize: 12, fill: textColor }} stroke={isDarkMode ? "#666" : "#ccc"} tickFormatter={(value) => value.length > 25 ? value.substring(0, 25) + "..." : value} />
                <RechartsTooltip cursor={{ fill: isDarkMode ? "rgba(255,255,255,0.1)" : "#f5f5f5" }} contentStyle={{ backgroundColor: isDarkMode ? "#1f1f1f" : "#fff", borderColor: isDarkMode ? "#333" : "#ccc", color: textColor }} />
                <Bar dataKey="count" fill="#1890ff" barSize={20} radius={[0, 4, 4, 0]} name="數量" />
              </BarChart>
            </ResponsiveContainer>
          </Card>
        </Col>
      </Row>
    </div>
  );
};

export default DashboardCharts;