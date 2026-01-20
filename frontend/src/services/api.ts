// src/services/api.ts
import axios from 'axios';
import type { APIResponse, SSLCertificate } from '../types';

// ==========================================
// 1. 型別定義 (集中管理)
// ==========================================

export interface NotificationSettings {
    webhook_enabled: boolean;
    webhook_url: string;
    telegram_enabled: boolean;
    telegram_bot_token: string;
    telegram_chat_id: string;
}

export interface DashboardStats {
    total_domains: number;
    status_counts: Record<string, number>;
    expiry_counts: Record<string, number>;
    issuer_counts: Record<string, number>;
}

// [新增] 憑證解碼結果的型別定義
export interface CertDecodeResult {
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
// ==========================================
// 2. Axios 實例配置
// ==========================================

// 設定後端地址
const api = axios.create({
    baseURL: '/api/v1',
    // 若開發環境需要指定完整路徑，可解開下方註解或使用環境變數
    // baseURL: import.meta.env.VITE_API_URL || 'http://localhost:8080/api/v1',
});

// Request Interceptor: 自動帶上 Token
api.interceptors.request.use((config) => {
    const token = localStorage.getItem('token');
    if (token) {
        config.headers.Authorization = `Bearer ${token}`;
    }
    return config;
});

// Response Interceptor: 處理 401 過期
api.interceptors.response.use(
    (response) => response,
    (error) => {
        if (error.response && error.response.status === 401) {
            // Token 失效，清除並跳轉
            localStorage.removeItem('token');
            if (window.location.pathname !== '/login') {
                window.location.href = '/login';
            }
        }
        return Promise.reject(error);
    }
);

// ==========================================
// 3. API 函數 (域名相關)
// ==========================================

// 獲取域名列表 (支援分頁、篩選)
export const fetchDomains = async (
    page = 1,
    limit = 10,
    sort = '',
    search = '',
    status = '',
    proxied = '',
    ignored = '',
    zone = ''
) => {
    const response = await api.get<APIResponse<SSLCertificate[]>>('/domains', {
        params: {
            page,
            pageSize: limit,
            sortBy: sort,
            search,
            status,
            proxied,
            ignored,
            zone
        }
    });
    return response.data;
};

// 從 Cloudflare 同步域名
export const syncDomains = async () => {
    return api.post('/domains/sync');
};

// 掃描所有域名
export const scanDomains = async () => {
    return api.post('/domains/scan');
};

// 掃描單一域名 (已修正：改用 axios 實例)
export const scanSingleDomain = async (id: string) => {
    return api.post(`/domains/${id}/scan`);
};

// 批量掃描 (已修正：改用 axios 實例)
export const batchScanDomains = async (ids: string[]) => {
    return api.post('/domains/batch-scan', { ids });
};

// 更新單一域名設定 (是否忽略)
export const updateDomainSettings = async (id: string, isIgnored: boolean, port?: number) => {
    return api.patch(`/domains/${id}/settings`, { is_ignored: isIgnored, port: port });
};

// 批量更新設定
export const batchUpdateSettings = async (ids: string[], isIgnored: boolean) => {
    return api.post('/domains/batch-settings', { ids, is_ignored: isIgnored });
};

// 獲取主域名 (Zone) 列表
export const fetchZones = async () => {
    const response = await api.get<{ data: string[] }>('/zones');
    return response.data.data;
};

// ==========================================
// 4. API 函數 (系統設定與統計)
// ==========================================

// 獲取通知設定
export const getSettings = async () => {
    const res = await api.get<{ data: NotificationSettings }>('/settings');
    return res.data.data;
};

// 儲存通知設定
export const saveSettings = async (settings: NotificationSettings) => {
    return api.post('/settings', settings);
};

// 測試通知設定
export const testNotification = async (settings: NotificationSettings) => {
    return api.post('/settings/test', settings);
};

// 獲取儀表板統計數據
export const fetchStats = async () => {
    const res = await api.get<{ data: DashboardStats }>('/stats');
    return res.data.data;
};

// 解析 SSL 憑證內容
export const decodeCertificate = async (certContent: string) => {
    // 這裡定義回傳結構為 { data: CertDecodeResult }
    const res = await api.post<{ data: CertDecodeResult }>('/tools/decode-cert', {
        cert_content: certContent
    });
    return res.data.data;
};