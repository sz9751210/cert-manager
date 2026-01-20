import React, { useEffect } from 'react';
import { Modal, Form, InputNumber } from 'antd';
import type { SSLCertificate } from '../../types';

interface Props {
  open: boolean;
  record: SSLCertificate | null;
  onClose: () => void;
  onSubmit: (id: string, values: { port: number }) => void;
  confirmLoading: boolean;
}

export const EditDomainModal: React.FC<Props> = ({ open, record, onClose, onSubmit, confirmLoading }) => {
  const [form] = Form.useForm();

  // 當開啟 Modal 時，將目前的資料填入表單
  useEffect(() => {
    if (open && record) {
      form.setFieldsValue({
        port: record.port || 443, // 預設 443
      });
    }
  }, [open, record, form]);

  const handleOk = async () => {
    try {
      const values = await form.validateFields();
      if (record) {
        // 呼叫父層的提交函式
        onSubmit(record.id, {
          port: values.port,
        });
      }
    } catch (error) {
      // 表單驗證失敗
    }
  };

  return (
    <Modal
      title={`編輯設定: ${record?.domain_name}`}
      open={open}
      onOk={handleOk}
      onCancel={onClose}
      confirmLoading={confirmLoading}
      destroyOnClose
    >
      <Form form={form} layout="vertical">
        <Form.Item
          name="port"
          label="監控 Port"
          rules={[{ required: true, message: '請輸入 Port' }]}
          help="預設為 443，若目標伺服器使用非標準 Port 請修改"
        >
          <InputNumber min={1} max={65535} style={{ width: '100%' }} />
        </Form.Item>
      </Form>
    </Modal>
  );
};