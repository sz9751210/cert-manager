// src/components/DomainList/columns.tsx
import React from 'react';
import { Tag, Tooltip, Switch, Button, Space } from "antd";
import {
  CheckCircleOutlined, WarningOutlined, ApiOutlined,
  ReloadOutlined, StopOutlined, CloudServerOutlined,
  GlobalOutlined, SettingOutlined, InfoCircleOutlined, CloseCircleOutlined,DisconnectOutlined
} from "@ant-design/icons";
import dayjs from "dayjs";
import type { ColumnsType } from "antd/es/table";
import type { SSLCertificate } from "../../types";

// --- 小組件：狀態標籤 ---
const StatusBadge = ({ status, isIgnored }: { status: string; isIgnored: boolean }) => {
  if (isIgnored) return <Tag icon={<StopOutlined />}>已忽略</Tag>;

  const config: Record<string, { color: string; text: string; icon: React.ReactNode }> = {
    active: { color: "success", text: "正常", icon: <CheckCircleOutlined /> },
    warning: { color: "warning", text: "即將過期", icon: <WarningOutlined /> },
    expired: { color: "error", text: "已過期", icon: <CloseCircleOutlined /> },
    unresolvable: { color: "red", text: "無法解析", icon: <DisconnectOutlined /> },
    connection_error: { color: "red", text: "連線錯誤", icon: <ApiOutlined /> },
    pending: { color: "processing", text: "等待中", icon: <ReloadOutlined spin /> },
  };

  const { color, text, icon } = config[status] || { color: "default", text: status, icon: null };
  return <Tag icon={icon} color={color}>{text}</Tag>;
};

// --- 小組件：HTTP 狀態 ---
const HttpStatusTag = ({ code, latency }: { code: number; latency: number }) => {
  if (!code) return <Tag color="red">Down</Tag>;
  let color = "error";
  if (code >= 200 && code < 300) color = "success";
  else if (code >= 300 && code < 400) color = "blue";
  else if (code >= 400 && code < 500) color = "orange";

  return (
    <Tooltip title={`${latency}ms`}>
      <Tag color={color}>{code}</Tag>
    </Tooltip>
  );
};

interface ColumnProps {
  onToggleIgnore: (id: string, val: boolean) => void;
  onEdit: (record: SSLCertificate) => void;
  onScan: (id: string) => void;
  onDetail: (record: SSLCertificate) => void;
  ignoreLoading: boolean;
}

export const getDomainColumns = ({
  onToggleIgnore, onEdit, onScan, onDetail, ignoreLoading
}: ColumnProps): ColumnsType<SSLCertificate> => [
    {
      title: "狀態",
      dataIndex: "status",
      width: 110,
      render: (status, record) => <StatusBadge status={status} isIgnored={record.is_ignored} />,
    },
    {
      title: "域名",
      dataIndex: "domain_name",
      render: (text, record) => (
        <Space>
          <span style={{ fontWeight: 600, color: record.is_ignored ? "#999" : "inherit" }}>{text}</span>
          {!record.is_match && !record.is_ignored && record.status !== 'unresolvable' && (
            <Tooltip title={`危險：憑證名稱不符！(SANs: ${record.sans?.[0] || 'Unknown'})`}>
              <Tag color="red" icon={<StopOutlined />}>錯置</Tag>
            </Tooltip>
          )}
          {record.is_proxied && <Tooltip title="Proxy ON"><CloudServerOutlined style={{ color: "#fa8c16" }} /></Tooltip>}
          {record.cf_comment && <Tooltip title={record.cf_comment} color="blue"> <InfoCircleOutlined style={{ color: '#1890ff', cursor: 'pointer', marginLeft: 4 }} /></Tooltip>}
        </Space>
      ),
    },
    {
      title: "HTTP",
      dataIndex: "http_status_code",
      width: 80,
      render: (code, record) => record.is_ignored ? "-" : <HttpStatusTag code={code} latency={record.latency} />,
    },
    {
      title: "剩餘天數",
      dataIndex: "days_remaining",
      width: 120,
      sorter: true,
      render: (days, record) => {
        if (record.is_ignored || record.status === 'unresolvable') return <span style={{ color: "#ccc" }}>-</span>;
        const color = days < 7 ? "red" : days < 16 ? "orange" : "green";
        return <span style={{ color, fontWeight: "bold" }}>{days} 天</span>;
      },
    },
    {
      title: "網域到期",
      dataIndex: "domain_days_left",
      width: 120,
      sorter: true,
      render: (days, record) => {
        if (record.is_ignored || !record.domain_expiry_date) return "-";
        const color = days < 7 ? "red" : days < 16 ? "orange" : "green";
        return (
          <Tooltip title={`到期日: ${dayjs(record.domain_expiry_date).format("YYYY-MM-DD")}`}>
            <Tag icon={<GlobalOutlined />} color={color}>{days} 天</Tag>
          </Tooltip>
        );
      },
    },
    {
      title: "上次檢查",
      dataIndex: "last_check_time",
      width: 140,
      sorter: true, // Enable sorting UI
      render: (date: string) => {
        if (!date || date.startsWith("0001")) return "-";
        return (
          <Tooltip title={dayjs(date).format("YYYY-MM-DD HH:mm:ss")}>
            <span>{dayjs(date).format("MM-DD HH:mm")}</span>
          </Tooltip>
        );
      },
    },
    {
      title: "監控中",
      dataIndex: "is_ignored",
      width: 80,
      render: (ignored, record) => (
        <Switch
          size="small"
          checked={!ignored}
          onChange={(checked) => onToggleIgnore(record.id, !checked)}
          loading={ignoreLoading}
        />
      ),
    },
    {
      title: "操作",
      key: "action",
      width: 150,
      render: (_, record) => (
        <Space>
          <Button size="small" icon={<SettingOutlined />} onClick={() => onEdit(record)} />
          <Tooltip title="重新掃描">
            <Button size="small" icon={<ReloadOutlined />} onClick={() => onScan(record.id)} />
          </Tooltip>
          <Button size="small" type="text" icon={<InfoCircleOutlined />} onClick={() => onDetail(record)}>詳情</Button>
        </Space>
      ),
    },
  ];