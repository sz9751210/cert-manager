import React from 'react';
import { Card, Form, Input, Button, message, Typography, theme } from 'antd';
import { UserOutlined, LockOutlined, SafetyCertificateOutlined } from '@ant-design/icons';
import { useNavigate } from 'react-router-dom';
import axios from 'axios';

const { Title } = Typography;

const Login: React.FC = () => {
    const navigate = useNavigate();
    const [loading, setLoading] = React.useState(false);

    const { token } = theme.useToken();

    const onFinish = async (values: any) => {
        setLoading(true);
        try {
            // 直接呼叫 axios，不經過攔截器
            const res = await axios.post('http://192.168.25.100:8080/api/login', values);
            const token = res.data.token;
            
            // 存入 LocalStorage
            localStorage.setItem('token', token);
            message.success('登入成功');
            navigate('/'); // 跳轉回首頁
        } catch (error: any) {
            message.error('登入失敗: ' + (error.response?.data?.error || '未知錯誤'));
        } finally {
            setLoading(false);
        }
    };

    return (
        <div style={{ 
            height: '100vh', 
            display: 'flex', 
            justifyContent: 'center', 
            alignItems: 'center',
            background: token.colorBgLayout
        }}>
            <Card style={{ width: 400, boxShadow: '0 4px 12px rgba(0,0,0,0.1)' }}>
                <div style={{ textAlign: 'center', marginBottom: 24 }}>
                    <SafetyCertificateOutlined style={{ fontSize: 48, color: token.colorPrimary }} />
                    <Title level={3} style={{ marginTop: 12 }}>CertManager</Title>
                    <Typography.Text type="secondary">SSL 全生命週期管理系統</Typography.Text>
                </div>

                <Form name="login" onFinish={onFinish} size="large">
                    <Form.Item name="username" rules={[{ required: true, message: '請輸入帳號' }]}>
                        <Input prefix={<UserOutlined />} placeholder="帳號 (admin)" />
                    </Form.Item>
                    <Form.Item name="password" rules={[{ required: true, message: '請輸入密碼' }]}>
                        <Input.Password prefix={<LockOutlined />} placeholder="密碼" />
                    </Form.Item>
                    <Form.Item>
                        <Button type="primary" htmlType="submit" block loading={loading}>
                            登入系統
                        </Button>
                    </Form.Item>
                </Form>
            </Card>
        </div>
    );
};

export default Login;