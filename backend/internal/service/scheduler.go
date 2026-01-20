// package service

// import (
// 	"cert-manager/internal/repository"
// 	"context"

// 	"github.com/robfig/cron/v3"
// 	"github.com/sirupsen/logrus"
// )

// type SchedulerService struct {
// 	Cron    *cron.Cron
// 	Scanner *ScannerService
// 	CF      *CloudflareService
// 	Repo    repository.DomainRepository // 假設您需要直接操作 Repo
// }

// func NewSchedulerService(scanner *ScannerService, cf *CloudflareService) *SchedulerService {
// 	// 使用標準 parser (支援 5 個欄位: 分 時 日 月 週)
// 	c := cron.New()
// 	return &SchedulerService{
// 		Cron:    c,
// 		Scanner: scanner,
// 		CF:      cf,
// 	}
// }

// // Start 啟動排程
// func (s *SchedulerService) Start() {
// 	// 1. 每天凌晨 02:00 自動同步 Cloudflare (確保有新域名進來)
// 	// Cron 表達式: "0 2 * * *"
// 	_, err := s.Cron.AddFunc("0 2 * * *", func() {
// 		logrus.Info("[Cron] 開始自動同步 Cloudflare...")
// 		// 這裡需要 Context，我們建立一個背景的
// 		if _, err := s.CF.FetchDomains(context.Background()); err != nil {
// 			logrus.Errorf("[Cron] Cloudflare 同步失敗: %v", err)
// 		}
// 	})
// 	if err != nil {
// 		logrus.Error(err)
// 	}

// 	// 2. 每天凌晨 02:30 自動掃描 SSL 並告警
// 	_, err = s.Cron.AddFunc("30 2 * * *", func() {
// 		logrus.Info("[Cron] 開始自動掃描 SSL...")
// 		if err := s.Scanner.ScanAll(context.Background()); err != nil {
// 			logrus.Errorf("[Cron] SSL 掃描失敗: %v", err)
// 		}
// 	})
// 	if err != nil {
// 		logrus.Error(err)
// 	}

// 	s.Cron.Start()
// 	logrus.Info("自動排程服務已啟動 (每日 02:00 同步, 02:30 掃描)")
// }

// // Stop 停止排程
// func (s *SchedulerService) Stop() {
// 	s.Cron.Stop()
// }


package service

import (
    "cert-manager/internal/domain"
    "cert-manager/internal/repository"
    "context"
    "sync"
    "sync/atomic"
    "time"

    "github.com/robfig/cron/v3"
    "github.com/sirupsen/logrus"
)

type SchedulerService struct {
    Cron     *cron.Cron
    Scanner  *ScannerService
    CF       *CloudflareService
    Repo     repository.DomainRepository
    Notifier *NotifierService
}

func NewSchedulerService(scanner *ScannerService, cf *CloudflareService, repo repository.DomainRepository, notifier *NotifierService) *SchedulerService {
    c := cron.New()
    return &SchedulerService{
        Cron:     c,
        Scanner:  scanner,
        CF:       cf,
        Repo:     repo,
        Notifier: notifier,
    }
}

// Start 啟動排程
func (s *SchedulerService) Start() {
    // 1. 每天凌晨 02:00 自動同步 Cloudflare (Pipeline 模式)
    // 這會執行: 抓取 -> 寫入 DB (Pending) -> 立即掃描 -> 更新狀態
    _, err := s.Cron.AddFunc("0 2 * * *", func() {
        logrus.Info("⏰ [Scheduler] 觸發排程任務: Cloudflare 同步 (Pipeline)")
        if _, err := s.PerformSync(context.Background()); err != nil {
            logrus.Errorf("❌ [Scheduler] Cloudflare 同步失敗: %v", err)
        }
    })
    if err != nil {
        logrus.Error(err)
    }

    // 2. 每天凌晨 02:30 執行全量深度掃描 (Double Check)
    // 這會對資料庫內 "所有" 域名 (包含手動新增的) 再次進行檢查
    _, err = s.Cron.AddFunc("30 2 * * *", func() {
        logrus.Info("⏰ [Scheduler] 觸發排程任務: 全量深度掃描")
        s.PerformScan(context.Background())
    })
    if err != nil {
        logrus.Error(err)
    }

    s.Cron.Start()
    logrus.Info("✅ 自動排程服務已啟動 (每日 02:00 同步, 02:30 掃描)")
}

// Stop 停止排程
func (s *SchedulerService) Stop() {
    s.Cron.Stop()
}

// =============================================================================
// 以下為核心邏輯 (Pipeline & Consumers)
// =============================================================================

// PerformSync 執行同步流程 (Pipeline Mode)
func (s *SchedulerService) PerformSync(ctx context.Context) (SyncStats, error) {
    start := time.Now()
    stats := SyncStats{}

    logrus.Info("🚀 [Sync] 開始執行同步任務 (Pipeline Mode)...")

    // 1. 讀取 DB 現有資料 (用於比對)
    dbDomains, _, err := s.Repo.List(ctx, 1, 100000, "", "", "", "", "all", "")
    if err != nil {
        return stats, err
    }
    dbMap := make(map[string]domain.SSLCertificate)
    for _, d := range dbDomains {
        dbMap[d.DomainName] = d
    }

    // 2. 建立 Pipeline 通道
    domainStream := make(chan domain.SSLCertificate, 500)
    
    // 收集器 (用於刪除比對)
    var allCFDomains []domain.SSLCertificate
    var cfMutex sync.Mutex
    newZones := make(map[string]bool) // 這裡簡化 Zone 偵測邏輯，您可視需求加上

    // 3. 啟動 Cloudflare 抓取 (生產者)
    errChan := make(chan error, 1)
    go func() {
        defer close(domainStream)
        // 注意：這裡呼叫的是修改後支援 Channel 的 CF.FetchDomains
        if err := s.CF.FetchDomains(ctx, domainStream); err != nil {
            errChan <- err
        }
    }()

    // 4. 啟動處理邏輯 (消費者) - 這會卡住直到通道關閉
    s.processUpsertsStream(ctx, domainStream, dbMap, &stats, newZones, &allCFDomains, &cfMutex)

    // 檢查抓取錯誤
    select {
    case err := <-errChan:
        return stats, err
    default:
    }

    // 5. 處理刪除
    logrus.Info("🗑 [Sync] 開始檢查已刪除的域名...")
    s.processDeletions(ctx, allCFDomains, dbDomains, &stats)

    stats.Duration = time.Since(start).String()
    
    // 發送同步結果通知
    s.notifySyncResult(ctx, stats)
    
    logrus.Infof("🏁 [Sync] 同步完成 (耗時: %s)", stats.Duration)
    return stats, nil
}

// processUpsertsStream 流水線消費者
func (s *SchedulerService) processUpsertsStream(
    ctx context.Context,
    domainStream <-chan domain.SSLCertificate,
    dbMap map[string]domain.SSLCertificate,
    stats *SyncStats,
    newZones map[string]bool,
    allCFDomains *[]domain.SSLCertificate,
    cfMutex *sync.Mutex,
) {
    concurrency := 15
    sem := make(chan struct{}, concurrency)
    var wg sync.WaitGroup
    var mu sync.Mutex

    var processedCount int32 = 0

    logrus.Info("⚡ [Pipeline] 掃描器就緒，等待資料流入...")

    for cfD := range domainStream {
        // 收集總表
        cfMutex.Lock()
        *allCFDomains = append(*allCFDomains, cfD)
        cfMutex.Unlock()

        if shouldSkipDomain(cfD.DomainName) {
            continue
        }

        wg.Add(1)
        go func(targetCert domain.SSLCertificate) {
            sem <- struct{}{}
            defer func() {
                <-sem
                wg.Done()
                // Log 進度
                current := atomic.AddInt32(&processedCount, 1)
                if current%20 == 0 {
                    logrus.Infof("⏳ [Stream] 已處理: %d 筆", current)
                }
            }()

            existing, exists := dbMap[targetCert.DomainName]
            scanPort := targetCert.Port
            if exists {
                targetCert.ID = existing.ID
                targetCert.IsIgnored = existing.IsIgnored
                targetCert.Port = existing.Port
                scanPort = existing.Port
                targetCert.LastCheckTime = existing.LastCheckTime
            }

            // 執行網路掃描
            sslResult := s.Scanner.PerformNetworkScan(ctx, targetCert.DomainName, scanPort)
            s.mergeSSLResult(&targetCert, sslResult)

            // 寫入資料庫
            s.Repo.Upsert(ctx, targetCert)

            // 處理通知與統計 (簡化版)
            if exists {
                // ... (此處放入您的 Diff / Renew 通知邏輯) ...
                 if targetCert.NotAfter.After(existing.NotAfter.Add(24 * time.Hour)) {
                    s.Notifier.NotifyOperation(ctx, EventRenew, targetCert.DomainName, "SSL 已續簽")
                 }
            } else {
                mu.Lock()
                stats.Added++
                mu.Unlock()
                // 新增通知...
            }
        }(cfD)
    }
    wg.Wait()
}

// PerformScan 執行全量掃描 (02:30 的任務)
func (s *SchedulerService) PerformScan(ctx context.Context) {
    if err := s.Scanner.ScanAll(ctx); err != nil {
        logrus.Errorf("❌ [Scan] 排程掃描失敗: %v", err)
    }
}

// func (s *SchedulerService) PerformScan(ctx context.Context) {
//     start := time.Now()
//     if err := s.Scanner.ScanAll(ctx); err != nil {
//         logrus.Errorf("❌ [Scan] 排程掃描失敗: %v", err)
//     } else {
//         duration := time.Since(start).String()
//         stats, _ := s.Repo.GetStatistics(ctx)
//         if stats != nil {
//             s.Notifier.NotifyTaskFinish(ctx, EventScanFinish, TaskSummaryData{
//                 Total:    int(stats.TotalDomains),
//                 Active:   stats.StatusCounts["active"],
//                 Expired:  stats.StatusCounts["expired"],
//                 Duration: duration,
//             })
//         }
//     }
// }

// processDeletions 刪除邏輯
func (s *SchedulerService) processDeletions(ctx context.Context, cfDomains []domain.SSLCertificate, dbDomains []domain.SSLCertificate, stats *SyncStats) {
    cfMap := make(map[string]bool)
    for _, d := range cfDomains {
        cfMap[d.DomainName] = true
    }
    for _, dbD := range dbDomains {
        if dbD.CFRecordType == "placeholder" { continue } // 簡單保護
        if !cfMap[dbD.DomainName] && !shouldSkipDomain(dbD.DomainName) {
            if err := s.Repo.Delete(ctx, dbD.ID); err == nil {
                stats.Deleted++
                stats.DeletedNames = append(stats.DeletedNames, dbD.DomainName)
            }
        }
    }
}

func (s *SchedulerService) mergeSSLResult(target *domain.SSLCertificate, result domain.SSLCertificate) {
    target.Issuer = result.Issuer
    target.NotAfter = result.NotAfter
    target.NotBefore = result.NotBefore
    target.DaysRemaining = result.DaysRemaining
    target.Status = result.Status
    target.ResolvedIPs = result.ResolvedIPs
    target.ResolvedRecord = result.ResolvedRecord
    target.TLSVersion = result.TLSVersion
    target.HTTPStatusCode = result.HTTPStatusCode
    target.IsMatch = result.IsMatch
    target.ErrorMsg = result.ErrorMsg
    target.LastCheckTime = time.Now()
}

func (s *SchedulerService) notifySyncResult(ctx context.Context, stats SyncStats) {
    // 這裡呼叫 s.Notifier.NotifyTaskFinish ...
    // 實作與之前相同
}

// 輔助函式 (shouldSkipDomain 等) 請直接貼上或保留在同個 package 下