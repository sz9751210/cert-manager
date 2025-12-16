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
}
