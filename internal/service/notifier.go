package service

import (
	"bytes"
	"cert-manager/internal/domain"
	"cert-manager/internal/repository"
	"context"
	"encoding/json"
	"fmt"
	"github.com/sirupsen/logrus"
	"net/http"
	"text/template"
	"time"
)

// 定義給模板用的資料結構 (Context)
// 這裡定義變數名稱，使用者在模板裡就是用這些名字，例如 {{.Domain}}
type TemplateData struct {
	Domain     string
	Status     string
	Days       int
	ExpiryDate string
	Issuer     string
	IP         string
	TLS        string
	HTTPCode   int
}

// 輔助函式：渲染模板
func renderMessage(tmplStr string, cert domain.SSLCertificate) (string, error) {
	// 準備資料
	data := TemplateData{
		Domain:     cert.DomainName,
		Status:     string(cert.Status),
		Days:       cert.DaysRemaining,
		ExpiryDate: cert.NotAfter.Format("2006-01-02"),
		Issuer:     cert.Issuer,
		IP:         cert.ResolvedIP,
		TLS:        cert.TLSVersion,
		HTTPCode:   cert.HTTPStatusCode,
	}

	// 建立模板
	tmpl, err := template.New("notify").Parse(tmplStr)
	if err != nil {
		return "", err
	}

	// 渲染
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// 預設模板 (當使用者沒設定時用這個)
const defaultTelegramTemplate = `
⚠️ <b>[監控告警]</b>
域名: {{.Domain}}
狀態: {{.Status}}
剩餘: {{.Days}} 天
到期: {{.ExpiryDate}}
IP: {{.IP}}
`

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

	// 2. 決定使用的模板
	tmpl := settings.TelegramTemplate
	if tmpl == "" {
		tmpl = defaultTelegramTemplate
	}

	// 3. 渲染訊息
	msg, err := renderMessage(tmpl, cert)
	if err != nil {
		// 如果渲染失敗 (例如使用者語法打錯)，降級回預設文字，避免發不出告警
		msg = fmt.Sprintf("告警: %s 過期 (模板錯誤)", cert.DomainName)
	}

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

// 定義事件類型常數
type EventType string

const (
	EventAdd    EventType = "ADD"
	EventDelete EventType = "DELETE"
	EventRenew  EventType = "RENEW"
)

// 定義給操作模板用的資料結構
type OperationTemplateData struct {
	Action  string // 動作名稱 (中文)
	Domain  string // 對象域名
	Details string // 額外詳情
	Time    string // 發生時間
}

// 預設模板 (Fallback)
const (
	defaultAddTpl    = "✨ <b>[新增域名]</b>\n對象: {{.Domain}}\n詳情: {{.Details}}"
	defaultDeleteTpl = "🗑 <b>[刪除域名]</b>\n對象: {{.Domain}}\n詳情: {{.Details}}"
	defaultRenewTpl  = "♻️ <b>[SSL 續簽]</b>\n對象: {{.Domain}}\n結果: {{.Details}}"
)

// NotifyOperation 發送操作類型的告警
// action: 動作名稱 (e.g., "新增域名", "刪除域名")
// target: 操作對象 (e.g., "example.com")
// details: 額外資訊 (e.g., "由 admin 操作", "IP: 127.0.0.1")
func (n *NotifierService) NotifyOperation(ctx context.Context, eventType EventType, domainName, details string) {
	// 1. 取得設定
	settings, err := n.Repo.GetSettings(ctx)
	if err != nil || !settings.TelegramEnabled {
		return
	}

	// 2. 根據事件類型，決定 "是否發送" 以及 "使用哪個模板"
	var enabled bool
	var tmplStr string
	var actionName string

	switch eventType {
	case EventAdd:
		enabled = settings.NotifyOnAdd
		tmplStr = settings.NotifyOnAddTemplate
		if tmplStr == "" {
			tmplStr = defaultAddTpl
		}
		actionName = "新增域名"
	case EventDelete:
		enabled = settings.NotifyOnDelete
		tmplStr = settings.NotifyOnDeleteTemplate
		if tmplStr == "" {
			tmplStr = defaultDeleteTpl
		}
		actionName = "刪除域名"
	case EventRenew:
		enabled = settings.NotifyOnRenew
		tmplStr = settings.NotifyOnRenewTemplate
		if tmplStr == "" {
			tmplStr = defaultRenewTpl
		}
		actionName = "SSL 續簽"
	default:
		return // 未知事件不處理
	}

	// 如果使用者關閉了此類通知，直接退出
	if !enabled {
		return
	}

	// 3. 渲染模板
	data := OperationTemplateData{
		Action:  actionName,
		Domain:  domainName,
		Details: details,
		Time:    time.Now().Format("2006-01-02 15:04:05"),
	}

	t, err := template.New("op").Parse(tmplStr)
	if err != nil {
		logrus.Errorf("模板解析失敗: %v", err)
		return
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		logrus.Errorf("模板渲染失敗: %v", err)
		return
	}

	msg := buf.String()
	// 3. 發送 (非同步執行，避免卡住 API 回應)
	go func() {
		n.sendTelegram(settings.TelegramBotToken, settings.TelegramChatID, msg)
		// 如果有 webhook 也可以順便發
		if settings.WebhookEnabled && settings.WebhookURL != "" {
			n.sendWebhook(settings.WebhookURL, msg) // 注意：Webhook 可能需要 JSON 格式，這裡視您之前的實作而定
		}
	}()
}
