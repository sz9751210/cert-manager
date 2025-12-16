package service

import (
	"bytes"
	"cert-manager/internal/domain"
	"cert-manager/internal/repository"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/sirupsen/logrus"
)

type NotifierService struct {
	Repo repository.DomainRepository
}

func NewNotifierService(repo repository.DomainRepository) *NotifierService {
	return &NotifierService{Repo: repo}
}

// WebhookPayload 定義通用的訊息格式 (相容 Slack/Teams/Discord)
type WebhookPayload struct {
	Text string `json:"text"` // Slack, Discord 常用
}

// CheckAndNotify 檢查並發送告警 (核心邏輯)
func (n *NotifierService) CheckAndNotify(ctx context.Context, cert domain.SSLCertificate) {
	// 1. 判斷告警條件 (邏輯保持不變)
	if cert.IsIgnored {
		return
	}
	shouldNotify := false
	if cert.DaysRemaining < 14 && cert.DaysRemaining >= 0 {
		shouldNotify = true
	}
	// [新增] 網域過期檢查 (例如少於 30 天)
	if cert.DomainDaysLeft < 30 && cert.DomainDaysLeft > 0 {
		shouldNotify = true
	}
	if cert.Status == domain.StatusUnresolvable {
		shouldNotify = true
	}
	if !shouldNotify {
		return
	}

	// 2. 防騷擾 (24hr)
	if time.Since(cert.LastAlertTime) < 24*time.Hour {
		return
	}

	// 3. 獲取設定
	settings, err := n.Repo.GetSettings(ctx)
	if err != nil {
		return
	}

	// 4. 組裝訊息
	msg := fmt.Sprintf(
		"⚠️ *[監控告警]*\n域名: `%s`\nSSL 剩餘: %d 天\n網域 剩餘: %d 天\n狀態: %s",
		cert.DomainName, cert.DaysRemaining, cert.DomainDaysLeft, cert.Status,
	)
	// 5. 依序發送各管道
	sentCount := 0

	// Channel A: Webhook
	if settings.WebhookEnabled && settings.WebhookURL != "" {
		if err := n.sendWebhook(settings.WebhookURL, msg); err == nil {
			sentCount++
		} else {
			logrus.Errorf("Webhook 發送失敗: %v", err)
		}
	}

	// Channel B: Telegram [新增]
	if settings.TelegramEnabled && settings.TelegramBotToken != "" && settings.TelegramChatID != "" {
		if err := n.sendTelegram(settings.TelegramBotToken, settings.TelegramChatID, msg); err == nil {
			sentCount++
		} else {
			logrus.Errorf("Telegram 發送失敗: %v", err)
		}
	}

	// 只要有一個管道發送成功，就更新時間
	if sentCount > 0 {
		n.Repo.UpdateAlertTime(ctx, cert.ID)
		logrus.Infof("告警已發送: %s (成功管道數: %d)", cert.DomainName, sentCount)
	}
}

// [修改] 測試訊息：接收設定物件，而不是單一 URL
func (n *NotifierService) SendTestMessage(ctx context.Context, settings domain.NotificationSettings) error {
	var errs []string
	msg := "🔔 [測試] 這是一條來自 CertManager 的測試告警訊息！"

	if settings.WebhookEnabled {
		if settings.WebhookURL == "" {
			// 如果開關開著但沒網址，可以忽略或記錄錯誤，這裡選擇忽略不報錯
		} else {
			if err := n.sendWebhook(settings.WebhookURL, "🔔 這是一條來自 CertManager 的測試告警訊息！"); err != nil {
				errs = append(errs, "Webhook: "+err.Error())
			}
		}
	}

	if settings.TelegramEnabled {
		// [修正] 必須檢查 Token 和 ChatID
		if settings.TelegramBotToken != "" && settings.TelegramChatID != "" {
			if err := n.sendTelegram(settings.TelegramBotToken, settings.TelegramChatID, msg); err != nil {
				errs = append(errs, "Telegram: "+err.Error())
			}
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("部分發送失敗: %v", errs)
	}
	return nil
}

// 底層邏輯：Webhook
func (n *NotifierService) sendWebhook(url, message string) error {
	payload := map[string]string{"text": message}
	jsonBytes, _ := json.Marshal(payload)
	req, _ := http.NewRequest("POST", url, bytes.NewBuffer(jsonBytes))
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("status code %d", resp.StatusCode)
	}
	return nil
}

// 底層邏輯：Telegram [新增]
func (n *NotifierService) sendTelegram(token, chatID, message string) error {
	apiURL := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", token)
	payload := map[string]string{
		"chat_id":    chatID,
		"text":       message,
		"parse_mode": "Markdown", // 支援粗體等格式
	}
	jsonBytes, _ := json.Marshal(payload)

	req, _ := http.NewRequest("POST", apiURL, bytes.NewBuffer(jsonBytes))
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("telegram status code %d", resp.StatusCode)
	}
	return nil
}
