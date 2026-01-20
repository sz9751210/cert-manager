import React, { useEffect } from "react";
import { Form, message, Space, Typography, Button, Card } from "antd";
import { CheckCircleOutlined, NotificationOutlined } from '@ant-design/icons';
import { useQuery, useMutation } from "@tanstack/react-query";
import { useLocation } from "react-router-dom";

// 請確保這些路徑正確，並且該檔案有 export 對應的函式
import { getSettings, saveSettings, testNotification } from "../services/api";

// 引入子組件 (請確保您有建立這些檔案)
import { DEFAULT_TEMPLATES } from "../components/settings/Shared";
import { ChannelSettings } from "../components/settings/ChannelSettings";
import { TemplateSettings } from "../components/settings/TemplateSettings";

const { Title } = Typography;

const Settings: React.FC = () => {
  const [form] = Form.useForm();
  const location = useLocation();

  // 根據 URL 判斷要顯示哪個區塊
  const isTemplatePage = location.pathname.includes("/settings/templates");
  // 預設或是 channels 頁面都顯示管道設定
  const isChannelPage = location.pathname.includes("/settings/channels") || !isTemplatePage;

  // 1. 讀取後端設定
  const { data: settings, isLoading } = useQuery({
    queryKey: ["settings"],
    queryFn: getSettings,
  });

  // 2. 初始化 Form 資料
  useEffect(() => {
    if (settings) {
      form.setFieldsValue({
        ...settings,
        notify_on_expiry: settings.notify_on_expiry ?? true,
        // 如果後端是空字串，就用預設模板填入，方便使用者查看
        telegram_template: settings.telegram_template || DEFAULT_TEMPLATES.expiry,
        notify_on_add_tpl: settings.notify_on_add_tpl || DEFAULT_TEMPLATES.add,
        notify_on_delete_tpl: settings.notify_on_delete_tpl || DEFAULT_TEMPLATES.delete,
        notify_on_renew_tpl: settings.notify_on_renew_tpl || DEFAULT_TEMPLATES.renew,
        notify_on_update_tpl: settings.notify_on_update_tpl || DEFAULT_TEMPLATES.update,
        sync_finish_tpl: settings.sync_finish_tpl || DEFAULT_TEMPLATES.sync_finish,
        scan_finish_tpl: settings.scan_finish_tpl || DEFAULT_TEMPLATES.scan_finish,
      });
    } else {
      // 第一次載入如果為空，給予預設值
      form.setFieldsValue({
        notify_on_expiry: true,
        telegram_template: DEFAULT_TEMPLATES.expiry,
      });
    }
  }, [settings, form]);

  // 3. API Actions
  const saveMutation = useMutation({
    mutationFn: (values: any) => saveSettings(values),
    onSuccess: () => message.success("設定已儲存"),
  });

  const testMutation = useMutation({
    mutationFn: () => testNotification(form.getFieldsValue()),
    onSuccess: () => message.success("測試訊息已發送"),
    onError: () => message.error("測試失敗，請檢查 Token 或 URL"),
  });

  if (isLoading) return <div>設定載入中...</div>;

  return (
    <div style={{ maxWidth: 1000, margin: "0 auto" }}>
      <Space direction="vertical" size="large" style={{ width: "100%" }}>
        <Title level={3}>
          {isTemplatePage ? "📝 通知模板設定" : "📡 通知管道與排程"}
        </Title>

        <Form
          layout="vertical"
          form={form}
          onFinish={(v) => saveMutation.mutate(v)}
          initialValues={{ webhook_enabled: false, telegram_enabled: false }}
        >
          {/* 技巧：使用 display: none 來切換顯示，確保表單資料不會因為組件卸載而遺失 */}

          <div style={{ display: isChannelPage ? 'block' : 'none' }}>
            <ChannelSettings formInstance={form} />
          </div>
          
          <div style={{ display: isTemplatePage ? 'block' : 'none' }}>
            <TemplateSettings />
          </div>

          {/* 底部固定操作列 */}
          <Card bordered={false} style={{ marginTop: 24, textAlign: 'right' }}>
            <Space>
              <Button onClick={() => testMutation.mutate()} loading={testMutation.isPending} size="large" icon={<NotificationOutlined />}>
                發送測試訊息
              </Button>
              <Button type="primary" htmlType="submit" loading={saveMutation.isPending} size="large" icon={<CheckCircleOutlined />}>
                儲存所有設定
              </Button>
            </Space>
          </Card>

        </Form>
      </Space>
    </div>
  );
};

// [重要] 必須加上這一行，App.tsx 才能使用 import Settings from ...
export default Settings;