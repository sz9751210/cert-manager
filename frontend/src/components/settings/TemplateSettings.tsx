// src/components/settings/TemplateSettings.tsx
import React from 'react';
import { Card, Form, Input, Switch, Alert, Collapse, Tabs } from 'antd';
import { VariableCheatSheet, OpVariableCheatSheet, TaskVariableCheatSheet, DEFAULT_TEMPLATES } from './Shared';

const { Panel } = Collapse;
const { TextArea } = Input;

export const TemplateSettings: React.FC = () => {
  return (
    <Card title="訊息模板管理" bordered={false}>
      <Alert message="支援 HTML 標籤 (如 粗體) 與 Go Template 語法" type="info" showIcon style={{ marginBottom: 16 }} />

      <Collapse defaultActiveKey={['expiry']}>
        {/* 1. 最重要的到期通知 */}
        <Panel header="🔔 到期與異常告警 (最重要)" key="expiry">
          <Form.Item
            name="notify_on_expiry"
            valuePropName="checked"
            style={{ marginBottom: 16 }}
            help="關閉後將不再收到過期、無法解析、憑證錯誤等重要告警"
          >
            <Switch checkedChildren="開啟通知" unCheckedChildren="關閉通知" />
          </Form.Item>
          <VariableCheatSheet />
          <Form.Item name="telegram_template" style={{ marginTop: 12 }}>
            <TextArea rows={6} placeholder={DEFAULT_TEMPLATES.expiry} />
          </Form.Item>
        </Panel>

        {/* 2. 操作類通知 */}
        <Panel header="🌱 新增/刪除/更新 通知" key="ops">
          <Tabs items={[
            {
              key: 'add', label: '新增通知', children: (
                <>
                  <Form.Item name="notify_on_add" valuePropName="checked" style={{ marginBottom: 8 }}><Switch checkedChildren="開啟" unCheckedChildren="關閉" /></Form.Item>
                  <OpVariableCheatSheet />
                  <Form.Item name="notify_on_add_tpl"><TextArea rows={4} placeholder={DEFAULT_TEMPLATES.add} /></Form.Item>
                </>
              )
            },
            {
              key: 'del', label: '刪除通知', children: (
                <>
                  <Form.Item name="notify_on_delete" valuePropName="checked" style={{ marginBottom: 8 }}><Switch checkedChildren="開啟" unCheckedChildren="關閉" /></Form.Item>
                  <OpVariableCheatSheet />
                  <Form.Item name="notify_on_delete_tpl"><TextArea rows={4} placeholder={DEFAULT_TEMPLATES.delete} /></Form.Item>
                </>
              )
            },
            {
              key: 'update', label: '設定變更', children: (
                <>
                  <Form.Item name="notify_on_update" valuePropName="checked" style={{ marginBottom: 8 }}><Switch checkedChildren="開啟" unCheckedChildren="關閉" /></Form.Item>
                  <OpVariableCheatSheet />
                  <Form.Item name="notify_on_update_tpl"><TextArea rows={4} placeholder={DEFAULT_TEMPLATES.update} /></Form.Item>
                </>
              )
            },
            {
              key: 'zone_add', label: '新增主域名(Zone)', children: (
                <>
                  <div style={{ marginBottom: 16 }}>
                    <Form.Item
                      name="notify_on_zone_add"
                      valuePropName="checked"
                      style={{ marginBottom: 8 }}
                      label="啟用通知"
                    >
                      <Switch checkedChildren="開啟" unCheckedChildren="關閉" />
                    </Form.Item>
                    <Alert message="當 Cloudflare 同步發現新的主域名時觸發" type="warning" showIcon style={{ marginBottom: 8 }} />
                  </div>

                  <OpVariableCheatSheet />
                  <Form.Item name="notify_on_zone_add_tpl" label="通知模板">
                    <TextArea rows={4} placeholder={DEFAULT_TEMPLATES.zone_add} />
                  </Form.Item>
                </>
              )
            },
            // [修改] Zone 刪除 Tab
            {
              key: 'zone_del', label: '移除主域名(Zone)', children: (
                <>
                  <Form.Item
                    name="notify_on_zone_delete"
                    valuePropName="checked"
                    style={{ marginBottom: 8 }}
                    label="啟用通知"
                  >
                    <Switch checkedChildren="開啟" unCheckedChildren="關閉" />
                  </Form.Item>
                  <OpVariableCheatSheet />
                  <Form.Item name="notify_on_zone_delete_tpl" label="通知模板">
                    <TextArea rows={4} placeholder={DEFAULT_TEMPLATES.zone_delete} />
                  </Form.Item>
                </>
              )
            },
            {
              key: 'renew',
              label: 'SSL 續簽',
              children: (
                <>
                  <Form.Item
                    name="notify_on_renew"
                    valuePropName="checked"
                    style={{ marginBottom: 8 }}
                    label="啟用通知"
                  >
                    <Switch checkedChildren="開啟" unCheckedChildren="關閉" />
                  </Form.Item>
                  <OpVariableCheatSheet />
                  <Form.Item name="notify_on_renew_tpl" label="通知模板">
                    <TextArea rows={4} placeholder={DEFAULT_TEMPLATES.renew} />
                  </Form.Item>
                </>
              )
            }
          ]} />
        </Panel>

        {/* 3. [新增] 任務匯總通知 */}
        <Panel header="📊 排程任務匯總 (Sync/Scan)" key="tasks">
          <Tabs items={[
            {
              key: 'sync', label: 'Cloudflare 同步報告', children: (
                <>
                  <Form.Item name="notify_on_sync_finish" valuePropName="checked" style={{ marginBottom: 8 }}><Switch checkedChildren="開啟" unCheckedChildren="關閉" /></Form.Item>
                  <TaskVariableCheatSheet />
                  <Form.Item name="sync_finish_tpl"><TextArea rows={5} placeholder={DEFAULT_TEMPLATES.sync_finish} /></Form.Item>
                </>
              )
            },
            {
              key: 'scan', label: 'SSL 掃描報告', children: (
                <>
                  <Form.Item name="notify_on_scan_finish" valuePropName="checked" style={{ marginBottom: 8 }}><Switch checkedChildren="開啟" unCheckedChildren="關閉" /></Form.Item>
                  <TaskVariableCheatSheet />
                  <Form.Item name="scan_finish_tpl"><TextArea rows={5} placeholder={DEFAULT_TEMPLATES.scan_finish} /></Form.Item>
                </>
              )
            }
          ]} />
        </Panel>
      </Collapse>
    </Card>
  );
};
