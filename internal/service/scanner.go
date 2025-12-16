package service

import (
	"cert-manager/internal/domain"
	"cert-manager/internal/repository"
	"context"
	"crypto/tls"
	"net"
	"sync"
	"time"

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

	// 2. 使用 WaitGroup 和 Channel 控制併發 (Worker Pool 模式)
	var wg sync.WaitGroup
	sem := make(chan struct{}, 10) // 限制同時最多 10 個掃描連線，避免把自己頻寬塞爆

	for _, d := range domains {
		wg.Add(1)
		sem <- struct{}{} // 搶票

		go func(target domain.SSLCertificate) {
			defer wg.Done()
			defer func() { <-sem }() // 還票

			s.checkAndUpdate(ctx, target)
		}(d)
	}

	wg.Wait()
	logrus.Info("所有域名掃描完成")
	return nil
}

// checkAndUpdate 單個域名的檢查邏輯
func (s *ScannerService) checkAndUpdate(ctx context.Context, d domain.SSLCertificate) {
	logrus.Debugf("Checking: %s", d.DomainName)

	// 設定連線逾時，避免卡死
	dialer := &net.Dialer{
		Timeout: 5 * time.Second,
	}

	// 建立 TLS 連線 (忽略憑證信任鏈錯誤，因為我們主要只想看日期)
	conn, err := tls.DialWithDialer(dialer, "tcp", d.DomainName+":443", &tls.Config{
		InsecureSkipVerify: true,
	})

	if err != nil {
		logrus.Warnf("連線失敗 %s: %v", d.DomainName, err)
		d.Status = domain.StatusUnresolvable
		d.ErrorMsg = err.Error()
	} else {
		defer conn.Close()
		certs := conn.ConnectionState().PeerCertificates
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
			d.ErrorMsg = "" // 清除之前的錯誤
		}
	}

	// 寫回資料庫
	if err := s.Repo.UpdateCertInfo(ctx, d); err != nil {
		logrus.Errorf("更新資料庫失敗 %s: %v", d.DomainName, err)
	}
	s.Notifier.CheckAndNotify(ctx, d)
}
