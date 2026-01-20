// src/pages/CertDecoder.tsx

import React, { useState } from 'react';
import { Card, Input, Button, Descriptions, Tag, message, Alert } from 'antd';
import { FileTextOutlined, SafetyCertificateOutlined, CheckCircleOutlined, ClockCircleOutlined } from '@ant-design/icons';

// [修正 1] 改用 Named Import 引入定義好的 Service 函式與型別
import { decodeCertificate, CertDecodeResult } from '../services/api'; 

const { TextArea } = Input;

// [修正 2] 移除這裡重複定義的 interface，直接使用 api.ts 匯出的 CertDecodeResult (如果 api.ts 有匯出它的話)
// 如果 api.ts 沒匯出 CertDecodeResult，您可以保留下面的 interface，或者在 api.ts 加上 export
/* interface CertInfo {
  subject: string;
  issuer: string;
  not_before: string;
  not_after: string;
  days_remaining: number;
  dns_names: string[];
  serial_number: string;
  signature_algo: string;
  is_ca: boolean;
}
*/

const CertDecoder: React.FC = () => {
  const [certContent, setCertContent] = useState('');
  const [loading, setLoading] = useState(false);
  // 使用 api.ts 定義的型別
  const [result, setResult] = useState<CertDecodeResult | null>(null);

  const handleDecode = async () => {
    if (!certContent.trim()) {
      message.warning('請先貼上憑證內容');
      return;
    }
    
    if (!certContent.includes('BEGIN CERTIFICATE')) {
       message.error('內容格式不正確，必須包含 -----BEGIN CERTIFICATE-----');
       return;
    }

    setLoading(true);
    try {
      // [修正 3] 改用 Service 函式，不需要直接操作 api.post
      // Service 層已經處理了 response.data.data 的解構，這裡直接拿回來的 data 就是結果
      const data = await decodeCertificate(certContent);
      setResult(data);
      message.success('解析成功');
    } catch (error: any) {
      // 這裡的錯誤處理維持原樣，或是根據您的 axios interceptor 做調整
      const errorMsg = error.response?.data?.error || error.message || '解析失敗';
      message.error(errorMsg);
      setResult(null);
    } finally {
      setLoading(false);
    }
  };

  return (
    <div style={{ padding: '24px', maxWidth: '800px', margin: '0 auto' }}>
      <Card 
        title={<><SafetyCertificateOutlined /> SSL 憑證解碼器</>} 
        bordered={false}
      >
        <Alert 
           message="使用說明"
           description="請將您的憑證內容 (包含 -----BEGIN CERTIFICATE----- 與 -----END CERTIFICATE-----) 貼入下方文字框。"
           type="info"
           showIcon
           style={{ marginBottom: 16 }}
        />

        <TextArea 
          rows={6} 
          placeholder="-----BEGIN CERTIFICATE-----&#10;MIIF...&#10;-----END CERTIFICATE-----" 
          value={certContent}
          onChange={(e) => setCertContent(e.target.value)}
          style={{ fontFamily: 'monospace' }}
        />
        
        <div style={{ marginTop: 16, textAlign: 'right' }}>
           <Button type="default" onClick={() => {setCertContent(''); setResult(null);}} style={{ marginRight: 8 }}>
             清空
           </Button>
           <Button type="primary" icon={<FileTextOutlined />} loading={loading} onClick={handleDecode}>
             解析憑證
           </Button>
        </div>
      </Card>

      {result && (
        <Card style={{ marginTop: 24 }} title="解析結果">
          <Descriptions bordered column={1} size="middle">
             {/* 1. 主機名稱 (Subject) */}
             <Descriptions.Item label="DNS 主機名稱 (CN)">
                <span style={{ fontWeight: 'bold', fontSize: '16px' }}>{result.subject}</span>
                {result.is_ca && <Tag color="gold" style={{ marginLeft: 8 }}>Root CA</Tag>}
             </Descriptions.Item>

             {/* 2. SANs (多域名) */}
             <Descriptions.Item label="包含網域 (SANs)">
                {result.dns_names && result.dns_names.length > 0 ? (
                    result.dns_names.map(name => (
                        <Tag key={name}>{name}</Tag>
                    ))
                ) : (
                    <span style={{ color: '#999' }}>無 (單一網域憑證)</span>
                )}
             </Descriptions.Item>

             {/* 3. 到期日期 */}
             <Descriptions.Item label="到期日期">
                <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                    {result.days_remaining > 30 ? (
                        <CheckCircleOutlined style={{ color: '#52c41a' }} />
                    ) : (
                        <ClockCircleOutlined style={{ color: '#faad14' }} />
                    )}
                    <span>{new Date(result.not_after).toLocaleString()}</span>
                    <Tag color={result.days_remaining > 0 ? (result.days_remaining > 30 ? 'green' : 'orange') : 'red'}>
                        剩餘 {result.days_remaining} 天
                    </Tag>
                </div>
             </Descriptions.Item>

             {/* 4. 序號 */}
             <Descriptions.Item label="序號 (Serial Number)">
                 <span style={{ fontFamily: 'monospace' }}>{result.serial_number}</span>
             </Descriptions.Item>

             {/* 5. 發行商 */}
             <Descriptions.Item label="憑證核發單位 (Issuer)">
                 {result.issuer}
             </Descriptions.Item>

             {/* 6. 演算法 */}
             <Descriptions.Item label="簽章演算法">
                 <Tag>{result.signature_algo}</Tag>
             </Descriptions.Item>

             <Descriptions.Item label="生效日期">
                 {new Date(result.not_before).toLocaleString()}
             </Descriptions.Item>
          </Descriptions>
        </Card>
      )}
    </div>
  );
};

export default CertDecoder;