package domain

type NotificationSettings struct {
	// Webhook 設定
	WebhookEnabled  bool   `bson:"webhook_enabled" json:"webhook_enabled"`
	WebhookURL      string `bson:"webhook_url" json:"webhook_url"`
	WebhookUser     string `bson:"webhook_user" json:"webhook_user"`
	WebhookPassword string `bson:"webhook_password" json:"webhook_password"`

	// Telegram 設定 [新增]
	TelegramEnabled  bool   `bson:"telegram_enabled" json:"telegram_enabled"`
	TelegramBotToken string `bson:"telegram_bot_token" json:"telegram_bot_token"`
	TelegramChatID   string `bson:"telegram_chat_id" json:"telegram_chat_id"`

	// [新增] Let's Encrypt 設定
	AcmeEmail      string `bson:"acme_email" json:"acme_email"`
	AcmePrivateKey string `bson:"acme_private_key" json:"acme_private_key"` // 存 PEM 格式
	AcmeRegData    string `bson:"acme_reg_data" json:"acme_reg_data"`       // 存註冊資訊 JSON

	// [新增] 自定義模板
	// 如果為空字串，則使用系統預設模板
	TelegramTemplate string `bson:"telegram_template" json:"telegram_template"`
	WebhookTemplate  string `bson:"webhook_template" json:"webhook_template"`

	// --- [新增] 操作通知設定 ---

	NotifyOnExpiry         bool   `bson:"notify_on_expiry" json:"notify_on_expiry"`
	NotifyOnExpiryTemplate string `bson:"notify_on_expiry_tpl" json:"notify_on_expiry_tpl"`
	// 1. 新增域名 (Add Domain)
	NotifyOnAdd         bool   `bson:"notify_on_add" json:"notify_on_add"`
	NotifyOnAddTemplate string `bson:"notify_on_add_tpl" json:"notify_on_add_tpl"`

	// 2. 刪除域名 (Delete Domain)
	NotifyOnDelete         bool   `bson:"notify_on_delete" json:"notify_on_delete"`
	NotifyOnDeleteTemplate string `bson:"notify_on_delete_tpl" json:"notify_on_delete_tpl"`

	// 3. 續簽/更新 (Renew / Update)
	NotifyOnRenew         bool   `bson:"notify_on_renew" json:"notify_on_renew"`
	NotifyOnRenewTemplate string `bson:"notify_on_renew_tpl" json:"notify_on_renew_tpl"`

	// DNS/設定變更 (Update)
	NotifyOnUpdate         bool   `bson:"notify_on_update" json:"notify_on_update"`
	NotifyOnUpdateTemplate string `bson:"notify_on_update_tpl" json:"notify_on_update_tpl"`

	NotifyOnZoneAdd         bool   `bson:"notify_on_zone_add" json:"notify_on_zone_add"`
	NotifyOnZoneAddTemplate string `bson:"notify_on_zone_add_template" json:"notify_on_zone_add_tpl"`

	NotifyOnZoneDelete         bool   `bson:"notify_on_zone_delete" json:"notify_on_zone_delete"`
	NotifyOnZoneDeleteTemplate string `bson:"notify_on_zone_delete_template" json:"notify_on_zone_delete_tpl"`
	// --- [新增] E. 排程與匯總通知設定 ---

	// 1. Cloudflare 自動同步
	SyncEnabled        bool   `bson:"sync_enabled" json:"sync_enabled"`                   // 是否開啟自動同步
	SyncSchedule       string `bson:"sync_schedule" json:"sync_schedule"`                 // Cron 表達式 (e.g. "0 3 * * *")
	NotifyOnSyncFinish bool   `bson:"notify_on_sync_finish" json:"notify_on_sync_finish"` // 完成後是否通知
	SyncFinishTemplate string `bson:"sync_finish_tpl" json:"sync_finish_tpl"`             // 完成通知模板

	// 2. SSL 自動掃描
	ScanEnabled        bool   `bson:"scan_enabled" json:"scan_enabled"`
	ScanSchedule       string `bson:"scan_schedule" json:"scan_schedule"`
	NotifyOnScanFinish bool   `bson:"notify_on_scan_finish" json:"notify_on_scan_finish"`
	ScanFinishTemplate string `bson:"scan_finish_tpl" json:"scan_finish_tpl"`
}
