// src/components/settings/ChannelSettings.tsx
import React from 'react';
import { Card, Tabs, Form, Input, Switch, Button, Alert, Collapse, Tag, Space, Typography } from 'antd';
import { ThunderboltOutlined, CloudServerOutlined, SafetyCertificateOutlined, ReloadOutlined } from '@ant-design/icons';
import { DEFAULT_TEMPLATES } from './Shared';

const { Panel } = Collapse;
const { TextArea } = Input;
const { Text } = Typography;

interface ChannelSettingsProps {
  formInstance: any; // 用於獲取當前欄位值
}

export const ChannelSettings: React.FC<ChannelSettingsProps> = ({ formInstance }) => {
  return (
    <Card title="連線與排程設定" bordered={false} style={{ marginBottom: 24 }}>
      <Tabs
        defaultActiveKey="telegram"
        type="card"
        items={[
          {
            key: "telegram",
            label: <span><ThunderboltOutlined /> Telegram</span>,
            children: (
              <div style={{ marginTop: 16 }}>
                <Form.Item name="telegram_enabled" label="啟用 Telegram 通知" valuePropName="checked">
                  <Switch />
                </Form.Item>
                <Form.Item noStyle shouldUpdate={(prev, curr) => prev.telegram_enabled !== curr.telegram_enabled}>
                  {({ getFieldValue }) => getFieldValue("telegram_enabled") && (
                    <>
                      <Form.Item label="Bot Token" name="telegram_bot_token" rules={[{ required: true, message: '請輸入 Bot Token' }]}>
                        <Input.Password placeholder="123456789:ABCdef..." />
                      </Form.Item>
                      <Form.Item label="Chat ID" name="telegram_chat_id" rules={[{ required: true, message: '請輸入 Chat ID' }]}>
                        <Input placeholder="-987654321" />
                      </Form.Item>
                    </>
                  )}
                </Form.Item>
              </div>
            ),
          },
          {
            key: "webhook",
            label: <span><CloudServerOutlined /> Webhook</span>,
            children: (
              <div style={{ marginTop: 16 }}>
                <Form.Item name="webhook_enabled" label="啟用 Webhook" valuePropName="checked">
                  <Switch />
                </Form.Item>
                <Form.Item noStyle shouldUpdate={(prev, curr) => prev.webhook_enabled !== curr.webhook_enabled}>
                  {({ getFieldValue }) => getFieldValue("webhook_enabled") && (
                    <>
                      <Form.Item label="Webhook URL" name="webhook_url" rules={[{ required: true }]}>
                        <Input placeholder="https://hooks.slack.com/..." />
                      </Form.Item>
                      <div style={{ display: 'flex', gap: 16 }}>
                        <Form.Item label="Auth User (選填)" name="webhook_user" style={{ flex: 1 }}>
                          <Input placeholder="Username" />
                        </Form.Item>
                        <Form.Item label="Auth Password (選填)" name="webhook_password" style={{ flex: 1 }}>
                          <Input.Password placeholder="Password" />
                        </Form.Item>
                      </div>
                    </>
                  )}
                </Form.Item>
              </div>
            ),
          },
          {
            key: "cron",
            label: <span><ReloadOutlined /> 排程與自動化</span>,
            children: (
              <div style={{ marginTop: 16 }}>
                <Alert message="Cron 範例：'0 3 * * *' (每天03:00)" type="info" showIcon style={{ marginBottom: 16 }} />
                <Collapse defaultActiveKey={['sync', 'scan']}>
                  <Panel header="☁️ Cloudflare 自動同步" key="sync">
                    <div style={{ display: 'flex', gap: 16 }}>
                      <Form.Item name="sync_enabled" valuePropName="checked" label="啟用">
                        <Switch />
                      </Form.Item>
                      <Form.Item name="sync_schedule" label="Cron 表達式" style={{ flex: 1 }}>
                        <Input placeholder="0 3 * * *" />
                      </Form.Item>
                    </div>
                    <Alert message="通知開關與模板請至下方「訊息模板管理」設定" type="info" showIcon style={{ marginTop: 8 }} />
                  </Panel>
                  <Panel header="🔍 SSL 定期掃描" key="scan">
                    <div style={{ display: 'flex', gap: 16 }}>
                      <Form.Item name="scan_enabled" valuePropName="checked" label="啟用">
                        <Switch />
                      </Form.Item>
                      <Form.Item name="scan_schedule" label="Cron 表達式" style={{ flex: 1 }}>
                        <Input placeholder="0 4 * * *" />
                      </Form.Item>
                    </div>
                  </Panel>
                </Collapse>
              </div>
            )
          }
        ]}
      />
    </Card>
  );
};

export default ChannelSettings;
