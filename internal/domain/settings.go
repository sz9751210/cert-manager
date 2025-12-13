package domain

type NotificationSettings struct {
	// Webhook 設定
	WebhookEnabled bool   `bson:"webhook_enabled" json:"webhook_enabled"`
	WebhookURL     string `bson:"webhook_url" json:"webhook_url"`

	// Telegram 設定 [新增]
	TelegramEnabled  bool   `bson:"telegram_enabled" json:"telegram_enabled"`
	TelegramBotToken string `bson:"telegram_bot_token" json:"telegram_bot_token"`
	TelegramChatID   string `bson:"telegram_chat_id" json:"telegram_chat_id"`
}
