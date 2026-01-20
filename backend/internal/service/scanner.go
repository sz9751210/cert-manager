package service

import (
	"cert-manager/internal/domain"
	"cert-manager/internal/repository"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/likexian/whois"
	whoisparser "github.com/likexian/whois-parser"
	"github.com/sirupsen/logrus"
	"golang.org/x/net/publicsuffix"
)

// ScannerService 負責域名的掃描、監控與通知
type ScannerService struct {
	Repo       repository.DomainRepository
	Notifier   *NotifierService
	CFService  *CloudflareService
	httpClient *http.Client
}

// NewScannerService 初始化 ScannerService
func NewScannerService(repo repository.DomainRepository, notifier *NotifierService, cf *CloudflareService) *ScannerService {
	return &ScannerService{
		Repo:      repo,
		Notifier:  notifier,
		CFService: cf,
		// 使用共用 Client，設定全域超時與連線池限制，避免 FD 洩漏
		httpClient: &http.Client{
			Timeout: 15 * time.Second,
			Transport: &http.Transport{
				TLSClientConfig:     &tls.Config{InsecureSkipVerify: true},
				MaxIdleConns:        100,
				MaxIdleConnsPerHost: 10,
				IdleConnTimeout:     90 * time.Second,
				DisableKeepAlives:   false, // 允許 KeepAlive 以提升效率，但在掃描大量不同 Host 時效益有限
			},
		},
	}
}

// =============================================================================
// Public Methods (業務入口)
// =============================================================================

// ScanAll 啟動併發掃描 (用於排程任務)
func (s *ScannerService) ScanAll(ctx context.Context) error {
	// 設定總體超時時間 30 分鐘
	scanCtx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()

	// 1. 獲取待掃描域名
	// TODO: 生產環境建議分頁獲取或只獲取 is_ignored=false
	domains, _, err := s.Repo.List(scanCtx, 1, 10000, "", "", "", "", "false", "")
	if err != nil {
		logrus.Errorf("掃描排程獲取域名失敗: %v", err)
		return err
	}

	total := len(domains)
	logrus.Infof("開始排程掃描 (SSL Expiry Check)，共 %d 個域名...", len(domains))

	startTime := time.Now()
	var summary TaskSummaryData
	summary.Total = len(domains)

	// [新增] 進度計數器
	var processedCount int32 = 0

	// 2. 設定併發控制
	concurrency := 10
	sem := make(chan struct{}, concurrency) // 信號量
	var wg sync.WaitGroup
	var mu sync.Mutex // 保護 summary 寫入

	// 3. 執行併發掃描
Loop:
	for _, d := range domains {
		// 檢查 Context 是否已超時或取消
		select {
		case <-scanCtx.Done():
			logrus.Warnf("排程掃描已達時間上限，停止新增任務。")
			break Loop
		case sem <- struct{}{}: // 獲取令牌
			// 繼續
		}

		wg.Add(1)
		go func(cert domain.SSLCertificate) {
			defer wg.Done()
			defer func() { <-sem }() // 釋放令牌

			// 執行單一任務並收集結果
			resStatus, latency, err := s.processTask(scanCtx, cert)

			// 更新統計數據
			mu.Lock()
			switch resStatus {
			case domain.StatusActive:
				summary.Active++
			case domain.StatusExpired:
				summary.Expired++
			case domain.StatusWarning, domain.StatusUnresolvable:
				summary.Warning++
			}
			mu.Unlock()

			// 記錄單行 Log (保留你需要的進度輸出)
			logTaskResult(cert.DomainName, resStatus, latency, err)
			// [新增] 更新並顯示進度 (每完成 5 個顯示一次，避免 Log 太多，也可設為 1)
			current := atomic.AddInt32(&processedCount, 1)
			if current%5 == 0 || int(current) == total {
				percentage := float64(current) / float64(total) * 100
				logrus.Infof("📊 進度: %d/%d (%.1f%%) - 最新完成: %s (%s)",
					current, total, percentage, cert.DomainName, resStatus)
			} else {
				// 如果想要看每一個的 Log，保留原本的這行，否則可以註解掉減少雜訊
				// logTaskResult(cert.DomainName, resStatus, latency, err)
			}
		}(d)
	}

	logrus.Info("⏳ 等待剩餘背景任務完成...")
	wg.Wait()

	// 4. 發送匯總通知
	summary.Duration = time.Since(startTime).String()
	// s.Notifier.NotifyTaskFinish(ctx, EventScanFinish, summary)
	logrus.Infof("排程掃描全部完成 (總耗時: %s)", summary.Duration)

	return nil
}

// ScanOne 對單一域名執行完整掃描流程 (包含 WHOIS, Diff, Notify, Save)
// 這是外部手動觸發或 ScanAll 內部呼叫的核心邏輯
func (s *ScannerService) ScanOne(ctx context.Context, oldCert domain.SSLCertificate, checkExpiry bool) (domain.SSLCertificate, []string, error) {
	// 1. 網路掃描 (SSL/IP/HTTP)
	newCert := s.PerformNetworkScan(ctx, oldCert.DomainName, oldCert.Port)

	// 2. 繼承舊資料 (Cloudflare 設定等不由此處更新)
	s.inheritConfig(&newCert, oldCert)

	// 3. WHOIS 查詢 (智慧緩存策略)
	s.syncWhois(ctx, &newCert, oldCert)

	// 4. 生成差異報告
	changes := s.generateDiff(oldCert, newCert)

	// 5. 寫入資料庫
	if err := s.Repo.UpdateCertInfo(ctx, newCert); err != nil {
		return newCert, nil, err
	}

	// 6. 發送通知 (狀態變更與續簽)
	s.notifyChanges(ctx, newCert, oldCert, changes)

	// 7. [關鍵修改] 判斷是否發送告警
	// 邏輯：
	// (A) checkExpiry=true (手動/排程掃描): 總是檢查
	// (B) isFreshError: 只有當 "舊狀態不是連線錯誤" 且 "新狀態是連線錯誤" 時，才視為新發生的故障

	isFreshError := newCert.Status == domain.StatusConnectionError && oldCert.Status != domain.StatusConnectionError

	if checkExpiry || isFreshError {
		s.Notifier.CheckAndNotify(ctx, newCert)
	}

	return newCert, changes, nil
}

// =============================================================================
// Private Logic: Orchestration & Helper (流程控制與輔助)
// =============================================================================

// processTask 封裝 ScanAll 中的單一任務邏輯
func (s *ScannerService) processTask(ctx context.Context, cert domain.SSLCertificate) (status string, latency int64, err error) {
	// 檢查 Unresolvable，但仍需檢查是否需要過期通知
	if cert.Status == domain.StatusUnresolvable {
		logrus.Infof("--- [Skip ] 跳過網路掃描 (Status=Unresolvable): %s", cert.DomainName)
		// 即使跳過掃描，仍需檢查 DB 內的日期是否觸發通知
		// s.Notifier.CheckAndNotify(ctx, cert)
		return domain.StatusUnresolvable, 0, nil
	}

	logrus.Infof(">>> [Start] 掃描中: %s", cert.DomainName)

	if ctx.Err() != nil {
		return "", 0, ctx.Err()
	}

	// 呼叫核心 ScanOne
	newCert, _, err := s.ScanOne(ctx, cert, true)
	if err != nil {
		return "", 0, err
	}

	return newCert.Status, newCert.Latency, nil
}

// inheritConfig 繼承不需要重新掃描的配置
func (s *ScannerService) inheritConfig(newCert *domain.SSLCertificate, oldCert domain.SSLCertificate) {
	newCert.ID = oldCert.ID
	newCert.CFZoneID = oldCert.CFZoneID
	newCert.ZoneName = oldCert.ZoneName
	newCert.CFRecordID = oldCert.CFRecordID
	newCert.IsIgnored = oldCert.IsIgnored
	newCert.AutoRenew = oldCert.AutoRenew
	newCert.IsProxied = oldCert.IsProxied
	newCert.CFOriginValue = oldCert.CFOriginValue
	newCert.CFRecordType = oldCert.CFRecordType
	newCert.CFComment = oldCert.CFComment
	// 默認繼承 WHOIS，稍後由 syncWhois 決定是否覆蓋
	newCert.DomainExpiryDate = oldCert.DomainExpiryDate
	newCert.DomainDaysLeft = oldCert.DomainDaysLeft
	if newCert.Port == 0 && oldCert.Port != 0 {
         newCert.Port = oldCert.Port
    }
}

// syncWhois 處理 WHOIS 查詢與緩存策略
func (s *ScannerService) syncWhois(ctx context.Context, newCert *domain.SSLCertificate, oldCert domain.SSLCertificate) {
	shouldQuery := false
	if oldCert.DomainExpiryDate.IsZero() {
		shouldQuery = true
	} else if oldCert.DomainDaysLeft < 60 {
		// 快到期才頻繁查
		shouldQuery = true
	}

	if shouldQuery {
		rootDomain := getRootDomain(newCert.DomainName)
		expiryDate, daysLeft, err := s.fetchWhoisInfo(rootDomain)
		if err == nil {
			newCert.DomainExpiryDate = expiryDate
			newCert.DomainDaysLeft = daysLeft
		} else {
			logrus.Debugf("WHOIS fail for %s: %v", rootDomain, err)
			// 失敗則保持繼承的值 (已在 inheritConfig 設定)
		}
	} else {
		// 重新計算剩餘天數
		if !newCert.DomainExpiryDate.IsZero() {
			newCert.DomainDaysLeft = int(time.Until(newCert.DomainExpiryDate).Hours() / 24)
		}
	}
}

// notifyChanges 處理所有通知邏輯
func (s *ScannerService) notifyChanges(ctx context.Context, newCert, oldCert domain.SSLCertificate, changes []string) {
	// =================================================================
	// [關鍵修正 1] 靜音初始化過程 (Pending -> Active/Warning/Expired)
	// 如果舊狀態是 pending，代表這是剛入庫後的第一次掃描 (初始化)。
	// 這種情況下的 "狀態變更" 或 "日期更新" 不應視為 Update/Renew 事件。
	// 新域名的通知責任在 CronService (EventAdd) 或 ZoneAdd，Scanner 應保持安靜。
	// =================================================================
	if oldCert.Status == "pending" {
		return
	}
	// 1. 變更通知 (Diff)
	if len(changes) > 0 {
		logrus.Infof("🔍 [Debug] %s 變更內容: %v", newCert.DomainName, changes)

		diffMsg := strings.Join(changes, "\n")
		eventType := EventUpdate
		// 如果是續簽，使用特殊事件類型
		if !oldCert.NotAfter.IsZero() && newCert.NotAfter.After(oldCert.NotAfter.Add(24*time.Hour)) {
			eventType = EventRenew
			logrus.Infof("🔔 [Notify] 觸發 EventRenew: %s", newCert.DomainName)
		} else {
			logrus.Infof("🔔 [Notify] 觸發 EventUpdate: %s", newCert.DomainName)
		}
		s.Notifier.NotifyOperation(ctx, eventType, newCert.DomainName, diffMsg)
	}

}

// =============================================================================
// Private Logic: Network Scanners (底層掃描實作)
// =============================================================================

// performNetworkScan 執行所有網路層面的檢查 (DNS, SSL, HTTP)
func (s *ScannerService) PerformNetworkScan(parentCtx context.Context, domainName string, port int) domain.SSLCertificate {
	// 硬性超時保護：單一域名最多 30 秒
	ctx, cancel := context.WithTimeout(parentCtx, 60*time.Second)
	defer cancel()

	if port == 0 {
		port = 443
	}

	result := domain.SSLCertificate{
		DomainName:    domainName,
		Port:          port,
		Status:        domain.StatusActive,
		LastCheckTime: time.Now(),
	}

	start := time.Now()

	// 1. DNS 解析
	if err := s.resolveDNS(ctx, &result); err != nil {
		// DNS 失敗則直接返回 Unresolvable
		result.Status = domain.StatusUnresolvable
		result.ErrorMsg = "DNS 解析失敗: " + err.Error()
		return result
	}

	// 2. SSL 連線與憑證解析 (包含重試機制)
	err := s.withRetry(ctx, 3, 5*time.Second, func() error {
		return s.checkSSLHandshake(ctx, &result)
	})

	if err != nil {
		// [關鍵修正] 如果 DNS 解析成功了(上面沒 return)，但這裡連線失敗
		// 不應該標記為 Unresolvable，而是 Connection Failed
		// 這樣您就知道是 "網路/防火牆" 問題，而不是 "域名不存在"

		// result.Status = domain.StatusUnresolvable
		errMsg := s.parseDialError(err)
		result.Latency = 0
		result.ErrorMsg = errMsg

		result.DaysRemaining = 0
		result.Issuer = ""
		result.IsMatch = true // 預設為 true，避免因為沒抓到憑證而報 "Mismatch" 錯誤

		// 根據錯誤類型決定狀態
		if strings.Contains(errMsg, "DNS") {
			result.Status = domain.StatusUnresolvable
		} else {
			// timeout, connection refused, reset by peer 等等
			// 改用 connection_error，不再使用 expired 或 unresolvable
			result.Status = domain.StatusConnectionError
		}
		return result // SSL 失敗則不需要測 HTTP
	}

	result.Latency = time.Since(start).Milliseconds()

	// 3. HTTP 狀態檢查 (僅當還有剩餘時間時執行)
	if deadline, ok := ctx.Deadline(); ok && time.Until(deadline) > 2*time.Second {
		s.checkHTTPStatus(ctx, &result)
	}

	return result
}

// resolveDNS 解析 IP 與 CNAME
func (s *ScannerService) resolveDNS(ctx context.Context, result *domain.SSLCertificate) error {
	// Lookup IPs
	ips, err := net.DefaultResolver.LookupHost(ctx, result.DomainName)
	if err != nil {
		result.Status = domain.StatusUnresolvable
		result.ErrorMsg = "DNS 解析失敗: " + err.Error()
		return err
	}
	sort.Strings(ips)
	result.ResolvedIPs = ips
	result.ResolvedRecord = strings.Join(ips, ", ")

	// Lookup CNAME
	cname, err := net.DefaultResolver.LookupCNAME(ctx, result.DomainName)
	if err == nil {
		cname = strings.TrimSuffix(cname, ".")
		if cname != "" && cname != result.DomainName {
			result.ResolvedRecord = cname
		}
	}
	return nil
}

// checkSSLHandshake 建立 TLS 連線並解析憑證
func (s *ScannerService) checkSSLHandshake(ctx context.Context, result *domain.SSLCertificate) error {
	address := fmt.Sprintf("%s:%d", result.DomainName, result.Port)
	dialer := &net.Dialer{Timeout: 15 * time.Second, KeepAlive: -1}

	// TCP Dial
	rawConn, err := dialer.DialContext(ctx, "tcp", address)
	if err != nil {
		return err
	}
	defer rawConn.Close()

	// 設定 Deadline 防止 Handshake 卡死
	_ = rawConn.SetDeadline(time.Now().Add(15 * time.Second))

	// TLS Config
	tlsConfig := &tls.Config{
		InsecureSkipVerify: true,
		ServerName:         result.DomainName,
	}
	conn := tls.Client(rawConn, tlsConfig)
	// 這裡不需要 defer conn.Close()，因為 rawConn 關閉時會一併斷開，且我們只讀一次

	// TLS Handshake
	if err := conn.HandshakeContext(ctx); err != nil {
		return err
	}

	// 解析憑證資訊
	s.parseCertInfo(conn, result)
	return nil
}

// parseCertInfo 從連線中提取憑證資訊
func (s *ScannerService) parseCertInfo(conn *tls.Conn, result *domain.SSLCertificate) {
	state := conn.ConnectionState()
	if len(state.PeerCertificates) == 0 {
		return
	}
	cert := state.PeerCertificates[0]

	result.Issuer = cert.Issuer.CommonName
	if result.Issuer == "" && len(cert.Issuer.Organization) > 0 {
		result.Issuer = cert.Issuer.Organization[0]
	}
	result.NotBefore = cert.NotBefore
	result.NotAfter = cert.NotAfter
	result.SANs = cert.DNSNames
	result.DaysRemaining = int(time.Until(cert.NotAfter).Hours() / 24)

	// TLS 版本
	if v, ok := tlsVersions[state.Version]; ok {
		result.TLSVersion = v
	} else {
		result.TLSVersion = "Unknown"
	}

	// 驗證 Hostname
	if err := cert.VerifyHostname(result.DomainName); err == nil {
		result.IsMatch = true
	} else {
		result.IsMatch = false
		result.ErrorMsg = "憑證名稱不符 (Hostname mismatch)"
	}

	// 設定狀態
	if result.DaysRemaining < 0 {
		result.Status = domain.StatusExpired
	} else {
		result.Status = domain.StatusActive
	}

	// if result.DaysRemaining < 0 {
	// 	result.Status = domain.StatusExpired
	// } else if result.DaysRemaining < 15 {
	// 	result.Status = domain.StatusWarning
	// } else {
	// 	result.Status = domain.StatusActive
	// }
}

// checkHTTPStatus 使用 Service 共用的 Client 檢查狀態碼
func (s *ScannerService) checkHTTPStatus(ctx context.Context, result *domain.SSLCertificate) {
	url := fmt.Sprintf("https://%s:%d", result.DomainName, result.Port)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return
	}

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body) // 讀取 Body 以確保 TCP 連接可重用

	result.HTTPStatusCode = resp.StatusCode
}

// fetchWhoisInfo 查詢 WHOIS
func (s *ScannerService) fetchWhoisInfo(domainName string) (time.Time, int, error) {
	rootDomain, err := publicsuffix.EffectiveTLDPlusOne(domainName)
	if err != nil {
		// 如果解析失敗（例如是 localhost 或 IP），就用原本的嘗試
		rootDomain = domainName
	}

	raw, err := whois.Whois(rootDomain)
	if err != nil {
		return time.Time{}, 0, err
	}

	result, err := whoisparser.Parse(raw)
	if err != nil {
		return time.Time{}, 0, err
	}

	dateStr := result.Domain.ExpirationDate
	if dateStr == "" {
		return time.Time{}, 0, fmt.Errorf("no expiration date found")
	}

	if idx := strings.Index(dateStr, " ("); idx != -1 {
		dateStr = strings.TrimSpace(dateStr[:idx])
	}

	// 2. 確保移除前後空白 (有些 WHOIS 會有不可見字元)
	dateStr = strings.TrimSpace(dateStr)

	expiryTime, err := s.parseWhoisTime(result.Domain.ExpirationDate)
	if err != nil {
		// Fallback: 嘗試直接解析常见格式，防止 s.parseWhoisTime 沒覆蓋到
		// TWNIC 常見格式: "2026-06-17 13:11:45 (UTC+8)" 或 "2026-06-17"
		layouts := []string{
			"2006-01-02 15:04:05", // 配合上述清洗後的格式
			time.RFC3339,
			"2006-01-02T15:04:05Z",
			"2006-01-02T15:04:05.00Z",
			"2006-01-02",
			"02-Jan-2006",
			"2006.01.02",
		}
		for _, layout := range layouts {
			if t, e := time.Parse(layout, dateStr); e == nil {
				expiryTime = t
				err = nil
				break
			}
		}
		if err != nil {
			// Log
			logrus.Warnf("WHOIS date parse fail for %s. Raw: '%s' | Cleaned: '%s'", domainName, result.Domain.ExpirationDate, dateStr)
			return time.Time{}, 0, fmt.Errorf("date parse fail: %s", dateStr)
		}
	}

	daysLeft := int(time.Until(expiryTime).Hours() / 24)
	return expiryTime, daysLeft, nil
}

// =============================================================================
// Helper Functions (工具函數)
// =============================================================================

// func (s *ScannerService) generateDiff(old, new domain.SSLCertificate) []string {
// 	var changes []string

// 	// 1. [新增] 憑證續簽檢測 (Renewal Check)
// 	// 邏輯：如果 新的到期日 比 舊的到期日 晚了超過 24 小時，視為已續簽
// 	// [關鍵修正] 加上 !old.NotAfter.IsZero()
// 	if !old.NotAfter.IsZero() && new.NotAfter.After(old.NotAfter.Add(24*time.Hour)) {
// 		logrus.Infof("♻️ [Diff] 偵測到續簽: %s | 舊: %s -> 新: %s",
// 			new.DomainName, old.NotAfter.Format("2006-01-02"), new.NotAfter.Format("2006-01-02"))
// 		change := fmt.Sprintf(
// 			"♻️ <b>SSL 憑證已更新 (Renewed)</b>\n"+
// 				"      📅 舊到期日: <del>%s</del>\n"+
// 				"      📅 新到期日: <b>%s</b>\n"+
// 				"      ⏳ 剩餘天數: <b>%d 天</b>",
// 			old.NotAfter.Format("2006-01-02"),
// 			new.NotAfter.Format("2006-01-02"),
// 			new.DaysRemaining,
// 		)
// 		changes = append(changes, change)
// 	}

// 	// 1. 狀態與續簽
// 	if old.Status != new.Status {

// 		if new.Status == domain.StatusConnectionError {
// 			// [CASE 1] 變成連線錯誤 (Active -> Error)
// 			// 忽略 Diff，交給 ScanOne 的 isFreshError 去觸發 "⚠️ 監控告警"

// 		} else if old.Status == domain.StatusConnectionError && new.Status == domain.StatusActive {
// 			// [CASE 2] 連線恢復 (Error -> Active)
// 			// 這是您要求的：Error 消失時發送通知
// 			changes = append(changes, fmt.Sprintf("✅ <b>連線已恢復</b>\n      狀態: %s ➔ %s", old.Status, new.Status))

// 		} else {
// 			// [CASE 3] 其他狀態變更 (e.g., Active -> Expired)
// 			changes = append(changes, fmt.Sprintf("狀態: %s ➔ %s", old.Status, new.Status))
// 		}
// 	}
// 	// if new.NotAfter.Sub(old.NotAfter) > 24*time.Hour {
// 	// 	changes = append(changes, fmt.Sprintf("憑證續簽: %s ➔ %s",
// 	// 		old.NotAfter.Format("2006-01-02"),
// 	// 		new.NotAfter.Format("2006-01-02"),
// 	// 	))
// 	// }

// 	// 2. Cloudflare 設定
// 	if old.CFOriginValue != new.CFOriginValue || old.CFRecordType != new.CFRecordType {
// 		changes = append(changes, fmt.Sprintf("Cloudflare 設定變更 [%s]: %s ➔ %s",
// 			new.CFRecordType, old.CFOriginValue, new.CFOriginValue,
// 		))
// 	}

// 	// 3. 解析 IP (僅非 Cloudflare 域名)
// 	isCloudflareDomain := old.CFRecordType != ""
// 	if !isCloudflareDomain {
// 		addedIPs, removedIPs := diffIPs(old.ResolvedIPs, new.ResolvedIPs)
// 		if len(addedIPs) > 0 {
// 			changes = append(changes, fmt.Sprintf("新增解析 IP: %s", strings.Join(addedIPs, ", ")))
// 		}
// 		if len(removedIPs) > 0 {
// 			changes = append(changes, fmt.Sprintf("移除解析 IP: %s", strings.Join(removedIPs, ", ")))
// 		}
// 	}

// 	// [CASE 4] 錯誤訊息變更 (Error Msg Changed)
// 	// 如果狀態都是 Error，但訊息從 Timeout 變成 EOF
// 	// 根據您的需求：不發送通知，只更新 DB (ScanOne 步驟 5 已處理更新)
// 	if old.Status == domain.StatusConnectionError && new.Status == domain.StatusConnectionError {
// 		// Do nothing.
// 		// return empty changes means no notification.
// 	}
// 	// 4. 其他變更
// 	// if old.IsProxied != new.IsProxied {
// 	// 	status := "關閉 (直連)"
// 	// 	if new.IsProxied {
// 	// 		status = "開啟 (保護中)"
// 	// 	}
// 	// 	changes = append(changes, fmt.Sprintf("Cloudflare Proxy: %s", status))
// 	// }
// 	// 4. Proxy 變更
// 	if old.IsProxied != new.IsProxied {
// 		statusOld := "🛡 DNS Only"
// 		if old.IsProxied {
// 			statusOld = "☁️ Proxy"
// 		}
// 		statusNew := "🛡 DNS Only"
// 		if new.IsProxied {
// 			statusNew = "☁️ Proxy"
// 		}
// 		changes = append(changes, fmt.Sprintf("⚡ <b>代理狀態</b>: %s ➔ %s", statusOld, statusNew))
// 	}
// 	if old.Issuer != new.Issuer && new.Issuer != "" {
// 		changes = append(changes, fmt.Sprintf("發行商: %s ➔ %s", old.Issuer, new.Issuer))
// 	}
// 	if old.ErrorMsg != new.ErrorMsg {
// 		if new.ErrorMsg != "" {
// 			changes = append(changes, fmt.Sprintf("錯誤: %s", new.ErrorMsg))
// 		} else {
// 			changes = append(changes, "錯誤已修復")
// 		}
// 	}

// 	if old.ErrorMsg != new.ErrorMsg {

// 		// [新增條件] 如果新狀態是 "連線錯誤"，我們不希望因為錯誤文字改變 (e.g. Timeout -> EOF) 而發送通知
// 		// 我們只關心 "錯誤修復" (即 new.ErrorMsg 變為空)

// 		if new.Status == domain.StatusConnectionError {
// 			// 靜音：不加入 changes，只讓 UpdateCertInfo 更新 DB 即可
// 		} else if new.ErrorMsg != "" {
// 			// 其他狀態下的錯誤訊息變更 (保留)
// 			changes = append(changes, fmt.Sprintf("錯誤: %s", new.ErrorMsg))
// 		} else {
// 			// ErrorMsg 變為空，代表修復
// 			// 但通常 Status 變更 (Error -> Active) 那邊已經會加 "✅ 連線已恢復"
// 			// 為了避免重複，這裡可以選擇不加，或者加個保險
// 			// changes = append(changes, "錯誤已修復")
// 		}
// 	}

// 	// [新增] 錯誤訊息變更 (Error Message Diff)
// 	// 如果狀態沒變但錯誤訊息變了 (例如從 timeout 變成 connection refused)，也可以考慮加進去
// 	if new.Status == domain.StatusConnectionError && old.ErrorMsg != new.ErrorMsg {
// 		// 這裡選擇不加，避免洗版。因為 CheckAndNotify 會發送當下的錯誤。
// 	}

// 	return changes
// }

func (s *ScannerService) generateDiff(old, new domain.SSLCertificate) []string {
	var changes []string

	// 1. [續簽檢測]
	if !old.NotAfter.IsZero() && new.NotAfter.After(old.NotAfter.Add(24*time.Hour)) {
		logrus.Infof("♻️ [Diff] 偵測到續簽: %s", new.DomainName)
		change := fmt.Sprintf(
			"♻️ <b>SSL 憑證已更新 (Renewed)</b>\n"+
				"      📅 舊到期日: <del>%s</del>\n"+
				"      📅 新到期日: <b>%s</b>\n"+
				"      ⏳ 剩餘天數: <b>%d 天</b>",
			old.NotAfter.Format("2006-01-02"),
			new.NotAfter.Format("2006-01-02"),
			new.DaysRemaining,
		)
		changes = append(changes, change)
	}

	// 2. [狀態變更檢測]
	if old.Status != new.Status {
		if new.Status == domain.StatusConnectionError {
			// [忽略] 變成連線錯誤時，不產生變更通知 (交給 CheckAndNotify 發告警)
		} else if old.Status == domain.StatusConnectionError && new.Status == domain.StatusActive {
			// [通知] 連線恢復
			changes = append(changes, fmt.Sprintf("✅ <b>連線已恢復</b>\n      狀態: %s ➔ %s", old.Status, new.Status))
		} else {
			// [通知] 其他狀態變更
			changes = append(changes, fmt.Sprintf("狀態: %s ➔ %s", old.Status, new.Status))
		}
	}

	// 3. [Cloudflare 設定檢測]
	if old.CFOriginValue != new.CFOriginValue || old.CFRecordType != new.CFRecordType {
		changes = append(changes, fmt.Sprintf("Cloudflare 設定變更 [%s]: %s ➔ %s",
			new.CFRecordType, old.CFOriginValue, new.CFOriginValue,
		))
	}

	// 4. [Proxy 狀態檢測]
	if old.IsProxied != new.IsProxied {
		statusOld := "🛡 DNS Only"
		if old.IsProxied {
			statusOld = "☁️ Proxy"
		}
		statusNew := "🛡 DNS Only"
		if new.IsProxied {
			statusNew = "☁️ Proxy"
		}
		changes = append(changes, fmt.Sprintf("⚡ <b>代理狀態</b>: %s ➔ %s", statusOld, statusNew))
	}

	// 5. [Error Message 檢測] (這是導致您問題的元兇)
	if old.ErrorMsg != new.ErrorMsg {
		// [關鍵] 只有當新狀態 "不是" 連線錯誤時，才報告錯誤訊息變更
		// 這樣就能過濾掉 "Timeout" -> "EOF" 這種無意義的通知
		if new.Status != domain.StatusConnectionError && new.ErrorMsg != "" {
			changes = append(changes, fmt.Sprintf("錯誤: %s", new.ErrorMsg))
		}
	}

	return changes
}

func (s *ScannerService) withRetry(ctx context.Context, attempts int, initialDelay time.Duration, op func() error) error {
	var err error
	for i := 0; i < attempts; i++ {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err = op(); err == nil {
			return nil
		}
		if i < attempts-1 {
			sleepTime := initialDelay * time.Duration(1<<i) // 指數退避
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(sleepTime):
			}
		}
	}
	return err
}

func (s *ScannerService) parseWhoisTime(dateStr string) (time.Time, error) {
	formats := []string{
		"2006-01-02 15:04:05", // [Added] Matches '2026-06-17 13:11:45'
		"2006-01-02T15:04:05Z",
		"2006-01-02T15:04:05.00Z",
		time.RFC3339,
		"2006-01-02",
		"02-Jan-2006",
		"2006.01.02",
	}
	for _, f := range formats {
		if t, e := time.Parse(f, dateStr); e == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("unknown date format: %s", dateStr)
}

func (s *ScannerService) parseDialError(err error) string {
	errMsg := err.Error()
	if strings.Contains(errMsg, "no such host") {
		return "DNS 解析失敗 (No such host)"
	}
	if strings.Contains(errMsg, "i/o timeout") {
		return "連線逾時 (Timeout)"
	}
	if strings.Contains(errMsg, "connection refused") {
		return "連線被拒 (Connection Refused)"
	}
	if strings.Contains(errMsg, "handshake failure") {
		return "SSL 握手失敗 (Handshake Fail)"
	}
	// 回傳原始錯誤，方便除錯
	return errMsg
}

func getRootDomain(domainName string) string {
	root, err := publicsuffix.EffectiveTLDPlusOne(domainName)
	if err != nil {
		return domainName
	}
	return root
}

func diffIPs(oldIPs, newIPs []string) (added []string, removed []string) {
	oldMap := make(map[string]bool)
	newMap := make(map[string]bool)
	for _, ip := range oldIPs {
		oldMap[ip] = true
	}
	for _, ip := range newIPs {
		newMap[ip] = true
	}
	for _, ip := range newIPs {
		if !oldMap[ip] {
			added = append(added, ip)
		}
	}
	for _, ip := range oldIPs {
		if !newMap[ip] {
			removed = append(removed, ip)
		}
	}
	return
}

// logTaskResult 集中處理掃描結果的 Log 輸出
func logTaskResult(domainName string, status string, latency int64, err error) {
	if err != nil {
		logrus.Errorf("XXX [Fail ] 結束: %s | 錯誤: %v", domainName, err)
	} else {
		logrus.Infof("<<< [End  ] 結束: %s | 狀態: %s | 耗時: %dms",
			domainName, status, latency,
		)
	}
}

// [新增] InspectDomain: 提供給工具類 API 使用，不寫入 DB，只回傳即時掃描結果
func (s *ScannerService) InspectDomain(ctx context.Context, domainName string, port int) (domain.SSLCertificate, error) {
	// 1. 執行 SSL 與 網路檢查
	result := s.PerformNetworkScan(ctx, domainName, port)

	// 2. 執行 WHOIS 查詢
	// 因為是即時工具，我們強制查詢一次
	rootDomain := getRootDomain(domainName)
	expiryDate, daysLeft, err := s.fetchWhoisInfo(rootDomain)
	if err == nil {
		result.DomainExpiryDate = expiryDate
		result.DomainDaysLeft = daysLeft
	} else {
		logrus.Warnf("InspectDomain WHOIS failed: %v", err)
		// WHOIS 失敗不應阻擋 SSL 結果的回傳，只是欄位會是空值
	}

	return result, nil
}

// 變數定義
var tlsVersions = map[uint16]string{
	tls.VersionTLS10: "TLS 1.0",
	tls.VersionTLS11: "TLS 1.1",
	tls.VersionTLS12: "TLS 1.2",
	tls.VersionTLS13: "TLS 1.3",
}
