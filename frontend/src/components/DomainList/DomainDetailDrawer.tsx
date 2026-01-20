import React from 'react';
import {
  Drawer, Descriptions, Tag, Typography, Space, Divider, Steps, Tooltip, Badge, theme
} from 'antd';
import {
  CloudServerOutlined,
  GlobalOutlined,
  CheckCircleFilled,
  SafetyCertificateOutlined,
  ClockCircleOutlined,
  BankOutlined,
  NumberOutlined
} from '@ant-design/icons';
import dayjs from 'dayjs';
import type { SSLCertificate } from '../../types';

const { Text } = Typography;

interface Props {
  open: boolean;
  onClose: () => void;
  record: SSLCertificate | null;
}

// [Helper Component] Compact IP List
const IpList = ({ ips }: { ips: string[] }) => {
  if (!ips || ips.length === 0) return <Tag color="red">尚未解析</Tag>;

  // Split IPv4 and IPv6 for better organization (Optional, but looks pro)
  const ipv4 = ips.filter(ip => !ip.includes(':'));
  const ipv6 = ips.filter(ip => ip.includes(':'));

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: '4px', marginTop: '4px' }}>
      {/* Render IPv4 first (usually more relevant) */}
      {ipv4.length > 0 && (
        <Space wrap size={[4, 4]}>
          <Text type="secondary" style={{ fontSize: '12px', width: '30px' }}>IPv4</Text>
          {ipv4.map(ip => (
            <Tag key={ip} color="geekblue" style={{ fontFamily: 'monospace' }}>{ip}</Tag>
          ))}
        </Space>
      )}

      {/* Render IPv6 on new lines because they are long */}
      {ipv6.length > 0 && (
        <div style={{ display: 'flex', flexDirection: 'column', gap: '2px' }}>
          <Space align="start">
            <Text type="secondary" style={{ fontSize: '12px', width: '30px', marginTop: '2px' }}>IPv6</Text>
            <div style={{ display: 'flex', flexDirection: 'column', gap: '2px' }}>
              {ipv6.map(ip => (
                <Tag key={ip} color="purple" style={{ fontFamily: 'monospace', width: 'fit-content' }}>
                  {ip}
                </Tag>
              ))}
            </div>
          </Space>
        </div>
      )}
    </div>
  );
};

export const DomainDetailDrawer: React.FC<Props> = ({ open, onClose, record }) => {
  if (!record) return null;
  const { token } = theme.useToken();
  // 檢查當前域名是否在 SANs 列表中 (用於高亮顯示)
  const isInSans = (san: string) => {
    // 簡單的字串比對，實務上可能需要處理 wildcard (*.example.com)
    return san === record.domain_name ||
      (san.startsWith('*.') && record.domain_name.endsWith(san.slice(2)));
  };

  return (
    <Drawer
      title={
        <Space>
          <SafetyCertificateOutlined style={{ color: '#52c41a' }} />
          <Text strong>域名詳細資訊</Text>
        </Space>
      }
      placement="right"
      width={640} // 稍微加寬一點讓 Steps 更好看
      onClose={onClose}
      open={open}
    >
      {/* 1. DNS 解析路徑 (之前優化的 Steps) */}
      <Descriptions title="DNS 解析路徑" column={1} bordered size="small" layout="vertical">
        <Descriptions.Item>
          <div style={{ padding: '16px 8px 8px 8px' }}>
            <Steps
              direction="vertical" // [Change 1] Switch to Vertical Steps for better mobile/narrow layout support
              size="small"
              current={1} // Visual trick: make both steps look "active/completed"
              // 如果狀態是正常，用 finish，否則用 process 或 error
              status={record.status === 'active' ? 'finish' : 'process'}
              items={[
                {
                  title: 'Cloudflare 設定',
                  icon: <CloudServerOutlined />,
                  description: (
                    <div style={{ marginTop: 8 }}>
                      <Space direction="vertical" size={2}>
                        <Space>
                          <Tag color="cyan">{record.cf_record_type || 'Unknown'}</Tag>
                          <Text copyable code>{record.cf_origin_value}</Text>
                        </Space>
                        {record.is_proxied ? (
                          <Tag color="orange" icon={<CloudServerOutlined />}>Proxy (橘雲)</Tag>
                        ) : (
                          <Tag color="default" icon={<GlobalOutlined />}>DNS Only (灰雲)</Tag>
                        )}
                      </Space>
                    </div>
                  ),
                },
                {
                  title: '最終解析結果',
                  icon: <CheckCircleFilled />,
                  description: (
                    <div style={{ marginTop: 8 }}>
                      <Space direction="vertical" size={2}>
                        {/* 顯示 IP */}
                        {/* CNAME Chain Info */}
                        {record.resolved_record &&
                          record.resolved_record.toLowerCase() !== record.cf_origin_value.toLowerCase() &&
                          record.cf_record_type !== 'A' && (
                            <div style={{
                              padding: '6px 10px',
                              // [關鍵修改] 使用 token 顏色取代寫死的 #fafafa
                              background: token.colorFillAlter,
                              border: `1px dashed ${token.colorBorder}`,
                              borderRadius: token.borderRadius
                            }}>
                              <Text type="secondary" style={{ fontSize: '12px', display: 'block' }}>
                                ↳ 實際指向 (CNAME):
                              </Text>
                              <Text code>{record.resolved_record}</Text>
                            </div>
                          )}

                        {/* Optimized IP List */}
                        <IpList ips={record.resolved_ips || []} />
                      </Space>
                    </div>
                  ),
                },
              ]}
            />
          </div>
        </Descriptions.Item>
      </Descriptions>

      <Divider style={{ margin: '24px 0' }} />

      {/* 2. SSL 憑證資訊 (整合 SANs) */}
      <Descriptions
        title="SSL 憑證資訊"
        bordered
        size="small"
        column={{ xxl: 2, xl: 2, lg: 2, md: 1, sm: 1, xs: 1 }}
      >
        {/* [新增] 域名與 Port */}
        <Descriptions.Item label={<Space><GlobalOutlined /> 監控目標</Space>}>
          <Space>
            <Text strong copyable>{record.domain_name}</Text>
            <Tag>{record.port || 443}</Tag>
          </Space>
        </Descriptions.Item>

        {/* [新增] 網域註冊到期日 (WHOIS) */}
        <Descriptions.Item label={<Space><ClockCircleOutlined /> 網域註冊到期</Space>}>
          {record.domain_expiry_date ? (
            <Space>
              <Text>{dayjs(record.domain_expiry_date).format('YYYY-MM-DD')}</Text>
              <Text type="secondary" style={{ fontSize: '12px' }}>
                ({record.domain_days_left} 天後)
              </Text>
            </Space>
          ) : <Text type="secondary">未知 (WHOIS)</Text>}
        </Descriptions.Item>
        <Descriptions.Item label={<Space><BankOutlined /> SSL 發行商</Space>}>
          {record.issuer || '-'}
        </Descriptions.Item>

        <Descriptions.Item label={<Space><NumberOutlined /> TLS 版本</Space>}>
          <Tag color={record.tls_version === 'TLS 1.3' ? 'success' : 'processing'}>
            {record.tls_version || '-'}
          </Tag>
        </Descriptions.Item>

        <Descriptions.Item label={<Space><ClockCircleOutlined /> SSL 有效期限</Space>}>
          {record.not_after ? (
            <Space direction="vertical" size={0}>
              <Text>{dayjs(record.not_before).format('YYYY-MM-DD')} ~</Text>
              <Text strong style={{ color: record.days_remaining < 30 ? '#ff4d4f' : '#52c41a' }}>
                {dayjs(record.not_after).format('YYYY-MM-DD')}
              </Text>
            </Space>
          ) : '-'}
        </Descriptions.Item>

        <Descriptions.Item label="SSL 剩餘天數">
          {record.days_remaining !== undefined ? (
            <Badge
              status={record.days_remaining < 30 ? 'error' : 'success'}
              text={`${record.days_remaining} 天`}
            />
          ) : '-'}
        </Descriptions.Item>

        {/* [優化] SANs 列表區塊 */}
        <Descriptions.Item
          label={
            <Space>
              <GlobalOutlined />
              SANs 列表
              {/* Badge 只需要簡單的 style (例如背景色) */}
              <Badge
                count={record.sans ? record.sans.length : 0}
                style={{ backgroundColor: '#52c41a' }}
                overflowCount={99}
              />
            </Space>
          }
          span={2}
        >
          {record.sans && record.sans.length > 0 ? (
            // 這裡才是列表容器，樣式應該放在這裡
            <div
              style={{
                maxHeight: '200px',
                overflowY: 'auto',
                padding: '8px',
                // [關鍵修改] 使用 token 顏色，支援暗黑模式
                background: token.colorFillAlter,
                borderRadius: token.borderRadius
              }}
            >
              <Space wrap size={[8, 8]}>
                {record.sans.map((san, index) => {
                  const isMatch = isInSans(san);
                  return (
                    <Tooltip title={isMatch ? "當前匹配域名" : ""} key={index}>
                      <Tag
                        color={isMatch ? "blue" : "default"}
                        style={isMatch ? { fontWeight: 'bold', border: `1px solid ${token.colorPrimary}` } : {}}
                      >
                        {san}
                      </Tag>
                    </Tooltip>
                  );
                })}
              </Space>
            </div>
          ) : (
            <Text type="secondary">無 SANs 資料 (或尚未掃描)</Text>
          )}
        </Descriptions.Item>
      </Descriptions>
    </Drawer>
  );
};