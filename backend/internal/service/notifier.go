package service

import (
	"bytes"
	"cert-manager/internal/domain"
	"cert-manager/internal/repository"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"text/template"
	"time"

	"github.com/sirupsen/logrus"
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
	Record     string
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
		IP:         strings.Join(cert.ResolvedIPs, ", "),
		TLS:        cert.TLSVersion,
		HTTPCode:   cert.HTTPStatusCode,
		Record:     cert.ResolvedRecord,
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
⚠️ [監控告警]
原因: {{.Reason}}
域名: {{.Domain}}
狀態: {{.Status}}
SSL剩餘: {{.Days}} 天
域名剩餘: {{.DomainDays}} 天
到期: {{.ExpiryDate}}
內容: {{.IP}}
`

// 定義內部使用的 Telegram 任務結構
type telegramJob struct {
	Token   string
	ChatID  string
	Message string
}

type webhookJob struct {
	URL      string
	Message  string
	User     string
	Password string
}

type NotifierService struct {
	Repo         repository.DomainRepository
	tgQueue      chan telegramJob // [新增] Telegram 訊息佇列
	webhookQueue chan webhookJob
}

// 2. 初始化
func NewNotifierService(repo repository.DomainRepository) *NotifierService {
	n := &NotifierService{
		Repo:         repo,
		tgQueue:      make(chan telegramJob, 1000), // 緩衝區 1000
		webhookQueue: make(chan webhookJob, 1000),  // 緩衝區 1000
	}

	// 啟動兩個背景發送 Worker
	go n.startTelegramWorker()
	go n.startWebhookWorker() // [新增]

	return n
}

// WebhookPayload 定義通用的訊息格式 (相容 Slack/Teams/Discord)
type WebhookPayload struct {
	Text string `json:"text"` // Slack, Discord 常用
}

// [新增] Telegram 背景工作者：負責限速發送
func (n *NotifierService) startTelegramWorker() {
	logrus.Info("[Notifier] Telegram Worker 已啟動，準備處理訊息佇列...")

	for job := range n.tgQueue {
		// 1. 執行發送
		// logrus.Infof("[Notifier] 正在發送 Telegram 訊息給 ChatID: %s", job.ChatID) // 除錯用，可註解
		if err := n.sendTelegram(job.Token, job.ChatID, job.Message); err != nil {
			logrus.Errorf("[Notifier] Telegram 發送失敗: %v", err)
		} else {
			// logrus.Info("[Notifier] Telegram 發送成功")
		}

		// 2. [關鍵修改] 強制休息 1.1 秒
		// 使用 Sleep 可以確保上一則發送完畢後，絕對會等待這段時間，不會有 Ticker 緩衝的問題
		time.Sleep(1100 * time.Millisecond)
	}
}

// [新增] Webhook Worker (每秒 1 則)
func (n *NotifierService) startWebhookWorker() {
	logrus.Info("[Notifier] Webhook Worker 已啟動...")

	for job := range n.webhookQueue {
		// 1. 執行發送
		if err := n.sendWebhook(job.URL, job.Message, job.User, job.Password); err != nil {
			logrus.Errorf("[Notifier] Webhook 發送失敗: %v", err)
		}

		// 2. [關鍵修改] 強制休息 1.1 秒
		time.Sleep(1100 * time.Millisecond)
	}
}

// CheckAndNotify 檢查並發送告警 (核心邏輯)
func (n *NotifierService) CheckAndNotify(ctx context.Context, cert domain.SSLCertificate) {
	// 1. 判斷告警條件 (邏輯保持不變)
	if cert.IsIgnored {
		return
	}

	// [修改 1] 放行條件：如果是 ConnectionError，即使日期是零值也要往下跑
	if cert.NotAfter.IsZero() && cert.Status != domain.StatusConnectionError {
		return
	}

	// if cert.NotAfter.IsZero() {
	// 	return
	// }
	var alertReasons []string
	shouldNotify := false

	// [新增] 連線錯誤檢查
	if cert.Status == domain.StatusConnectionError {
		// 您可以決定是否要發送告警，或者直接 return 忽略
		// 這裡示範加入告警原因
		alertReasons = append(alertReasons, fmt.Sprintf("❌ %s", cert.ErrorMsg))
		shouldNotify = true
	}

	// [修改 3] SSL 到期檢查 (隔離邏輯)
	// 只有在 "日期有效" 且 "不是連線錯誤" 時，才檢查剩餘天數
	// 這樣 0 天就不會誤判為過期
	if !cert.NotAfter.IsZero() && cert.Status != domain.StatusConnectionError {
		if cert.DaysRemaining < 30 && cert.DaysRemaining >= 0 {
			alertReasons = append(alertReasons, fmt.Sprintf("SSL憑證剩餘 %d 天", cert.DaysRemaining))
			shouldNotify = true
		} else if cert.DaysRemaining < 0 {
			alertReasons = append(alertReasons, "SSL憑證已過期")
			shouldNotify = true
		}
	}

	// B. 域名註冊過期檢查 (< 30 天)
	// 注意：需確保 DomainDaysLeft 有效 (例如 > -10000，避免初始值 0 誤判)
	// 這裡假設 DomainExpiryDate 不為零值才判斷
	if !cert.DomainExpiryDate.IsZero() {
		if cert.DomainDaysLeft < 30 && cert.DomainDaysLeft >= 0 {
			alertReasons = append(alertReasons, fmt.Sprintf("域名註冊剩餘 %d 天", cert.DomainDaysLeft))
		} else if cert.DomainDaysLeft < 0 {
			alertReasons = append(alertReasons, "域名註冊已過期")
		}
	}
	// 解析失敗
	// C. 解析失敗
	if cert.Status == domain.StatusUnresolvable {
		alertReasons = append(alertReasons, "❌ 域名無法解析 (Unresolvable)")
	}
	// [新增] 憑證不符檢查
	if !cert.IsMatch && cert.Status != domain.StatusUnresolvable {
		alertReasons = append(alertReasons, "❌ 憑證錯誤 (Hostname Mismatch)")
	}

	// 如果沒有任何告警原因，直接返回
	if len(alertReasons) == 0 {
		return
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

	if !settings.NotifyOnExpiry {
		return
	}

	reasonStr := strings.Join(alertReasons, ", ")

	// 4. 準備資料
	data := ExpiryTemplateData{
		Domain:     cert.DomainName,
		Status:     string(cert.Status),
		Days:       cert.DaysRemaining,
		DomainDays: cert.DomainDaysLeft, // [新增]
		ExpiryDate: cert.NotAfter.Format("2006-01-02"),
		Issuer:     cert.Issuer,
		IP:         cert.ResolvedRecord, // ResolvedIP 改為 ResolvedRecord (配合 DB)
		TLS:        cert.TLSVersion,
		HTTPCode:   cert.HTTPStatusCode,
		Record:     cert.ResolvedRecord,
		Reason:     reasonStr, // [新增]
	}

	// 5. 決定模板 (優先使用設定值，無設定則用預設)
	tmplStr := settings.TelegramTemplate
	if tmplStr == "" {
		tmplStr = defaultExpiryTpl // [修改] 使用新的變數名稱
	}

	// 6. 渲染訊息
	msg, err := n.renderTemplate(tmplStr, data)
	if err != nil {
		logrus.Errorf("模板渲染失敗: %v", err)
		msg = fmt.Sprintf("⚠️ 告警: %s (模板錯誤)", cert.DomainName)
	}

	// 額外附加嚴重錯誤資訊 (雙重保險，如果模板沒寫 Reason 也能看到)
	if !strings.Contains(msg, reasonStr) && !strings.Contains(tmplStr, "{{.Reason}}") {
		msg += fmt.Sprintf("\n原因: %s", reasonStr)
	}
	// 額外附加嚴重錯誤資訊 (如果模板裡沒寫的話，強制加在後面)
	if !cert.IsMatch {
		msg += "\n❌ [嚴重錯誤] 憑證錯誤！"
	}

	// 7. 發送
	n.sendToChannels(settings, msg)

	// 更新最後告警時間
	n.Repo.UpdateAlertTime(ctx, cert.ID)
}

// [修改] 測試訊息：接收設定物件，而不是單一 URL
// func (n *NotifierService) SendTestMessage(ctx context.Context, settings domain.NotificationSettings) error {
// 	var errs []string
// 	msg := "🔔 [測試] 這是一條來自 CertManager 的測試告警訊息！"

// 	if settings.WebhookEnabled {
// 		if settings.WebhookURL == "" {
// 			// 如果開關開著但沒網址，可以忽略或記錄錯誤，這裡選擇忽略不報錯
// 		} else {
// 			if err := n.sendWebhook(settings.WebhookURL, "🔔 這是一條來自 CertManager 的測試告警訊息！", settings.WebhookUser, settings.WebhookPassword); err != nil {
// 				errs = append(errs, "Webhook: "+err.Error())
// 			}
// 		}
// 	}

// 	if settings.TelegramEnabled {
// 		// [修正] 必須檢查 Token 和 ChatID
// 		if settings.TelegramBotToken != "" && settings.TelegramChatID != "" {
// 			if err := n.sendTelegram(settings.TelegramBotToken, settings.TelegramChatID, msg); err != nil {
// 				errs = append(errs, "Telegram: "+err.Error())
// 			}
// 		}
// 	}

// 	if len(errs) > 0 {
// 		return fmt.Errorf("部分發送失敗: %v", errs)
// 	}
// 	return nil
// }

// 底層邏輯：Webhook
// [修改] 增加 user, password 參數
func (n *NotifierService) sendWebhook(url, message, user, password string) error {
	payload := map[string]string{"text": message}
	jsonBytes, _ := json.Marshal(payload)

	// 建立請求
	req, _ := http.NewRequest("POST", url, bytes.NewBuffer(jsonBytes))
	req.Header.Set("Content-Type", "application/json")

	// [新增] 如果有設定帳密，則加入 Basic Auth Header
	if user != "" || password != "" {
		req.SetBasicAuth(user, password)
	}

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
		"parse_mode": "HTML", // 支援粗體等格式
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
	EventAdd        EventType = "ADD"
	EventDelete     EventType = "DELETE"
	EventRenew      EventType = "RENEW"
	EventUpdate     EventType = "UPDATE"
	EventSyncFinish EventType = "SYNC_FINISH"
	EventScanFinish EventType = "SCAN_FINISH"
	EventZoneAdd    EventType = "ZONE_ADD"
	EventZoneDelete EventType = "ZONE_DELETE"
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
	defaultExpiryTpl = "⚠️ [監控告警]\n域名: {{.Domain}}\n狀態: {{.Status}}\n剩餘: {{.Days}} 天\n到期: {{.ExpiryDate}}\n內容: {{.IP}}"
	defaultAddTpl    = "✨ [新增域名]\n對象: {{.Domain}}\n詳情: {{.Details}}"
	defaultDeleteTpl = "🗑 [刪除域名]\n對象: {{.Domain}}\n詳情: {{.Details}}"
	// defaultRenewTpl  = "♻️ [SSL 續簽]\n對象: {{.Domain}}\n結果: {{.Details}}"
	defaultRenewTpl  = "♻️ <b>[SSL 憑證續簽成功]</b>\n\n🌐 域名: <b>{{.Domain}}</b>\n{{.Details}}"
	defaultUpdateTpl = "🛠 [DNS 變更]\n對象: {{.Domain}}\n內容: {{.Details}}"
	// [新增] 匯總報告預設模板
	defaultSyncFinishTpl = "☁️ [Cloudflare 同步完成]\n新增: {{.Added}}\n更新: {{.Updated}}\n刪除: {{.Deleted}}\n略過: {{.Skipped}}\n耗時: {{.Duration}}"
	defaultScanFinishTpl = "🔍 [SSL 掃描完成]\n總數: {{.Total}}\n正常: {{.Active}}\n過期: {{.Expired}}\n異常: {{.Warning}}\n耗時: {{.Duration}}"
	defaultZoneAddTpl    = "🌍 <b>[新增主域名]</b>\nZone: {{.Domain}}\n詳情: {{.Details}}"
	defaultZoneDeleteTpl = "💥 <b>[移除主域名]</b>\nZone: {{.Domain}}\n詳情: {{.Details}}"
)

type ExpiryTemplateData struct {
	Domain     string
	Status     string
	Days       int
	DomainDays int
	ExpiryDate string
	Issuer     string
	IP         string
	TLS        string
	HTTPCode   int
	Record     string
	Reason     string
}

// NotifyOperation 發送操作類型的告警
// action: 動作名稱 (e.g., "新增域名", "刪除域名")
// target: 操作對象 (e.g., "example.com")
// details: 額外資訊 (e.g., "由 admin 操作", "IP: 127.0.0.1")
func (n *NotifierService) NotifyOperation(ctx context.Context, eventType EventType, domainName, details string) {
	// 1. 取得設定
	settings, err := n.Repo.GetSettings(ctx)
	if err != nil || (!settings.TelegramEnabled && !settings.WebhookEnabled) {
		return
	}

	// 2. 根據事件類型，決定 "是否發送" 以及 "使用哪個模板"
	var enabled bool
	var tmplStr string
	var actionName string

	if eventType == EventRenew && strings.Contains(details, "0001-01-01") {
		logrus.Warnf("🛑 [Notifier] 攔截到錯誤的續簽通知 (包含 0001-01-01): %s", domainName)
		return
	}

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
	case EventUpdate:
		enabled = settings.NotifyOnUpdate
		tmplStr = settings.NotifyOnUpdateTemplate
		if tmplStr == "" {
			tmplStr = defaultUpdateTpl
		}
		actionName = "設定變更"
	case EventZoneAdd:
		enabled = settings.NotifyOnZoneAdd
		tmplStr = settings.NotifyOnZoneAddTemplate
		if tmplStr == "" {
			tmplStr = defaultZoneAddTpl
		}
		actionName = "新增 Zone"
	case EventZoneDelete:
		enabled = settings.NotifyOnZoneDelete
		tmplStr = settings.NotifyOnZoneDeleteTemplate
		if tmplStr == "" {
			tmplStr = defaultZoneDeleteTpl
		}
		actionName = "移除 Zone"
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

	msg, err := n.renderTemplate(tmplStr, data)
	if err != nil {
		logrus.Errorf("操作通知模板錯誤: %v", err)
		return
	}
	// 3. 發送 (非同步執行，避免卡住 API 回應)
	n.sendToChannels(settings, msg)
}

func (n *NotifierService) SendTestMessage(ctx context.Context, settings domain.NotificationSettings) error {
	msg := "🔔 [測試] 這是一條來自 CertManager 的測試告警訊息！"
	n.sendToChannels(&settings, msg)
	return nil
}

// 我們需要一個新的結構來傳遞匯總數據
type TaskSummaryData struct {
	Added    int
	Updated  int
	Deleted  int
	Skipped  int
	Total    int
	Active   int
	Expired  int
	Warning  int
	Duration string
	Time     string
	Details  string // [新增] 用來放格式化後的詳細清單
}

// 新增 NotifyTaskFinish 用於發送匯總
func (n *NotifierService) NotifyTaskFinish(ctx context.Context, eventType EventType, data TaskSummaryData) {
	settings, err := n.Repo.GetSettings(ctx)
	if err != nil || (!settings.TelegramEnabled && !settings.WebhookEnabled) {
		return
	}

	var enabled bool
	var tmplStr string

	switch eventType {
	case EventSyncFinish:
		enabled = settings.NotifyOnSyncFinish
		tmplStr = settings.SyncFinishTemplate
		if tmplStr == "" {
			tmplStr = defaultSyncFinishTpl
		}
	case EventScanFinish:
		enabled = settings.NotifyOnScanFinish
		tmplStr = settings.ScanFinishTemplate
		if tmplStr == "" {
			tmplStr = defaultScanFinishTpl
		}
	default:
		return
	}

	if !enabled {
		return
	}

	data.Time = time.Now().Format("2006-01-02 15:04:05")

	msg, _ := n.renderTemplate(tmplStr, data)
	// 渲染與發送 (邏輯同 NotifyOperation)
	if err != nil {
		logrus.Error("匯總模板渲染錯誤:", err)
		return
	}

	n.sendToChannels(settings, msg)
}

func (n *NotifierService) renderTemplate(tmplStr string, data interface{}) (string, error) {
	t, err := template.New("notify").Parse(tmplStr)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// 統一發送邏輯 (同時發送 Telegram 和 Webhook)
// func (n *NotifierService) sendToChannels(settings *domain.NotificationSettings, msg string) {
// 	// 非同步發送，避免阻塞
// 	go func() {
// 		// 1. Telegram
// 		if settings.TelegramEnabled && settings.TelegramBotToken != "" && settings.TelegramChatID != "" {
// 			if err := n.sendTelegram(settings.TelegramBotToken, settings.TelegramChatID, msg); err != nil {
// 				logrus.Errorf("Telegram 發送失敗: %v", err)
// 			}
// 		}

// 		// 2. Webhook
// 		if settings.WebhookEnabled && settings.WebhookURL != "" {
// 			if err := n.sendWebhook(settings.WebhookURL, msg, settings.WebhookUser, settings.WebhookPassword); err != nil {
// 				logrus.Errorf("Webhook 發送失敗: %v", err)
// 			}
// 		}
// 	}()
// }

func (n *NotifierService) sendToChannels(settings *domain.NotificationSettings, msg string) {
	// 非同步放入 Queue，避免阻塞主流程
	go func() {
		// 1. Telegram Queue
		if settings.TelegramEnabled && settings.TelegramBotToken != "" && settings.TelegramChatID != "" {
			// [關鍵修改] 使用 <- 把任務丟進通道，而不是直接呼叫 n.sendTelegram
			select {
			case n.tgQueue <- telegramJob{
				Token:   settings.TelegramBotToken,
				ChatID:  settings.TelegramChatID,
				Message: msg,
			}:
				// [新增 Log] 成功放入佇列時印出，並顯示目前佇列堆積數量
				logrus.Infof("📥 [Queue] Telegram 訊息已入列 (目前堆積: %d)", len(n.tgQueue))
				// 成功排隊
			default:
				logrus.Warn("🔥 [Queue] Telegram 通知佇列已滿，丟棄訊息")
			}
		}

		// 2. Webhook Queue
		if settings.WebhookEnabled && settings.WebhookURL != "" {
			// [關鍵修改] 使用 <- 把任務丟進通道
			select {
			case n.webhookQueue <- webhookJob{
				URL:      settings.WebhookURL,
				Message:  msg,
				User:     settings.WebhookUser,
				Password: settings.WebhookPassword,
			}:
				logrus.Infof("📥 [Queue] Webhook 訊息已入列 (目前堆積: %d)", len(n.webhookQueue))
				// 成功排隊
			default:
				logrus.Warn("🔥 [Queue] Webhook 通知佇列已滿，丟棄訊息")
			}
		}
	}()
}
