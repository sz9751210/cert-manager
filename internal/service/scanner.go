package service

import (
	"cert-manager/internal/domain"
	"cert-manager/internal/repository"
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/likexian/whois"
	whois_parser "github.com/likexian/whois-parser"
	"github.com/sirupsen/logrus"
)

type ScannerService struct {
	Repo     repository.DomainRepository
	Notifier *NotifierService
}

func NewScannerService(repo repository.DomainRepository, notifier *NotifierService) *ScannerService {
	return &ScannerService{Repo: repo, Notifier: notifier}
}

// ScanAll 啟動併發掃描
func (s *ScannerService) ScanAll(ctx context.Context) error {
	// 1. 撈出所有域名 (暫時不分頁，全撈)
	// 在真實生產環境，這裡應該只撈 "is_ignored: false" 的域名
	domains, _, err := s.Repo.List(ctx, 1, 10000, "", "", "", "false", "")
	if err != nil {
		return err
	}

	logrus.Infof("開始掃描 %d 個域名...", len(domains))

	// 定義併發數量 (例如同時 10 個 Worker)
	concurrency := 10
	// 使用 buffered channel 作為信號量 (Semaphore) 來控制併發
	sem := make(chan struct{}, concurrency)

	// 使用 WaitGroup 和 Channel 控制併發 (Worker Pool 模式)
	var wg sync.WaitGroup

	for _, d := range domains {
		wg.Add(1)
		sem <- struct{}{} // 搶票

		go func(cert domain.SSLCertificate) {
			defer wg.Done()
			defer func() { <-sem }() // 還票

			s.checkAndUpdate(ctx, cert)
		}(d)
	}

	wg.Wait()
	logrus.Info("所有域名掃描完成")
	return nil
}

// checkAndUpdate 單個域名的檢查邏輯
func (s *ScannerService) checkAndUpdate(ctx context.Context, d domain.SSLCertificate) {
	logrus.Debugf("Checking: %s", d.DomainName)

	start := time.Now()

	// [新增] 解析 IP (為了在通知中顯示)
	// 這裡簡單取第一個 IPv4
	ips, err := net.LookupIP(d.DomainName)
	if err == nil && len(ips) > 0 {
		d.ResolvedIP = ips[0].String()
	}
	// --- 1. HTTP 檢查 (加入重試) ---
	// 重試 3 次，初始等待 1 秒
	err = withRetry(3, 1*time.Second, func() error {
		httpClient := &http.Client{
			Timeout:   5 * time.Second,
			Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}},
		}
		resp, err := httpClient.Get("https://" + d.DomainName)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		d.HTTPStatusCode = resp.StatusCode
		// 如果 HTTP 狀態碼是 5xx，我們也可以視為錯誤並重試 (看您的需求)
		if resp.StatusCode >= 500 {
			return fmt.Errorf("server error: %d", resp.StatusCode)
		}
		return nil
	})

	if err != nil {
		// 如果連不上 (例如 DNS 錯誤或連線被拒)
		d.HTTPStatusCode = 0
		// 這裡不一定要設為 Unresolvable，因為可能只是 Web Server 掛了但 IP 還在
		// 為了簡單，我們先記錄錯誤
		d.ErrorMsg = fmt.Sprintf("HTTP Check Failed: %v", err)
	} else {
		// HTTP 成功，清除錯誤訊息

		// 如果 HTTP 成功，清除之前的錯誤訊息 (除非 SSL 還有錯)
		if d.Status != domain.StatusExpired {
			d.ErrorMsg = ""
		}
	}

	// 計算延遲
	d.Latency = time.Since(start).Milliseconds()

	// --- 2. TLS/SSL 檢查 (加入重試) ---
	// 這一步最重要，避免 SSL 握手失敗導致誤判
	err = withRetry(3, 1*time.Second, func() error {
		dialer := &net.Dialer{Timeout: 5 * time.Second}
		conn, err := tls.DialWithDialer(dialer, "tcp", d.DomainName+":443", &tls.Config{
			InsecureSkipVerify: true,
		})
		if err != nil {
			logrus.Warnf("連線失敗 %s: %v", d.DomainName, err)
			d.Status = domain.StatusUnresolvable
			d.ErrorMsg = err.Error()
			return err
		} else {
			defer conn.Close()

			state := conn.ConnectionState()
			certs := state.PeerCertificates

			// 解析 TLS 版本
			switch state.Version {
			case tls.VersionTLS10:
				d.TLSVersion = "TLS 1.0"
			case tls.VersionTLS11:
				d.TLSVersion = "TLS 1.1"
			case tls.VersionTLS12:
				d.TLSVersion = "TLS 1.2"
			case tls.VersionTLS13:
				d.TLSVersion = "TLS 1.3"
			default:
				d.TLSVersion = "Unknown"
			}

			if len(certs) > 0 {
				cert := certs[0] // 拿第一張 (Server Certificate)
				d.Issuer = cert.Issuer.CommonName
				d.NotBefore = cert.NotBefore
				d.NotAfter = cert.NotAfter
				d.DaysRemaining = int(time.Until(cert.NotAfter).Hours() / 24)
				d.SANs = cert.DNSNames
				// 判斷狀態
				if d.DaysRemaining < 0 {
					d.Status = domain.StatusExpired
				} else if d.DaysRemaining < 30 {
					d.Status = domain.StatusWarning
				} else {
					d.Status = domain.StatusActive
				}
			}
			return nil
		}
	})

	if err != nil {
		// 只有在重試 3 次都失敗後，才判定為無法解析
		d.Status = domain.StatusUnresolvable
		d.ErrorMsg = fmt.Sprintf("SSL Handshake Failed: %v", err)
	}

	// --- 步驟 3: 網域 Whois 查詢 (新增) ---
	// 只有當域名是 "active" 且不是內網域名時才查 (避免查 localhost 或 internal DNS)
	// 這裡簡單用「是否有點」來判斷，或是依賴使用者標記
	if d.Status != domain.StatusUnresolvable {
		// 使用根域名查詢 (例如 api.google.com -> google.com)
		// 這裡為了簡單，我們先直接查完整域名，whois client 通常夠聰明能處理
		raw, err := whois.Whois(d.DomainName)
		if err == nil {
			result, err := whois_parser.Parse(raw)
			if err == nil && result.Domain.ExpirationDateInTime != nil {
				d.DomainExpiryDate = *result.Domain.ExpirationDateInTime
				d.DomainDaysLeft = int(time.Until(d.DomainExpiryDate).Hours() / 24)
			}
		} else {
			// Whois 失敗通常不記錄 error_msg，以免干擾 SSL 的錯誤顯示
			logrus.Warnf("Whois 查詢失敗 %s: %v", d.DomainName, err)
		}
	}

	// 寫回資料庫
	if err := s.Repo.UpdateCertInfo(ctx, d); err != nil {
		logrus.Errorf("更新資料庫失敗 %s: %v", d.DomainName, err)
	}
	s.Notifier.CheckAndNotify(ctx, d)
}

// 輔助函式：帶有重試機制的執行器
// attempts: 最大嘗試次數 (例如 3)
// initialDelay: 初始等待時間 (例如 1秒)
// operation: 要執行的邏輯，回傳 error 代表失敗
func withRetry(attempts int, initialDelay time.Duration, operation func() error) error {
	var err error
	for i := 0; i < attempts; i++ {
		if err = operation(); err == nil {
			return nil // 成功，直接返回
		}

		// 如果不是最後一次，就等待
		if i < attempts-1 {
			// 指數退避: 1s -> 2s -> 4s
			sleepTime := initialDelay * time.Duration(1<<i)
			logrus.Warnf("連線失敗，%s 後重試... (錯誤: %v)", sleepTime, err)
			time.Sleep(sleepTime)
		}
	}
	return err // 最後一次還是失敗，回傳錯誤
}
