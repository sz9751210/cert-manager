// src/pages/DomainScanner.tsx
import React, { useState } from 'react';
import { Card, Input, InputNumber, Button, Descriptions, Tag, message, Divider, Space, Badge } from 'antd';
import { GlobalOutlined, SearchOutlined, SafetyCertificateOutlined, CloudServerOutlined, ClockCircleOutlined } from '@ant-design/icons';
import { inspectDomain } from '../services/api';
import type { SSLCertificate } from '../types';

const DomainScanner: React.FC = () => {
  const [domain, setDomain] = useState('');
  const [port, setPort] = useState<number>(443);
  const [loading, setLoading] = useState(false);
  const [data, setData] = useState<SSLCertificate | null>(null);

  const handleScan = async () => {
    if (!domain) {
      message.warning('請輸入網域名稱');
      return;
    }

    setLoading(true);
    setData(null); // 清空舊資料
    try {
      const result = await inspectDomain(domain, port);
      setData(result);
      message.success('查詢成功');
    } catch (error: any) {
      message.error(error.response?.data?.error || '查詢失敗');
    } finally {
      setLoading(false);
    }
  };

  // 輔助函式：狀態標籤
  const getStatusTag = (status: string) => {
    switch (status) {
      case 'active': return <Tag color="success">有效 (Active)</Tag>;
      case 'expired': return <Tag color="error">已過期 (Expired)</Tag>;
      case 'warning': return <Tag color="warning">即將過期 (Warning)</Tag>;
      case 'unresolvable': return <Tag color="default">無法解析 (Unresolvable)</Tag>;
      default: return <Tag>{status}</Tag>;
    }
  };

  return (
    <div style={{ padding: '24px', maxWidth: '900px', margin: '0 auto' }}>
      <Card title={<><GlobalOutlined /> 域名即時檢測工具</>} bordered={false}>
        <div style={{ display: 'flex', gap: 16, marginBottom: 24 }}>
          <Input 
            prefix={<GlobalOutlined style={{ color: '#bfbfbf' }} />}
            placeholder="請輸入網域 (例如: google.com)" 
            value={domain}
            onChange={e => setDomain(e.target.value)}
            onPressEnter={handleScan}
            size="large"
          />
          <InputNumber 
            addonBefore="Port"
            defaultValue={443}
            value={port}
            onChange={val => setPort(val || 443)}
            size="large"
            style={{ width: 120 }}
          />
          <Button type="primary" icon={<SearchOutlined />} loading={loading} onClick={handleScan} size="large">
            查詢
          </Button>
        </div>

        {data && (
          <div style={{ animation: 'fadeIn 0.5s' }}>
            {/* 1. 摘要狀態區塊 */}
            <Card type="inner" title="檢測報告摘要" style={{ marginBottom: 16 }}>
                <Descriptions column={{ xs: 1, sm: 2, md: 3 }}>
                    <Descriptions.Item label="SSL 狀態">
                        {getStatusTag(data.status)}
                    </Descriptions.Item>
                    <Descriptions.Item label="連線延遲">
                        <Tag color={data.latency < 200 ? 'green' : 'orange'}>{data.latency} ms</Tag>
                    </Descriptions.Item>
                    <Descriptions.Item label="HTTP 狀態碼">
                        {data.http_status_code ? <Tag color="blue">{data.http_status_code}</Tag> : <Tag>N/A</Tag>}
                    </Descriptions.Item>
                </Descriptions>
            </Card>

            <Divider orientation="left"><SafetyCertificateOutlined /> SSL 憑證資訊</Divider>
            
            <Descriptions bordered column={1} size="small" labelStyle={{ width: '180px' }}>
                <Descriptions.Item label="通用名稱 (CN)">
                    <Space>
                        <span style={{ fontWeight: 'bold' }}>{data.domain_name}</span>
                        {data.is_match ? <Tag color="green">域名匹配</Tag> : <Tag color="red">域名不匹配</Tag>}
                    </Space>
                </Descriptions.Item>
                <Descriptions.Item label="發行機構 (Issuer)">{data.issuer}</Descriptions.Item>
                <Descriptions.Item label="有效期 (Validity)">
                    {new Date(data.not_before).toLocaleDateString()} ~ {new Date(data.not_after).toLocaleDateString()}
                </Descriptions.Item>
                <Descriptions.Item label="SSL 剩餘天數">
                    <span style={{ 
                        color: data.days_remaining < 30 ? 'red' : 'green', 
                        fontWeight: 'bold', fontSize: 16 
                    }}>
                        {data.days_remaining} 天
                    </span>
                </Descriptions.Item>
                <Descriptions.Item label="SANs (多域名)">
                    <Space wrap>
                        {data.sans && data.sans.slice(0, 10).map(d => <Tag key={d}>{d}</Tag>)}
                        {data.sans && data.sans.length > 10 && <Tag>...及其他 {data.sans.length - 10} 個</Tag>}
                    </Space>
                </Descriptions.Item>
                <Descriptions.Item label="TLS 版本">{data.tls_version}</Descriptions.Item>
            </Descriptions>

            <Divider orientation="left"><ClockCircleOutlined /> 網域註冊資訊 (WHOIS)</Divider>
            
            <Descriptions bordered column={1} size="small" labelStyle={{ width: '180px' }}>
                 <Descriptions.Item label="網域到期日">
                    {data.domain_expiry_date && !data.domain_expiry_date.startsWith('0001') ? (
                        <span>{new Date(data.domain_expiry_date).toLocaleDateString()}</span>
                    ) : <span style={{ color: '#999' }}>無法取得</span>}
                 </Descriptions.Item>
                 <Descriptions.Item label="網域剩餘天數">
                    {data.domain_days_left > 0 ? (
                        <span style={{ color: data.domain_days_left < 60 ? 'orange' : 'green', fontWeight: 'bold' }}>
                            {data.domain_days_left} 天
                        </span>
                    ) : <span style={{ color: '#999' }}>--</span>}
                 </Descriptions.Item>
            </Descriptions>

            <Divider orientation="left"><CloudServerOutlined /> 網路解析資訊</Divider>
            
            <Descriptions bordered column={1} size="small" labelStyle={{ width: '180px' }}>
                 <Descriptions.Item label="解析紀錄 (Resolved)">
                    {data.resolved_record ? <Tag color="geekblue">{data.resolved_record}</Tag> : '無'}
                 </Descriptions.Item>
                 <Descriptions.Item label="解析 IP 列表">
                    <Space wrap>
                        {data.resolved_ips && data.resolved_ips.map(ip => <Tag key={ip}>{ip}</Tag>)}
                    </Space>
                 </Descriptions.Item>
            </Descriptions>
          </div>
        )}
      </Card>
    </div>
  );
};

export default DomainScanner;
