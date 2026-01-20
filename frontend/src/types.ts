// src/types.ts

export interface SSLCertificate {
  id: string;
  domain_name: string;

  // Cloudflare
  cf_zone_id: string;
  cf_record_id: string;
  is_proxied: boolean; // 小橘雲

  // 設定
  is_ignored: boolean;
  auto_renew: boolean;

  // 狀態
  issuer: string;
  not_before: string; // ISO 日期字串
  not_after: string; // ISO 日期字串
  days_remaining: number;

  // 檢查結果
  last_check_time: string;
  status: "active" | "expired" | "warning" | "unresolvable" | "pending";
  error_msg?: string;
  sans?: string[];

  tls_version: string; // [新增]
  http_status_code: number; // [新增]
  latency: number; // [新增]
  domain_expiry_date: string; // ISO String
  domain_days_left: number;
  is_match: boolean;
  resolved_record: string;
  resolved_ips?: string[];
  port?: number;
  cf_origin_value?: string;
  cf_record_type?: string;
}

export interface APIResponse<T> {
  data: T;
  total: number;
  page: number;
  limit: number;
}

export interface NotificationSettings {
  // --- 原有的連線設定 ---
  telegram_enabled: boolean;
  telegram_bot_token: string;
  telegram_chat_id: string;
  telegram_template: string; // 到期/異常通知模板

  webhook_enabled: boolean;
  webhook_url: string;
  webhook_template: string;
  wehbook_user?: string;
  webhook_password?: string;

  acme_email: string;

  // --- [新增] 操作通知設定 ---

  notify_on_expiry: boolean;
  // 1. 新增域名
  notify_on_add: boolean;
  notify_on_add_tpl: string;

  // 2. 刪除域名
  notify_on_delete: boolean;
  notify_on_delete_tpl: string;

  // 3. 續簽結果
  notify_on_renew: boolean;
  notify_on_renew_tpl: string;

  notify_on_update: boolean;
  notify_on_update_tpl: string;

  // [新增] 排程設定
  sync_enabled: boolean;
  sync_schedule: string;
  notify_on_sync_finish: boolean;
  sync_finish_tpl: string;

  scan_enabled: boolean;
  scan_schedule: string;
  notify_on_scan_finish: boolean;
  scan_finish_tpl: string;
}

export interface DashboardStats {
  total_domains: number;
  total_zones: number;
  ignored_domains: number;
  active_domains: number;
  expired_domains: number;
  warning_domains: number; //即將過期
  unresolvable_domains: number; // 無法解析
  avg_latency: number;
  issuer_stats: Record<string, number>;
}