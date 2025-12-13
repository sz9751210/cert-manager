package domain

type NotificationSettings struct {
	WebhookURL string `bson:"webhook_url" json:"webhook_url"`
}
