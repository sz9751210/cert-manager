package domain

type NotificationSettings struct {
	// Webhook 設定
	WebhookEnabled bool   `bson:"webhook_enabled" json:"webhook_enabled"`
	WebhookURL     string `bson:"webhook_url" json:"webhook_url"`

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

	// 1. 新增域名 (Add Domain)
	NotifyOnAdd         bool   `bson:"notify_on_add" json:"notify_on_add"`
	NotifyOnAddTemplate string `bson:"notify_on_add_tpl" json:"notify_on_add_tpl"`

	// 2. 刪除域名 (Delete Domain)
	NotifyOnDelete         bool   `bson:"notify_on_delete" json:"notify_on_delete"`
	NotifyOnDeleteTemplate string `bson:"notify_on_delete_tpl" json:"notify_on_delete_tpl"`

	// 3. 續簽/更新 (Renew / Update)
	NotifyOnRenew         bool   `bson:"notify_on_renew" json:"notify_on_renew"`
	NotifyOnRenewTemplate string `bson:"notify_on_renew_tpl" json:"notify_on_renew_tpl"`
}
