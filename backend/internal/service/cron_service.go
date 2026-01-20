package service

import (
	"cert-manager/internal/domain"
	"cert-manager/internal/repository"
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/robfig/cron/v3"
	"github.com/sirupsen/logrus"
)

// SyncStats 記錄同步過程的統計數據
type SyncStats struct {
	Added        int
	AddedNames   []string
	Updated      int
	UpdatedNames []string
	Deleted      int
	DeletedNames []string
	Skipped      int
	Duration     string
}

type CronService struct {
	Cron      *cron.Cron
	Repo      repository.DomainRepository
	CFService *CloudflareService
	Scanner   *ScannerService
	Notifier  *NotifierService
	EntryIDs  map[string]cron.EntryID
}

func NewCronService(repo repository.DomainRepository, cf *CloudflareService, scan *ScannerService, notify *NotifierService) *CronService {
	return &CronService{
		Cron:      cron.New(),
		Repo:      repo,
		CFService: cf,
		Scanner:   scan,
		Notifier:  notify,
		EntryIDs:  make(map[string]cron.EntryID),
	}
}

// Start 啟動排程
func (s *CronService) Start() {
	s.ReloadJobs()
	s.Cron.Start()
}

// ReloadJobs 重新讀取資料庫設定並排程
func (s *CronService) ReloadJobs() {
	ctx := context.Background()
	settings, err := s.Repo.GetSettings(ctx)
	if err != nil {
		logrus.Error("無法讀取設定，略過排程啟動")
		return
	}

	// 1. 清除舊任務
	for _, id := range s.EntryIDs {
		s.Cron.Remove(id)
	}
	s.EntryIDs = make(map[string]cron.EntryID)

	// 2. 註冊同步任務
	if settings.SyncEnabled && settings.SyncSchedule != "" {
		s.registerJob("sync", settings.SyncSchedule, func() {
			if stats, err := s.PerformSync(context.Background()); err == nil {
				s.notifySyncResult(stats)
			}
		})
	}

	// 3. 註冊掃描任務
	if settings.ScanEnabled && settings.ScanSchedule != "" {
		s.registerJob("scan", settings.ScanSchedule, func() {
			s.PerformScan(context.Background())
		})
	}
}

// registerJob 封裝註冊邏輯
func (s *CronService) registerJob(name, schedule string, cmd func()) {
	id, err := s.Cron.AddFunc(schedule, cmd)
	if err == nil {
		s.EntryIDs[name] = id
		logrus.Infof("已排程自動任務 [%s]: %s", name, schedule)
	} else {
		logrus.Errorf("排程註冊失敗 [%s]: %v", name, err)
	}
}

// PerformSync 執行同步流程
func (s *CronService) PerformSync(ctx context.Context) (SyncStats, error) {
	start := time.Now()
	stats := SyncStats{}

	logrus.Info("🚀 [Cron] 開始執行同步任務 (Pipeline Mode)...")

	// 1. 從資料庫撈取現有域名以進行比對
	logrus.Info("📥 [Cron] 正在從資料庫撈取現有域名...")
	dbDomains, _, err := s.Repo.List(ctx, 1, 100000, "", "", "", "", "all", "")
	if err != nil {
		return stats, err
	}

	// 建立本地資料 Map 以加速查找
	dbMap := make(map[string]domain.SSLCertificate)
	// [新增] 建立已知 Zone 的 Map，用來判斷是否為新 Zone
	existingZones := make(map[string]bool)

	for _, d := range dbDomains {
		dbMap[d.DomainName] = d
		if d.ZoneName != "" {
			existingZones[d.ZoneName] = true
		}
	}

	// 2. 建立 Pipeline 通道
	domainStream := make(chan domain.SSLCertificate, 500)
	var allCFDomains []domain.SSLCertificate
	var cfMutex sync.Mutex

	// 3. 啟動 Cloudflare 抓取 (生產者)
	errChan := make(chan error, 1)
	go func() {
		defer close(domainStream)
		if err := s.CFService.FetchDomains(ctx, domainStream); err != nil {
			errChan <- err
		}
	}()

	// 4. 啟動處理邏輯 (消費者) - 這會執行 ScanOne
	logrus.Info("🔄 [Cron] 啟動即時掃描處理器...")

	// [修正] 不要在此時呼叫 detectZoneChanges，因為我們還沒有 cfDomains
	// 初始化一個空的 map 給 processUpsertsStream 用來動態記錄
	// newZones := make(map[string]bool)

	// 將新發現的 Zone 邏輯整合進 processUpsertsStream (見下步) 或保持現狀但 newZones 為空
	s.processUpsertsStream(ctx, domainStream, dbMap, &stats, existingZones, &allCFDomains, &cfMutex)

	// // 用來記錄新發現的 Zone，避免大量發送子域名新增通知
	// newZones := s.detectZoneChanges(ctx, nil, dbDomains) // 這裡先傳 nil，後面在 stream 裡動態判斷

	// s.processUpsertsStream(ctx, domainStream, dbMap, &stats, newZones, &allCFDomains, &cfMutex)

	// 檢查抓取是否有錯
	select {
	case err := <-errChan:
		return stats, err
	default:
	}

	// =================================================================
	// [關鍵修正] 安全閥 (Safety Valve)
	// 防止因為 API 失敗或網路問題導致抓到 0 筆，進而誤刪所有本地資料
	// =================================================================
	if len(allCFDomains) == 0 {
		if len(dbDomains) > 0 {
			logrus.Warnf("⚠️ [Safety] 本次同步未從 Cloudflare 獲取到任何域名 (但本地有 %d 筆)。", len(dbDomains))
			logrus.Warn("🛑 為防止誤刪資料，已強制略過刪除程序 (Deletion Skipped)。請檢查 API Token 權限或網路狀態。")

			stats.Duration = time.Since(start).String()
			return stats, fmt.Errorf("safety check triggered: 0 domains fetched from cloudflare")
		}
		// 如果本地原本也是空的，那就沒關係
	}
	// 5. 處理刪除
	logrus.Info("🗑 [Cron] 開始檢查已刪除的域名...")

	// [新增] 在這裡執行 Zone 的變更檢測，因為現在 allCFDomains 已經完整了
	s.detectZoneChanges(ctx, allCFDomains, dbDomains)

	s.processDeletions(ctx, allCFDomains, dbDomains, &stats)

	stats.Duration = time.Since(start).String()
	logrus.Infof("🏁 [Cron] 同步完成 (耗時: %s)", stats.Duration)

	return stats, nil
}

// service/cron_service.go
// processUpsertsStream 是核心的流水線處理器
// 它同時扮演消費者 (Consumer) 與 掃描調度者 (Dispatcher)
// processUpsertsStream 核心流水線：接收 CF 資料 -> 合併 DB 設定 -> 執行 ScanOne
func (s *CronService) processUpsertsStream(
	ctx context.Context,
	domainStream <-chan domain.SSLCertificate, // [輸入] 資料通道
	dbMap map[string]domain.SSLCertificate, // [對照] 資料庫現有資料
	stats *SyncStats, // [統計]
	existingZones map[string]bool, // [修改] 參數改為 existingZones (DB裡已知的)
	allCFDomains *[]domain.SSLCertificate, // [輸出] 收集所有抓到的域名
	cfMutex *sync.Mutex, // [鎖] 保護 allCFDomains
) {
	// 設定併發數 (建議 10-20)
	concurrency := 15
	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup
	var mu sync.Mutex // 保護 stats 寫入
	var processedCount int32 = 0

	// 追蹤用 Map (用於 Placeholder 清理)
	activeZonesWithRealData := make(map[string]bool)
	// zoneHasValidRecords := make(map[string]bool)
	// for z := range newZones {
	// 	zoneHasValidRecords[z] = false
	// }
	discoveredZones := make(map[string]bool)

	logrus.Info("⚡ [Pipeline] 掃描流水線啟動，正在處理資料流...")

	for cfD := range domainStream {
		// 1. 收集到總列表 (供刪除邏輯使用)
		cfMutex.Lock()
		*allCFDomains = append(*allCFDomains, cfD)
		cfMutex.Unlock()

		// [新增] 只要 Cloudflare 有這個 Zone，就記錄下來 (不管後面是否 skip)
		mu.Lock()
		discoveredZones[cfD.ZoneName] = true
		mu.Unlock()

		// 2. 過濾略過的域名
		if shouldSkipDomain(cfD.DomainName) {
			mu.Lock()
			stats.Skipped++
			mu.Unlock()
			atomic.AddInt32(&processedCount, 1)
			continue
		}

		// 3. 標記 Zone 狀態
		// if _, isNew := newZones[cfD.ZoneName]; isNew {
		// 	mu.Lock()
		// 	zoneHasValidRecords[cfD.ZoneName] = true
		// 	mu.Unlock()
		// }
		mu.Lock()
		// discoveredZones[cfD.ZoneName] = true
		activeZonesWithRealData[cfD.ZoneName] = true
		mu.Unlock()

		// 4. 啟動非同步掃描任務
		wg.Add(1)
		go func(sourceCF domain.SSLCertificate) {
			sem <- struct{}{} // 申請令牌
			defer func() {
				<-sem // 釋放令牌
				wg.Done()
				current := atomic.AddInt32(&processedCount, 1)
				if current%20 == 0 {
					logrus.Infof("⏳ [Stream] 已處理: %d 筆 | 最新完成: %s", current, sourceCF.DomainName)
				}
			}()

			// =========================================================
			// STEP 1: 狀態準備 (CF 資料 + DB 設定)
			// =========================================================
			existing, exists := dbMap[sourceCF.DomainName]
			var targetCert domain.SSLCertificate

			// existing, exists := dbMap[targetCert.DomainName]

			if exists {
				// [舊域名]：targetCert 目前是 Cloudflare 的最新資料
				// 使用 DB 裡的完整資料作為基底 (保留 ID, Port, CreatedAt, History...)
				targetCert = existing

				// 只更新來自 Cloudflare 的變動屬性
				targetCert.CFZoneID = sourceCF.CFZoneID
				targetCert.CFRecordID = sourceCF.CFRecordID
				targetCert.CFRecordType = sourceCF.CFRecordType
				targetCert.CFOriginValue = sourceCF.CFOriginValue
				targetCert.IsProxied = sourceCF.IsProxied
				targetCert.CFComment = sourceCF.CFComment
				
				// ZoneName 也更新一下，防止 CF 改名 (雖然罕見)
				targetCert.ZoneName = sourceCF.ZoneName

				// 注意：這裡完全不碰 ID, Port, IsIgnored, LastCheckTime
				// 它們都安全地保存在 targetCert (即 existing) 中
			} else {
				// // [新域名]：先 Upsert 一次 Pending 狀態
				// // 這是為了讓前端列表能馬上看到它，即便 ScanOne 還在跑
				// initialState := targetCert
				// initialState.Status = "pending"
				// if err := s.Repo.Upsert(ctx, initialState); err == nil {
				// 	// 這裡先不計入 Added，等 ScanOne 跑完再確認
				// 	logrus.Debugf("🆕 [Pending] 新域名入庫等待掃描: %s", targetCert.DomainName)
				// }
				// [情境 B: 新域名]
				// 使用 Cloudflare 資料作為基底
				targetCert = sourceCF

				// 初始化系統欄位
				targetCert.Status = "pending"
				// 嘗試寫入 Pending 狀態 (讓前端立刻有反應)
				if err := s.Repo.Upsert(ctx, targetCert); err == nil {
					logrus.Debugf("🆕 [Pending] 新域名入庫: %s", targetCert.DomainName)
				}
			}

			// =========================================================
			// STEP 2: [關鍵] 調用 ScanOne 進行完整掃描
			// ScanOne 內部會做：NetworkScan -> SyncWhois -> UpdateCertInfo -> Notify (SSL Diff)
			// =========================================================

			// 我們傳入 targetCert，ScanOne 會以此為基準進行掃描
			finalCert, _, err := s.Scanner.ScanOne(ctx, targetCert, false)

			if err != nil {
				logrus.Errorf("❌ [Scan Failed] %s: %v", targetCert.DomainName, err)
				return
			}

			// =========================================================
			// STEP 3: 處理 CF 相關變更通知 & 統計
			// ScanOne 負責 SSL 變更通知，CronService 負責 Cloudflare 設定變更通知
			// =========================================================

			if exists {
				if !existing.NotAfter.IsZero() && targetCert.NotAfter.After(existing.NotAfter.Add(24*time.Hour)) {

					// 1. [關鍵修改] 確保 Cloudflare 的變更 (如 Proxy 開關) 被儲存
					// 雖然 ScanOne 內部可能存了 SSL，但為了確保 CF 欄位同步，我們這裡再存一次。
					// 重點：必須使用 finalCert (它是最新的完全體)，絕對不能用 targetCert (會覆蓋掉 SSL)
					if err := s.Repo.Upsert(ctx, finalCert); err != nil { // <<<<< 關鍵修改：使用 finalCert
						logrus.Errorf("❌ [DB Error] 更新失敗 %s: %v", finalCert.DomainName, err)
					}
				}
				// [舊域名]：檢查 Cloudflare 設定是否變更 (Proxy, Origin, Type)
				// 注意：finalCert 是掃描後的新資料，existing 是資料庫裡的舊資料
				cfChanges := s.checkCFDiff(existing, finalCert)

				if len(cfChanges) > 0 {
					mu.Lock()
					stats.Updated++
					detailMsg := fmt.Sprintf("🔹 <b>%s</b>\n   ↳ %s",
						targetCert.DomainName,
						strings.Join(cfChanges, "\n   ↳ "))
					stats.UpdatedNames = append(stats.UpdatedNames, detailMsg)
					mu.Unlock()

					// 發送 CF 變更通知
					s.Notifier.NotifyOperation(ctx, EventUpdate, targetCert.DomainName, strings.Join(cfChanges, "\n"))
				}
			} else {
				// [新域名處理邏輯]

				// ScanOne 通常已經在內部做過儲存了。
				// 如果 ScanOne 內部用的是 UpdateCertInfo (只更新特定欄位)，
				// 這裡保險起見也可以 Upsert 一次 finalCert，確保完整。
				if err := s.Repo.Upsert(ctx, finalCert); err != nil {
					logrus.Errorf("❌ [DB Error] 新域名保存失敗 %s: %v", finalCert.DomainName, err)
				}

				mu.Lock()
				stats.Added++
				stats.AddedNames = append(stats.AddedNames, fmt.Sprintf("🔹 %s", targetCert.DomainName))
				mu.Unlock()

				// [關鍵修正] 判斷是否為新 Zone
				// 邏輯：如果這個 Zone 不在資料庫已知的 existingZones 裡，代表它是全新的 Zone
				isNewZone := !existingZones[targetCert.ZoneName]

				if isNewZone {
					// 如果是新 Zone，我們 **不發送** 單一子域名的通知
					// 因為稍後 detectZoneChanges 會發送「發現新主域名」的匯總通知
					logrus.Debugf("🔕 [Muted] 新增子域名 %s (因屬於新發現的主域名 %s)", targetCert.DomainName, targetCert.ZoneName)
				} else {
					// 如果是既有 Zone 下的新增子域名，正常發送通知
					s.sendNewDomainNotification(ctx, finalCert)
				}

				// 如果不是屬於新發現的 Zone (新 Zone 會發匯總通知)，則發送單獨通知
				// if !newZones[targetCert.ZoneName] {
				// s.sendNewDomainNotification(ctx, finalCert)
				// }
			}

		}(cfD)
	}

	wg.Wait()
	logrus.Infof("✅ [Pipeline] 所有資料處理完畢 (共 %d 筆)", atomic.LoadInt32(&processedCount))

	// 清理過期的 Placeholder (代碼保持你原本的邏輯)
	s.cleanupPlaceholders(ctx, dbMap, activeZonesWithRealData, discoveredZones, stats)
}

// sendNewDomainNotification 封裝新域名通知邏輯
func (s *CronService) sendNewDomainNotification(ctx context.Context, cert domain.SSLCertificate) {
	statusProxy := "☁️ Proxy (橘雲)"
	if !cert.IsProxied {
		statusProxy = "🛡 DNS Only (灰雲)"
	}

	statusText := "✅ 正常"
	if cert.Status != "active" {
		statusText = fmt.Sprintf("⚠️ %s", cert.Status)
	}

	details := fmt.Sprintf(
		"🎯 <b>目標</b>: <code>%s</code>\n"+
			"🏷 <b>類型</b>: %s\n"+
			"⚡ <b>Proxy</b>: %s\n"+
			"📊 <b>狀態</b>: %s\n"+
			"📅 <b>域名到期</b>: %s",
		cert.CFOriginValue,
		cert.CFRecordType,
		statusProxy,
		statusText,
		func() string {
			if cert.DomainExpiryDate.IsZero() {
				return "Unknown"
			}
			return cert.DomainExpiryDate.Format("2006-01-02")
		}(),
	)
	s.Notifier.NotifyOperation(ctx, EventAdd, cert.DomainName, details)
}

// cleanupPlaceholders 清理與建立 Placeholder
func (s *CronService) cleanupPlaceholders(
	ctx context.Context,
	dbMap map[string]domain.SSLCertificate,
	activeZonesWithRealData map[string]bool,
	discoveredZones map[string]bool,
	stats *SyncStats,
) {
	// 1. 清除過期的 Placeholder
	for _, dbRecord := range dbMap {
		if dbRecord.CFRecordType == "placeholder" {
			if activeZonesWithRealData[dbRecord.ZoneName] {
				logrus.Infof("🧹 [Cleanup] 清除過期佔位符: %s", dbRecord.DomainName)
				if err := s.Repo.Delete(ctx, dbRecord.ID); err == nil {
					stats.Deleted++
					stats.DeletedNames = append(stats.DeletedNames, fmt.Sprintf("佔位符清理: %s", dbRecord.DomainName))
				}
			}
		}
	}

	// 2. 建立新的 Zone Placeholder
	// 邏輯：如果一個 Zone 這次有被掃描到 (discoveredZones)，
	// 但它卻沒有任何有效的子域名被寫入 (即不在 activeZonesWithRealData 中)，
	// 且資料庫裡也沒有它的紀錄 (dbMap check)，則建立一個 Placeholder。
	for zoneName := range discoveredZones {
		if !activeZonesWithRealData[zoneName] {
			// 檢查 DB 是否已經有這個主域名本身的紀錄 (避免重複建立)
			// 注意：這裡檢查 dbMap 是為了防止即使有 placeholder 了還重複建立
			// 但因為 dbMap 是以 DomainName 為 key，而 Placeholder 的 DomainName 通常等於 ZoneName
			if _, exists := dbMap[zoneName]; !exists {
				logrus.Infof("🛡 [Zone Placeholder] 為全被過濾的 Zone 建立佔位符: %s", zoneName)

				placeholder := domain.SSLCertificate{
					DomainName:       zoneName, // 使用主域名作為名稱
					ZoneName:         zoneName,
					Status:           "skipped_zone",
					IsIgnored:        true,
					CFRecordType:     "placeholder",
					CFOriginValue:    "Auto Generated Placeholder",
					DomainExpiryDate: time.Time{}, // 這裡可以填入 Zone 的到期日如果有的話，但目前沒傳進來
				}
				if err := s.Repo.Create(ctx, placeholder); err != nil {
					logrus.Errorf("❌ 建立 Zone 佔位符失敗 %s: %v", zoneName, err)
				}
			}
		}
	}
}

// 	// 2. 建立新的 Zone Placeholder
// 	for zoneName, hasValid := range discoveredZones {
// 		if !hasValid {
// 			if _, exists := dbMap[zoneName]; !exists {
// 				logrus.Infof("🛡 [Zone Placeholder] 為空 Zone 建立佔位符: %s", zoneName)
// 				placeholder := domain.SSLCertificate{
// 					DomainName:       zoneName,
// 					ZoneName:         zoneName,
// 					Status:           "skipped_zone",
// 					IsIgnored:        true,
// 					CFRecordType:     "placeholder",
// 					CFOriginValue:    "Auto Generated Placeholder",
// 					DomainExpiryDate: time.Time{},
// 				}
// 				s.Repo.Create(ctx, placeholder)
// 			}
// 		}
// 	}
// }

// processUpsertsStream 是核心的流水線處理器
// 它同時扮演消費者 (Consumer) 與 掃描調度者 (Dispatcher)
// func (s *CronService) processUpsertsStream(
// 	ctx context.Context,
// 	domainStream <-chan domain.SSLCertificate, // [輸入] 資料通道
// 	dbMap map[string]domain.SSLCertificate, // [對照] 資料庫現有資料
// 	stats *SyncStats, // [統計]
// 	newZones map[string]bool, // [資訊] 新發現的 Zone
// 	allCFDomains *[]domain.SSLCertificate, // [輸出] 收集所有抓到的域名 (供刪除邏輯用)
// 	cfMutex *sync.Mutex, // [鎖] 保護 allCFDomains
// ) {
// 	// 設定掃描併發數 (建議 10-20，視伺服器性能與網路而定)
// 	concurrency := 15
// 	sem := make(chan struct{}, concurrency)
// 	var wg sync.WaitGroup
// 	var mu sync.Mutex // 保護 stats 寫入

// 	// 原子計數器 (用於 Log 進度顯示)
// 	var processedCount int32 = 0

// 	// 追蹤用 Map (用於 Placeholder 清理邏輯)
// 	activeZonesWithRealData := make(map[string]bool)
// 	zoneHasValidRecords := make(map[string]bool)
// 	// 初始化 Zone 狀態
// 	for z := range newZones {
// 		zoneHasValidRecords[z] = false
// 	}

// 	logrus.Info("⚡ [Pipeline] 掃描流水線啟動，正在處理資料流...")

// 	// [主迴圈] 持續從通道讀取，直到 CloudflareService 關閉通道
// 	for cfD := range domainStream {

// 		// 1. 收集到總列表 (這一步很重要，刪除邏輯依賴這個列表)
// 		cfMutex.Lock()
// 		*allCFDomains = append(*allCFDomains, cfD)
// 		cfMutex.Unlock()

// 		// 2. 過濾略過的域名
// 		if shouldSkipDomain(cfD.DomainName) {
// 			mu.Lock()
// 			stats.Skipped++
// 			mu.Unlock()
// 			atomic.AddInt32(&processedCount, 1)
// 			continue
// 		}

// 		// 3. 標記 Zone 狀態
// 		if _, isNew := newZones[cfD.ZoneName]; isNew {
// 			mu.Lock()
// 			zoneHasValidRecords[cfD.ZoneName] = true
// 			mu.Unlock()
// 		}
// 		mu.Lock()
// 		activeZonesWithRealData[cfD.ZoneName] = true
// 		mu.Unlock()

// 		// 4. 啟動非同步掃描任務
// 		wg.Add(1)
// 		go func(targetCert domain.SSLCertificate) {
// 			// 申請流量控制令牌 (若滿了會在這裡等待)
// 			sem <- struct{}{}

// 			defer func() {
// 				<-sem // 釋放令牌
// 				wg.Done()

// 				// 進度 Log (每處理 20 筆顯示一次，讓您知道還在跑)
// 				current := atomic.AddInt32(&processedCount, 1)
// 				if current%20 == 0 {
// 					logrus.Infof("⏳ [Stream] 已處理: %d 筆 | 最新完成: %s", current, targetCert.DomainName)
// 				}
// 			}()

// 			// 檢查是否為舊資料
// 			existing, exists := dbMap[targetCert.DomainName]

// 			// 決定掃描用的 Port (若是舊資料，使用使用者設定的 Port)
// 			scanPort := targetCert.Port
// 			if exists {
// 				targetCert.ID = existing.ID
// 				targetCert.IsIgnored = existing.IsIgnored
// 				targetCert.Port = existing.Port

// 				// [繼承屬性] 繼承上次的檢查時間，避免掃描失敗時時間歸零
// 				// 但稍後 PerformNetworkScan 成功後會覆蓋它
// 				targetCert.LastCheckTime = existing.LastCheckTime
// 				scanPort = existing.Port
// 			} else {
// 				// [新域名] 立即寫入 Pending
// 				initialState := targetCert
// 				initialState.Status = "pending"
// 				initialState.LastCheckTime = time.Time{}

// 				// 快速 Upsert，讓列表立刻有資料
// 				if err := s.Repo.Upsert(ctx, initialState); err == nil {
// 					mu.Lock()
// 					stats.Added++
// 					stats.AddedNames = append(stats.AddedNames, fmt.Sprintf("🔹 %s", targetCert.DomainName))
// 					mu.Unlock()
// 					logrus.Debugf("🆕 [Pending] 新域名入庫等待掃描: %s", targetCert.DomainName)
// 				}
// 			}

// 			// =========================================================
// 			// [核心動作] 執行網路掃描 (SSL / DNS / HTTP)
// 			// 這一步會花費數秒鐘
// 			// =========================================================
// 			// sslResult := s.Scanner.PerformNetworkScan(ctx, targetCert.DomainName, scanPort)
// 			finalCert, _, err := s.Scanner.ScanOne(ctx, targetCert)
// 			if err != nil {
// 				logrus.Errorf("❌ [Scan Failed] %s: %v", targetCert.DomainName, err)
// 				// 即使掃描失敗，ScanOne 內部通常也會嘗試更新狀態為 Unresolvable
// 				return
// 			}
// 			// 將掃描結果合併回 targetCert 物件
// 			// s.mergeSSLResult(&targetCert, sslResult)

// 			// =========================================================
// 			// STEP 3: 寫入最終結果 (Active/Expired) & 發送通知
// 			// =========================================================

// 			// 寫入 DB (此時狀態從 Pending 變為 Active/Expired/Unresolvable)
// 			// if err := s.Repo.Upsert(ctx, targetCert); err != nil {
// 			// 	logrus.Errorf("❌ [DB Error] 更新掃描結果失敗 %s: %v", targetCert.DomainName, err)
// 			// }
// 			// =========================================================
// 			// [核心動作] 寫入資料庫 & 發送通知
// 			// =========================================================
// 			if exists {
// 				// --- 舊域名邏輯 (更新) ---

// 				// 1. 檢查 Cloudflare 設定是否變更
// 				changes := s.checkCFDiff(existing, finalCert)

// 				// 2. 檢查 SSL 是否續簽 (新到期日 > 舊到期日 + 24H)
// 				if targetCert.NotAfter.After(existing.NotAfter.Add(24 * time.Hour)) {
// 					renewDetails := fmt.Sprintf(
// 						"📅 <b>舊到期日</b>: %s\n"+
// 							"📅 <b>新到期日</b>: <code>%s</code>\n"+
// 							"⏳ <b>剩餘天數</b>: <b>%d 天</b>\n"+
// 							"🔒 <b>發行商</b>: %s",
// 						existing.NotAfter.Format("2006-01-02"),
// 						targetCert.NotAfter.Format("2006-01-02"),
// 						targetCert.DaysRemaining,
// 						targetCert.Issuer,
// 					)
// 					s.Notifier.NotifyOperation(ctx, EventRenew, targetCert.DomainName, renewDetails)
// 				}

// 				// 3. 寫入 DB
// 				// s.Repo.Upsert(ctx, targetCert)
// 				if err := s.Repo.UpdateCertInfo(ctx, targetCert); err != nil {
// 					logrus.Errorf("❌ [DB Error] 更新失敗 %s: %v", targetCert.DomainName, err)
// 				}
// 				// 4. 發送變更通知 (若有)
// 				if len(changes) > 0 {
// 					mu.Lock()
// 					stats.Updated++
// 					// Log 格式化
// 					detailMsg := fmt.Sprintf("🔹 <b>%s</b>\n   ↳ %s",
// 						targetCert.DomainName,
// 						strings.Join(changes, "\n   ↳ "))
// 					stats.UpdatedNames = append(stats.UpdatedNames, detailMsg)
// 					mu.Unlock()

// 					s.Notifier.NotifyOperation(ctx, EventUpdate, targetCert.DomainName, strings.Join(changes, "\n"))
// 				}

// 			} else {
// 				// --- 新域名邏輯 (新增) ---

// 				// 1. 寫入 DB (這會把之前的 Pending 狀態更新為 Active/Expired/Unresolvable)
// 				// s.Repo.Upsert(ctx, targetCert)
// 				// 對於新域名，使用 Upsert/Create 將完整狀態 (從 Pending 變為 Active) 寫入
// 				if err := s.Repo.Upsert(ctx, targetCert); err != nil {
// 					logrus.Errorf("❌ [DB Error] 新增失敗 %s: %v", targetCert.DomainName, err)
// 				}
// 				mu.Lock()
// 				stats.Added++
// 				stats.AddedNames = append(stats.AddedNames, fmt.Sprintf("🔹 %s", targetCert.DomainName))
// 				mu.Unlock()

// 				// 2. 發送新增通知 (如果不是屬於新發現的 Zone)
// 				if !newZones[targetCert.ZoneName] {
// 					statusProxy := "☁️ Proxy (橘雲)"
// 					if !targetCert.IsProxied {
// 						statusProxy = "🛡 DNS Only (灰雲)"
// 					}
// 					details := fmt.Sprintf(
// 						"🎯 <b>指向目標</b>: <code>%s</code>\n"+
// 							"🏷 <b>類型</b>: %s\n"+
// 							"⚡ <b>代理狀態</b>: %s\n"+
// 							"📅 <b>域名到期</b>: %s",
// 						targetCert.CFOriginValue,
// 						targetCert.CFRecordType,
// 						statusProxy,
// 						func() string {
// 							if targetCert.DomainExpiryDate.IsZero() {
// 								return "Unknown"
// 							}
// 							return targetCert.DomainExpiryDate.Format("2006-01-02")
// 						}(),
// 					)
// 					s.Notifier.NotifyOperation(ctx, EventAdd, targetCert.DomainName, details)
// 				}
// 			}

// 		}(cfD)
// 	}

// 	// 等待所有掃描任務完成
// 	wg.Wait()
// 	logrus.Infof("✅ [Pipeline] 所有資料處理完畢 (共 %d 筆)", atomic.LoadInt32(&processedCount))

// 	// =================================================================
// 	// 清理邏輯 (Cleanup)
// 	// =================================================================

// 	// 1. 清除過期的 Placeholder
// 	for _, dbRecord := range dbMap {
// 		if dbRecord.CFRecordType == "placeholder" {
// 			// 如果該 Zone 本次有真實資料，刪除佔位符
// 			if activeZonesWithRealData[dbRecord.ZoneName] {
// 				logrus.Infof("🧹 [Cleanup] 清除過期佔位符: %s", dbRecord.DomainName)
// 				if err := s.Repo.Delete(ctx, dbRecord.ID); err == nil {
// 					mu.Lock()
// 					stats.Deleted++
// 					stats.DeletedNames = append(stats.DeletedNames, fmt.Sprintf("佔位符清理: %s", dbRecord.DomainName))
// 					mu.Unlock()
// 				}
// 			}
// 		}
// 	}

// 	// 2. 建立新的 Zone Placeholder (針對完全沒有子域名的空 Zone)
// 	for zoneName, hasValid := range zoneHasValidRecords {
// 		if !hasValid {
// 			if _, exists := dbMap[zoneName]; !exists {
// 				logrus.Infof("🛡 [Zone Placeholder] 為空 Zone 建立佔位符: %s", zoneName)
// 				placeholder := domain.SSLCertificate{
// 					DomainName:       zoneName,
// 					ZoneName:         zoneName,
// 					Status:           "skipped_`zone",
// 					IsIgnored:        true,
// 					CFRecordType:     "placeholder",
// 					CFOriginValue:    "Auto Generated Placeholder",
// 					DomainExpiryDate: time.Time{},
// 				}
// 				if err := s.Repo.Create(ctx, placeholder); err != nil {
// 					logrus.Errorf("❌ 建立 Zone 佔位符失敗 %s: %v", zoneName, err)
// 				}
// 			}
// 		}
// 	}
// }

// processUpserts 處理新增與更新邏輯 (包含 SSL 掃描)
func (s *CronService) processUpserts(ctx context.Context, cfDomains []domain.SSLCertificate, dbMap map[string]domain.SSLCertificate, stats *SyncStats, newZones map[string]bool) {
	// =================================================================
	// Phase 1: 快速寫入 (讓前端能看到 Pending 狀態)
	// =================================================================
	// logrus.Infof("⚡ [Sync] Phase 1: 快速寫入新域名...")
	// for _, cfD := range cfDomains {
	// 	if shouldSkipDomain(cfD.DomainName) {
	// 		continue
	// 	}

	// 	// 檢查是否已存在
	// 	if _, exists := dbMap[cfD.DomainName]; !exists {
	// 		// 如果是新域名，先寫入一個初始狀態
	// 		initialCert := cfD
	// 		initialCert.Status = "pending"          // 明確標記為等待中
	// 		initialCert.LastCheckTime = time.Time{} // 從未檢查過

	// 		// 寫入 DB
	// 		if err := s.Repo.Upsert(ctx, initialCert); err == nil {
	// 			// 更新 dbMap，這樣 Phase 2 就不會當作它不存在
	// 			// dbMap[cfD.DomainName] = initialCert

	// 			// 統計新增 (這裡先算，避免 Phase 2 重複算)
	// 			// 注意：原本的代碼是在掃描後才算 Added，改在這裡算會讓數據更即時
	// 			// 但為了配合您原本的通知邏輯，我們可以選擇不在此處發通知，留到掃描後再發
	// 		}
	// 	}
	// }
	// logrus.Infof("✅ [Sync] Phase 1 完成，所有域名已入庫 (Pending)")

	// =================================================================
	// Phase 2: 深度掃描與狀態更新 (Deep Scan & Refresh)
	// 目的：
	// 1. 激活新域名 (Pending -> Active)
	// 2. [您的需求] 重新掃描既有域名 (Refresh Existing Active/Expired)
	// =================================================================
	total := len(cfDomains)
	concurrency := 10
	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup
	var mu sync.Mutex

	// [新增] 原子計數器
	var processedCount int32 = 0
	// [新增] 用來追蹤本次同步中，哪些 Zone 確實擁有「有效且未被略過」的真實紀錄
	// 用途：用來清除舊的佔位符
	activeZonesWithRealData := make(map[string]bool)

	// [新增] 用來追蹤每個新 Zone 是否有「有效」的子域名被寫入
	// key: ZoneName, value: 是否有寫入至少一筆
	zoneHasValidRecords := make(map[string]bool)

	// 初始化：先把 newZones 放進去，預設為 false
	for z := range newZones {
		zoneHasValidRecords[z] = false
	}

	logrus.Infof("🚀 [Sync] 開始處理 %d 筆域名 (流水線模式: 寫入 -> 掃描)...", total)

	for _, cfD := range cfDomains {
		if shouldSkipDomain(cfD.DomainName) {
			mu.Lock()
			stats.Skipped++
			mu.Unlock()
			atomic.AddInt32(&processedCount, 1)
			continue
		}

		// [新增] 如果程式執行到這裡，代表這個域名沒有被略過
		// 我們標記它所屬的 Zone 為「有有效紀錄」
		if _, isNew := newZones[cfD.ZoneName]; isNew {
			mu.Lock()
			zoneHasValidRecords[cfD.ZoneName] = true
			mu.Unlock()
		}

		// [新增] 只要這個域名沒被略過，就標記該 Zone 擁有真實數據
		mu.Lock()
		activeZonesWithRealData[cfD.ZoneName] = true
		mu.Unlock()

		wg.Add(1)
		go func(targetCert domain.SSLCertificate) {
			// 確保 Semaphore 和 WaitGroup 正確釋放
			sem <- struct{}{}
			defer func() {
				<-sem
				wg.Done()
				// [關鍵新增] 任務結束後更新進度並打印 Log
				current := atomic.AddInt32(&processedCount, 1)
				// 每 50 筆，或者是最後一筆時，輸出 Log
				if current%50 == 0 || int(current) == total {
					percentage := float64(current) / float64(total) * 100
					// 這裡顯示當前處理完的域名，讓你知道程式還活著
					logrus.Infof("⏳ [Sync Progress] 已處理: %d/%d (%.1f%%) | 最新完成: %s",
						current, total, percentage, targetCert.DomainName)
				}
			}()

			// 合併舊資料屬性 (ID, Ignored, Port)
			existing, exists := dbMap[targetCert.DomainName]
			scanPort := targetCert.Port
			if exists {
				targetCert.ID = existing.ID
				targetCert.IsIgnored = existing.IsIgnored
				targetCert.Port = existing.Port
				scanPort = existing.Port
				// [新增] 繼承 Pending 狀態以外的屬性，避免覆蓋
			}

			// 執行即時掃描以獲取最新 SSL 狀態
			sslResult := s.Scanner.PerformNetworkScan(ctx, targetCert.DomainName, scanPort)
			s.mergeSSLResult(&targetCert, sslResult)

			// 寫入資料庫
			// if exists {
			if exists {
				changes := s.checkCFDiff(existing, targetCert)

				// 2. [新增] 檢查 SSL 續簽 (Renewal)
				// 如果 新到期日 > 舊到期日 + 1天
				if targetCert.NotAfter.After(existing.NotAfter.Add(24 * time.Hour)) {
					// 組裝續簽詳細內容
					renewDetails := fmt.Sprintf(
						"📅 <b>舊到期日</b>: %s\n"+
							"📅 <b>新到期日</b>: <code>%s</code>\n"+
							"⏳ <b>剩餘天數</b>: <b>%d 天</b>\n"+
							"🔒 <b>發行商</b>: %s",
						existing.NotAfter.Format("2006-01-02"),
						targetCert.NotAfter.Format("2006-01-02"),
						targetCert.DaysRemaining,
						targetCert.Issuer,
					)

					// 立即發送「SSL 續簽」專屬通知 (使用 EventRenew)
					s.Notifier.NotifyOperation(ctx, EventRenew, targetCert.DomainName, renewDetails)

					// (選擇性) 將此事件也記錄到 UpdatedNames 列表，讓匯總報告也看得到
					// changes = append(changes, "♻️ SSL 憑證已續簽")
				}
				s.Repo.Upsert(ctx, targetCert)
				if len(changes) > 0 {
					mu.Lock()
					stats.Updated++
					detailMsg := fmt.Sprintf("🔹 <b>%s</b>\n   ↳ %s",
						targetCert.DomainName,
						strings.Join(changes, "\n   ↳ ")) // 使用縮排符號
					stats.UpdatedNames = append(stats.UpdatedNames, detailMsg)
					mu.Unlock()
					// 觸發變更通知
					s.Notifier.NotifyOperation(ctx, EventUpdate, targetCert.DomainName, strings.Join(changes, "\n"))
				}
			} else {
				s.Repo.Upsert(ctx, targetCert)
				mu.Lock()
				stats.Added++
				stats.AddedNames = append(stats.AddedNames, fmt.Sprintf("🔹 %s", targetCert.DomainName))

				// stats.AddedNames = append(stats.AddedNames, targetCert.DomainName)
				mu.Unlock()

				// [關鍵修改] 判斷開關
				// 如果這個域名屬於剛剛發現的新 Zone，則跳過通知
				if newZones[targetCert.ZoneName] {
					// 這裡只記錄 Log，不發 Notifier
					logrus.Debugf("🔕 [Muted] 新增子域名 %s (因屬於新 Zone %s，略過通知)", targetCert.DomainName, targetCert.ZoneName)
					return
				}
				// 1. 準備漂亮的狀態顯示
				statusProxy := "☁️ Proxy (橘雲)"
				if !targetCert.IsProxied {
					statusProxy = "🛡 DNS Only (灰雲)"
				}
				// 2. 組裝詳細內容
				details := fmt.Sprintf(
					"🎯 <b>指向目標</b>: <code>%s</code>\n"+
						"🏷 <b>類型</b>: %s\n"+
						"⚡ <b>代理狀態</b>: %s\n"+
						"📅 <b>域名到期</b>: %s",
					targetCert.CFOriginValue,
					targetCert.CFRecordType,
					statusProxy,
					// 如果有 WHOIS 資料就顯示，沒有顯示 Unknown
					func() string {
						if targetCert.DomainExpiryDate.IsZero() {
							return "Unknown"
						}
						return targetCert.DomainExpiryDate.Format("2006-01-02")
					}(),
				)

				// 3. [關鍵] 立即發送「單獨」通知
				s.Notifier.NotifyOperation(ctx, EventAdd, targetCert.DomainName, details)
			}
		}(cfD)
	}
	wg.Wait()

	// =================================================================
	// [新增] 清理過期的佔位符 (Placeholder Cleanup)
	// =================================================================
	// 邏輯：如果一個 Zone 已經掃描到了真實的子域名 (activeZonesWithRealData 為 true)
	// 但資料庫裡還留著該 Zone 的 "placeholder" 紀錄，則該佔位符已完成歷史任務，應予以刪除。
	for _, dbRecord := range dbMap {
		if dbRecord.CFRecordType == "placeholder" {
			// 檢查這個佔位符所屬的 Zone，是否在本次同步中發現了真實域名
			if activeZonesWithRealData[dbRecord.ZoneName] {
				logrus.Infof("🧹 [Cleanup] 清除過期佔位符: %s (已偵測到真實子域名)", dbRecord.DomainName)

				// 從資料庫刪除
				if err := s.Repo.Delete(ctx, dbRecord.ID); err == nil {
					// 更新統計 (視需求而定，也可以不加)
					mu.Lock()
					stats.Deleted++
					stats.DeletedNames = append(stats.DeletedNames, fmt.Sprintf("佔位符清理: %s", dbRecord.DomainName))
					mu.Unlock()
				}
			}
		}
	}
	// =================================================================
	// [新增] 檢查是否需要建立「Zone 佔位符」
	// =================================================================
	// 如果一個新 Zone，跑完了所有子域名，卻沒有半個被寫入 (例如全都是 _ 開頭)，
	// 我們必須手動建立一筆該 Zone 的紀錄，否則下次 Sync 又會當作它是新的。
	for zoneName, hasValid := range zoneHasValidRecords {
		if !hasValid {
			// 檢查 DB 是否已經有這個主域名本身的紀錄 (避免重複建立)
			if _, exists := dbMap[zoneName]; !exists {
				logrus.Infof("🛡 [Zone Placeholder] 為全被過濾的 Zone 建立佔位符: %s", zoneName)

				// 建立一個佔位符物件
				placeholder := domain.SSLCertificate{
					DomainName:       zoneName, // 使用主域名作為名稱
					ZoneName:         zoneName,
					Status:           "skipped_zone", // 特殊狀態，或者用 unresolvable
					IsIgnored:        true,           // [關鍵] 預設忽略，避免掃描報錯
					CFRecordType:     "placeholder",  // 標記類型
					CFOriginValue:    "Auto Generated Placeholder",
					DomainExpiryDate: time.Time{}, // 空時間
				}

				// 寫入 DB
				if err := s.Repo.Create(ctx, placeholder); err != nil {
					logrus.Errorf("❌ 建立 Zone 佔位符失敗 %s: %v", zoneName, err)
				} else {
					// 算作新增，但不發通知 (因為 detectZoneChanges 已經發過 Zone 通知的)
					// 也可以選擇不計入 stats.Added，看您需求
					// stats.Added++
				}
			}
		}
	}
}

// processDeletions 處理刪除邏輯
func (s *CronService) processDeletions(ctx context.Context, cfDomains []domain.SSLCertificate, dbDomains []domain.SSLCertificate, stats *SyncStats) {
	cfMap := make(map[string]bool)
	// 2. [新增] 建立 Cloudflare 存在的「Zone (主域名)」Map
	activeZones := make(map[string]bool)

	for _, d := range cfDomains {
		cfMap[d.DomainName] = true
		if d.ZoneName != "" {
			activeZones[d.ZoneName] = true
		}
	}

	for _, dbD := range dbDomains {
		// =================================================================
		// [關鍵修正] 保護佔位符 (Placeholder) 不被誤刪
		// =================================================================
		// 如果這是一筆「佔位符」資料
		if dbD.CFRecordType == "placeholder" {
			// 檢查該 Zone 是否還存在於 Cloudflare
			if activeZones[dbD.ZoneName] {
				// 如果 Zone 還在，絕對不能刪除這個佔位符！直接跳過
				continue
			}
			// 如果 Zone 都不在了，那這個佔位符也可以刪了 (會往下執行刪除)
		}
		// =================================================================

		// 原本的刪除邏輯：如果 DB 有但 CF 沒有，且不是特殊排除域名
		if !cfMap[dbD.DomainName] && !shouldSkipDomain(dbD.DomainName) {

			// [額外保護] 再次確認不是 placeholder (雙重保險)
			if dbD.CFRecordType == "placeholder" && activeZones[dbD.ZoneName] {
				continue
			}

			if err := s.Repo.Delete(ctx, dbD.ID); err == nil {
				stats.Deleted++
				stats.DeletedNames = append(stats.DeletedNames, dbD.DomainName)

				// =========================================================
				// [新增] 立即發送單獨的刪除通知
				// =========================================================
				details := fmt.Sprintf(
					"來源: Cloudflare Sync\n" +
						"說明: 該域名已從 Cloudflare 移除，系統已同步刪除。",
				)
				s.Notifier.NotifyOperation(ctx, EventDelete, dbD.DomainName, details)
			}
		}
	}
}

// mergeSSLResult 將掃描結果合併到目標物件
func (s *CronService) mergeSSLResult(target *domain.SSLCertificate, result domain.SSLCertificate) {
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

// PerformScan 執行掃描排程
func (s *CronService) PerformScan(ctx context.Context) {
	start := time.Now()
	logrus.Info("🚀 [Cron] 開始執行 SSL 掃描任務...")

	// 呼叫 Scanner Service 執行
	if err := s.Scanner.ScanAll(ctx); err != nil {
		logrus.Errorf("❌ [Cron] 掃描任務失敗: %v", err)
		return
	}

	duration := time.Since(start).String()

	// 發送完成統計通知
	stats, _ := s.Repo.GetStatistics(ctx)
	if stats != nil {
		s.Notifier.NotifyTaskFinish(ctx, EventScanFinish, TaskSummaryData{
			Total:    int(stats.TotalDomains),
			Active:   stats.StatusCounts["active"],
			Expired:  stats.StatusCounts["expired"],
			Warning:  stats.StatusCounts["warning"],
			Duration: duration,
		})
	}
}

// notifySyncResult 發送同步結果通知
func (s *CronService) notifySyncResult(stats SyncStats) {
	ctx := context.Background()
	var detailsBuilder strings.Builder

	// if len(stats.AddedNames) > 0 {
	// 	detailsBuilder.WriteString(fmt.Sprintf("\n\n✅ 新增 (%d):\n- %s", len(stats.AddedNames), formatList(stats.AddedNames, 10)))
	// }
	// if len(stats.DeletedNames) > 0 {
	// 	detailsBuilder.WriteString(fmt.Sprintf("\n\n🗑 刪除 (%d):\n- %s", len(stats.DeletedNames), formatList(stats.DeletedNames, 10)))
	// }
	// if len(stats.UpdatedNames) > 0 {
	// 	detailsBuilder.WriteString(fmt.Sprintf("\n\n🛠 更新 (%d):\n- %s", len(stats.UpdatedNames), formatList(stats.UpdatedNames, 5)))
	// }

	s.Notifier.NotifyTaskFinish(ctx, EventSyncFinish, TaskSummaryData{
		Added:    stats.Added,
		Updated:  stats.Updated,
		Deleted:  stats.Deleted,
		Skipped:  stats.Skipped,
		Duration: stats.Duration,
		Details:  detailsBuilder.String(),
	})

	// --- 2. 發送「新增」詳情 (如果有) ---
	// if len(stats.AddedNames) > 0 {
	// 	s.sendBatchDetails(ctx, "✅ 新增域名列表", stats.AddedNames)
	// }

	// --- 3. 發送「刪除」詳情 (如果有) ---
	if len(stats.DeletedNames) > 0 {
		// 刪除列表可能只是字串，稍微格式化一下
		var formattedDeleted []string
		for _, name := range stats.DeletedNames {
			formattedDeleted = append(formattedDeleted, fmt.Sprintf("🔸 %s", name))
		}
		s.sendBatchDetails(ctx, "🗑 刪除域名列表", formattedDeleted)
	}

	// --- 4. 發送「更新」詳情 (如果有) ---
	if len(stats.UpdatedNames) > 0 {
		s.sendBatchDetails(ctx, "🛠 變更詳情列表", stats.UpdatedNames)
	}
}

// =============================================================================
// Helper Functions
// =============================================================================

// shouldSkipDomain 判斷是否略過該域名 (如 _domainkey, SPF 紀錄等)
func shouldSkipDomain(name string) bool {
	if strings.Contains(name, "_domainkey") {
		return true
	}
	parts := strings.Split(name, ".")
	if len(parts) > 0 {
		if strings.HasPrefix(parts[0], "_") {
			return true
		}
		if strings.HasSuffix(parts[0], "pri") { // 常見的私有紀錄後綴
			return true
		}
	}
	return false
}

// formatList 格式化列表輸出，超過限制顯示 "..."
func formatList(names []string, limit int) string {
	if len(names) == 0 {
		return ""
	}
	if len(names) <= limit {
		return strings.Join(names, "\n- ")
	}
	remaining := len(names) - limit
	return strings.Join(names[:limit], "\n- ") + fmt.Sprintf("\n...及其他 %d 個", remaining)
}

// checkCFDiff 比對 Cloudflare 設定差異
func (s *CronService) checkCFDiff(old, new domain.SSLCertificate) []string {
	var changes []string

	if old.CFOriginValue != new.CFOriginValue {
		change := fmt.Sprintf("🎯 <b>指向目標 (Content)</b>:\n      🔴 <code>%s</code>\n      🟢 <code>%s</code>",
			old.CFOriginValue, new.CFOriginValue)
		changes = append(changes, change)
	}

	// 2. 比對紀錄類型
	if old.CFRecordType != new.CFRecordType {
		change := fmt.Sprintf("🏷 <b>類型 (Type)</b>: %s ➔ %s", old.CFRecordType, new.CFRecordType)
		changes = append(changes, change)
	}

	// 3. 比對 Proxy 狀態
	if old.IsProxied != new.IsProxied {
		statusOld := "☁️ Proxy (橘雲)"
		if !old.IsProxied {
			statusOld = "🛡 DNS Only (灰雲)"
		}

		statusNew := "☁️ Proxy (橘雲)"
		if !new.IsProxied {
			statusNew = "🛡 DNS Only (灰雲)"
		}

		change := fmt.Sprintf("⚡ <b>代理狀態</b>:\n      %s\n      ⬇️\n      %s", statusOld, statusNew)
		changes = append(changes, change)
	}

	return changes
}

func (s *CronService) sendBatchDetails(ctx context.Context, title string, items []string) {
	const batchSize = 20 // 每則訊息最多顯示 20 筆，避免 Telegram/Slack 限制

	for i := 0; i < len(items); i += batchSize {
		end := i + batchSize
		if end > len(items) {
			end = len(items)
		}

		chunk := items[i:end]

		// 組合訊息內容
		details := strings.Join(chunk, "\n")

		// 如果有分頁，標題加註 (1/3)
		currentTitle := title
		if len(items) > batchSize {
			page := (i / batchSize) + 1
			totalPages := (len(items) + batchSize - 1) / batchSize
			currentTitle = fmt.Sprintf("%s (%d/%d)", title, page, totalPages)
		}

		// 使用 NotifyOperation 發送
		// 注意：這裡借用 EventUpdate 類型，或者您可以新增 EventInfo 類型
		s.Notifier.NotifyOperation(ctx, EventUpdate, currentTitle, details)

		// 稍微停頓，避免順序錯亂
		time.Sleep(200 * time.Millisecond)
	}
}

func (s *CronService) detectZoneChanges(ctx context.Context, cfDomains []domain.SSLCertificate, dbDomains []domain.SSLCertificate) {
	// newZonesMap := make(map[string]bool) // 用來儲存新 Zone

	// 1. 提取 Cloudflare 目前所有的 Zone (New)
	cfZoneMap := make(map[string]bool)
	for _, d := range cfDomains {
		if d.ZoneName != "" {
			cfZoneMap[d.ZoneName] = true
		}
	}

	// 2. 提取 DB 目前所有的 Zone (Old)
	dbZoneMap := make(map[string]bool)
	for _, d := range dbDomains {
		if d.ZoneName != "" {
			dbZoneMap[d.ZoneName] = true
		}
	}

	// 3. 檢查新增的 Zone
	for zone := range cfZoneMap {
		if !dbZoneMap[zone] {
			// [關鍵] 標記為新 Zone
			// newZonesMap[zone] = true

			subCount := countSubdomains(cfDomains, zone)
			details := fmt.Sprintf(
				"來源: Cloudflare Sync\n"+
					"偵測到新的主域名已加入 Cloudflare，將自動納入監控。\n"+
					"包含子域名數量: %d 個\n"+
					"(為避免打擾，該主域名下的子域名新增通知已自動靜音 🔕)", subCount)

			s.Notifier.NotifyOperation(ctx, EventZoneAdd, zone, details)
			logrus.Infof("🌍 [Zone] 發現新主域名: %s (靜音子域名通知)", zone)
		}
	}

	// 4. 檢查移除的 Zone
	for zone := range dbZoneMap {
		if !cfZoneMap[zone] {
			details := fmt.Sprintf(
				"來源: Cloudflare Sync\n"+
					"該主域名已從 Cloudflare 移除，系統將自動清理相關子域名。\n"+
					"影響子域名數量: %d 個", countSubdomains(dbDomains, zone))

			s.Notifier.NotifyOperation(ctx, EventZoneDelete, zone, details)
			logrus.Infof("💥 [Zone] 主域名已移除: %s", zone)
		}
	}
	// return newZonesMap // 回傳新 Zone 列表
}

func countSubdomains(domains []domain.SSLCertificate, zoneName string) int {
	count := 0
	for _, d := range domains {
		if d.ZoneName == zoneName {
			count++
		}
	}
	return count
}
