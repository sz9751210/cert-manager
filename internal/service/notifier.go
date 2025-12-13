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

// SendTestMessage 發送測試訊息
func (n *NotifierService) SendTestMessage(ctx context.Context, webhookURL string) error {
	return n.send(webhookURL, "🔔 這是一條來自 CertManager 的測試告警訊息！")
}

// CheckAndNotify 檢查並發送告警 (核心邏輯)
func (n *NotifierService) CheckAndNotify(ctx context.Context, cert domain.SSLCertificate) {
	// 1. 判斷是否需要告警
	// 條件：剩餘天數 < 14 天 OR 狀態是 "unresolvable" (且不是被忽略的)
	// 且距離上次告警超過 24 小時 (防騷擾)
	shouldNotify := false

	if cert.IsIgnored {
		return
	}

	if cert.DaysRemaining < 14 && cert.DaysRemaining >= 0 {
		shouldNotify = true
	}
	// 您可以決定是否要針對 "無法解析" 進行告警
	if cert.Status == domain.StatusUnresolvable {
		shouldNotify = true
	}

	if !shouldNotify {
		return
	}

	// 2. 防騷擾檢查 (24小時內不重複發)
	if time.Since(cert.LastAlertTime) < 24*time.Hour {
		return
	}

	// 3. 獲取 Webhook URL
	settings, err := n.Repo.GetSettings(ctx)
	if err != nil || settings.WebhookURL == "" {
		return // 沒設定 URL 就不發
	}

	// 4. 組裝訊息
	msg := fmt.Sprintf("⚠️ [憑證告警] 域名: %s \n狀態: %s \n剩餘天數: %d 天 \n發行商: %s",
		cert.DomainName, cert.Status, cert.DaysRemaining, cert.Issuer)

	// 5. 發送
	logrus.Infof("正在發送告警: %s", cert.DomainName)
	if err := n.send(settings.WebhookURL, msg); err == nil {
		// 發送成功才更新 LastAlertTime
		n.Repo.UpdateAlertTime(ctx, cert.ID)
	} else {
		logrus.Errorf("發送告警失敗: %v", err)
	}
}

// 底層發送邏輯
func (n *NotifierService) send(url, message string) error {
	payload := WebhookPayload{Text: message}
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
		return fmt.Errorf("webhook 回應錯誤代碼: %d", resp.StatusCode)
	}
	return nil
}
