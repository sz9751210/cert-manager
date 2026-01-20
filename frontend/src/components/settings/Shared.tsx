// src/components/settings/Shared.tsx
import React from 'react';
import { Tag, Typography, theme, message, Space } from 'antd';

const { Text } = Typography;

// 定義預設模板常數 (給 Settings.tsx 初始化用)
export const DEFAULT_TEMPLATES = {
  expiry: `⚠️ [監控告警]\n域名: {{.Domain}}\n狀態: {{.Status}}\n剩餘: {{.Days}} 天\n到期: {{.ExpiryDate}}\nIP: {{.IP}}`,
  add: `🌱 [新增監控]\n域名: {{.Domain}}\n時間: {{.Time}}\n備註: {{.Details}}`,
  delete: `🗑 [移除監控]\n域名: {{.Domain}}\n時間: {{.Time}}\n備註: {{.Details}}`,
  renew: `♻️ [SSL 續簽]\n域名: {{.Domain}}\n時間: {{.Time}}\n結果: {{.Details}}`,
  update: `🛠 [DNS 變更通知]\n域名: {{.Domain}}\n時間: {{.Time}}\n變更內容: {{.Details}}`,

  // [新增] Zone (主域名) 類
  zone_add: `🌍 <b>[新增主域名]</b>\nZone: {{.Domain}}\n詳情: {{.Details}}`,
  zone_delete: `💥 <b>[移除主域名]</b>\nZone: {{.Domain}}\n詳情: {{.Details}}`,

  // [新增] 任務匯總類
  sync_finish: `☁️ [Cloudflare 同步完成]\n新增: {{.Added}} | 更新: {{.Updated}}\n刪除: {{.Deleted}} | 略過: {{.Skipped}}\n耗時: {{.Duration}}{{.Details}}`,
  scan_finish: `🔍 [SSL 掃描完成]\n總數: {{.Total}}\n正常: {{.Active}}\n過期: {{.Expired}}\n異常: {{.Warning}}\n耗時: {{.Duration}}`,
};

// 一般變數說明
export const VariableCheatSheet: React.FC = () => {
  const { token } = theme.useToken();
  return (
    <div style={{ marginTop: 8, padding: 8, background: token.colorFillAlter, borderRadius: token.borderRadius, border: `1px solid ${token.colorBorderSecondary}` }}>
      <Text type="secondary" style={{ fontSize: 12 }}>可用變數 (點擊複製): </Text>
      <div style={{ marginTop: 4, display: "flex", flexWrap: "wrap", gap: 4 }}>
        {["{{.Domain}}", "{{.Days}}", "{{.ExpiryDate}}", "{{.Status}}", "{{.Issuer}}", "{{.IP}}", "{{.Record}}", "{{.TLS}}", "{{.HTTPCode}}"].map((v) => (
          <Tag key={v} style={{ cursor: "pointer" }} onClick={() => { navigator.clipboard.writeText(v); message.success(`已複製 ${v}`); }}>{v}</Tag>
        ))}
      </div>
    </div>
  );
};

// 操作類變數說明 (通用於 Add/Delete/Update/Zone)
export const OpVariableCheatSheet: React.FC = () => {
  const { token } = theme.useToken();
  return (
    <div style={{ margin: "8px 0", padding: 8, background: token.colorFillAlter, borderRadius: token.borderRadius, border: `1px solid ${token.colorBorderSecondary}` }}>
      <Text type="secondary" style={{ fontSize: 12 }}>可用變數: </Text>
      <Space size={4} wrap>
        {["{{.Action}}", "{{.Domain}}", "{{.Details}}", "{{.Time}}"].map((v) => (
          <Tag key={v} style={{ cursor: "pointer" }} onClick={() => { navigator.clipboard.writeText(v); message.success(`已複製 ${v}`); }}>{v}</Tag>
        ))}
      </Space>
    </div>
  );
};

// [新增] 任務匯總類變數說明
export const TaskVariableCheatSheet: React.FC = () => {
  const { token } = theme.useToken();
  return (
    <div style={{ margin: "8px 0", padding: 8, background: token.colorFillAlter, borderRadius: token.borderRadius, border: `1px solid ${token.colorBorderSecondary}` }}>
      <Text type="secondary" style={{ fontSize: 12 }}>匯總變數: </Text>
      <Space size={4} wrap>
        {["{{.Total}}", "{{.Added}}", "{{.Updated}}", "{{.Deleted}}", "{{.Active}}", "{{.Expired}}", "{{.Duration}}"].map((v) => (
          <Tag key={v} style={{ cursor: "pointer" }} onClick={() => { navigator.clipboard.writeText(v); message.success(`已複製 ${v}`); }}>{v}</Tag>
        ))}
      </Space>
    </div>
  );
};