package api

import (
	"cert-manager/internal/domain"
	"cert-manager/internal/repository"
	"cert-manager/internal/service"
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type DomainHandler struct {
	Repo      repository.DomainRepository
	CFService *service.CloudflareService
	Scanner   *service.ScannerService
	Notifier  *service.NotifierService
	Cron      *service.CronService
}

// [新增] 定義請求結構
type UpdateSettingsRequest struct {
	IsIgnored *bool `json:"is_ignored"`
	Port      *int  `json:"port"`
}

func NewDomainHandler(r repository.DomainRepository, c *service.CloudflareService, s *service.ScannerService, n *service.NotifierService, cron *service.CronService) *DomainHandler {
	return &DomainHandler{Repo: r, CFService: c, Scanner: s, Notifier: n, Cron: cron}
}

// =============================================================================
// Query APIs (讀取類)
// =============================================================================

// GetDomains 獲取域名列表 (支援分頁與多種篩選)
func (h *DomainHandler) GetDomains(c *gin.Context) {
	page, _ := strconv.ParseInt(c.DefaultQuery("page", "1"), 10, 64)
	limit, _ := strconv.ParseInt(c.DefaultQuery("pageSize", "10"), 10, 64)
	sortBy := c.Query("sortBy")
	search := c.Query("search")
	status := c.Query("status")
	proxied := c.Query("proxied")
	ignored := c.Query("ignored")
	zone := c.Query("zone")

	logrus.Infof("🔍 List Query: page=%d, pageSize=%d, search=%s, status=%s", page, limit, search, status)

	domains, total, err := h.Repo.List(c.Request.Context(), page, limit, sortBy, search, status, proxied, ignored, zone)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"data":  domains,
		"total": total,
		"page":  page,
		"limit": limit,
	})
}

// GetZones 獲取所有主域名清單
func (h *DomainHandler) GetZones(c *gin.Context) {
	zones, err := h.Repo.GetUniqueZones(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": zones})
}

// GetStatistics 獲取儀表板數據
func (h *DomainHandler) GetStatistics(c *gin.Context) {
	stats, err := h.Repo.GetStatistics(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": stats})
}

// GetSettings 獲取系統設定
func (h *DomainHandler) GetSettings(c *gin.Context) {
	settings, err := h.Repo.GetSettings(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": settings})
}

// =============================================================================
// Command APIs (操作類)
// =============================================================================

// SyncDomains 手動觸發 Cloudflare 同步
func (h *DomainHandler) SyncDomains(c *gin.Context) {
	// 1. 立即回應
	c.JSON(200, gin.H{"message": "Cloudflare 同步任務已在背景啟動"})

	// 2. 背景執行
	go func() {
		ctx := context.Background()
		logrus.Info("🚀 [Sync] 開始執行手動同步...")

		stats, err := h.Cron.PerformSync(ctx)
		if err != nil {
			logrus.Errorf("❌ [Sync] 同步失敗: %v", err)
			return
		}

		// 建構通知訊息
		var detailsBuilder strings.Builder
		if len(stats.AddedNames) > 0 {
			detailsBuilder.WriteString(fmt.Sprintf("\n\n✅ 新增 (%d):\n- %s", len(stats.AddedNames), strings.Join(limitList(stats.AddedNames, 10), "\n- ")))
		}
		if len(stats.UpdatedNames) > 0 {
			detailsBuilder.WriteString(fmt.Sprintf("\n\n🛠 更新 (%d):\n- %s", len(stats.UpdatedNames), strings.Join(limitList(stats.UpdatedNames, 10), "\n- ")))
		}
		if len(stats.DeletedNames) > 0 {
			detailsBuilder.WriteString(fmt.Sprintf("\n\n🗑 刪除 (%d):\n- %s", len(stats.DeletedNames), strings.Join(limitList(stats.DeletedNames, 10), "\n- ")))
		}

		h.Notifier.NotifyTaskFinish(ctx, service.EventSyncFinish, service.TaskSummaryData{
			Added:    stats.Added,
			Updated:  stats.Updated,
			Deleted:  stats.Deleted,
			Skipped:  stats.Skipped,
			Duration: stats.Duration,
			Details:  detailsBuilder.String(),
		})
		logrus.Infof("🏁 [Sync] 手動同步完成 (耗時: %s)", stats.Duration)
	}()
}

// ScanDomains 手動觸發全量 SSL 掃描
func (h *DomainHandler) ScanDomains(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"message": "掃描任務已在背景啟動"})

	go func() {
		ctx := context.Background()
		logrus.Info("🚀 [Scan] 開始執行手動全量掃描...")

		// ScanAll 內部已有 Log，但如果您想看更詳細的，ScanAll 的實現已包含 atomic counter
		if err := h.Scanner.ScanAll(ctx); err != nil {
			logrus.Errorf("❌ [Scan] 背景掃描失敗: %v", err)
			return
		}

		// 掃描完成後，獲取統計並通知
		stats, err := h.Repo.GetStatistics(ctx)
		if err == nil && stats != nil {
			// 注意：h.Scanner.ScanAll 內部可能已經發送過一次通知了，
			// 如果 CronService 有做這件事，這裡可能會重複。
			// 建議：如果是手動觸發，且 ScanAll 內部有發通知，這裡可以省略。
			// 或者，讓 ScanAll 只做掃描，通知由呼叫方決定。
			// 在目前的架構下，ScanAll 已經發送了 NotifyTaskFinish，所以這裡其實不需要再發一次。
			logrus.Info("🏁 [Scan] 手動全量掃描完成")
		}
	}()
}

// ScanOneDomain 單一域名掃描
func (h *DomainHandler) ScanOneDomain(c *gin.Context) {
	idStr := c.Param("id")
	oid, err := primitive.ObjectIDFromHex(idStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "無效的 ID 格式"})
		return
	}

	d, err := h.Repo.GetByID(c.Request.Context(), oid)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "找不到該域名"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": fmt.Sprintf("已啟動對 %s 的掃描", d.DomainName)})

	// 背景執行並詳細 Log
	go func() {
		ctx := context.Background()
		logrus.Infof("🔍 [ScanOne] 開始掃描: %s", d.DomainName)

		newCert, changes, err := h.Scanner.ScanOne(ctx, *d, true)
		if err != nil {
			logrus.Errorf("❌ [ScanOne] 失敗 %s: %v", d.DomainName, err)
			return
		}

		if len(changes) > 0 {
			logrus.Warnf("⚠️ [ScanOne] %s 發現 %d 項變更:", d.DomainName, len(changes))
			for _, change := range changes {
				logrus.Warnf("   -> %s", change)
			}
		} else {
			logrus.Infof("✅ [ScanOne] 完成 %s (無變更, Status: %s)", d.DomainName, newCert.Status)
		}
	}()
}

// BatchScanDomains 批量掃描
func (h *DomainHandler) BatchScanDomains(c *gin.Context) {
	var req struct {
		IDs []string `json:"ids"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "無效的請求格式"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": fmt.Sprintf("已觸發 %d 個域名的批量掃描", len(req.IDs))})

	go func() {
		// 設定批量操作的總體超時 (10分鐘)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()

		start := time.Now()
		total := len(req.IDs)
		logrus.Infof("🚀 [BatchScan] 開始批量掃描 %d 個域名...", total)

		// 併發控制
		concurrency := 5
		sem := make(chan struct{}, concurrency)
		var wg sync.WaitGroup
		var successCount, failCount int64

		for i, idStr := range req.IDs {
			if ctx.Err() != nil {
				logrus.Warn("⚠️ [BatchScan] 任務超時中斷")
				break
			}

			oid, err := primitive.ObjectIDFromHex(idStr)
			if err != nil {
				atomic.AddInt64(&failCount, 1)
				continue
			}

			d, err := h.Repo.GetByID(ctx, oid)
			if err != nil {
				atomic.AddInt64(&failCount, 1)
				continue
			}

			wg.Add(1)
			sem <- struct{}{}

			go func(idx int, target domain.SSLCertificate) {
				defer wg.Done()
				defer func() { <-sem }()

				// 呼叫 ScanOne
				newCert, changes, err := h.Scanner.ScanOne(ctx, target, true)

				// Log 輸出 (包含進度)
				progress := fmt.Sprintf("[%d/%d]", idx+1, total)

				if err != nil {
					atomic.AddInt64(&failCount, 1)
					logrus.Errorf("%s ❌ %s: %v", progress, target.DomainName, err)
				} else {
					atomic.AddInt64(&successCount, 1)
					if len(changes) > 0 {
						logrus.Warnf("%s ⚠️ %s (變更: %d)", progress, target.DomainName, len(changes))
					} else {
						// 正常完成的可以只印 Debug 或是 Info
						logrus.Infof("%s ✅ %s (%s)", progress, target.DomainName, newCert.Status)
					}
				}
			}(i, *d)
		}

		wg.Wait()
		duration := time.Since(start).String()
		logrus.Infof("🏁 [BatchScan] 結束。成功: %d, 失敗: %d, 耗時: %s", successCount, failCount, duration)

		// 發送批量完成通知
		h.Notifier.NotifyTaskFinish(context.Background(), service.EventScanFinish, service.TaskSummaryData{
			Total:    total,
			Active:   int(successCount),
			Expired:  int(failCount),
			Duration: duration,
			Details:  fmt.Sprintf("\n(手動批量 %d 筆)", total),
		})
	}()
}

// UpdateSettings 更新單一域名設定 (Port, Ignored)
func (h *DomainHandler) UpdateSettings(c *gin.Context) {
	idStr := c.Param("id")
	objID, err := primitive.ObjectIDFromHex(idStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "無效的 ID 格式"})
		return
	}

	var req UpdateSettingsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	currentDomain, err := h.Repo.GetByID(c.Request.Context(), objID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "找不到該域名"})
		return
	}

	newIgnored := currentDomain.IsIgnored
	if req.IsIgnored != nil {
		newIgnored = *req.IsIgnored
	}

	newPort := currentDomain.Port
	if req.Port != nil {
		newPort = *req.Port
	}

	err = h.Repo.UpdateSettings(c.Request.Context(), idStr, newIgnored, newPort)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "設定已更新", "port": newPort, "is_ignored": newIgnored})
}

// BatchUpdateSettings 批量更新設定
func (h *DomainHandler) BatchUpdateSettings(c *gin.Context) {
	var req struct {
		IDs       []string `json:"ids"`
		IsIgnored bool     `json:"is_ignored"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid JSON"})
		return
	}

	var objectIDs []primitive.ObjectID
	var targetNames []string

	for _, id := range req.IDs {
		if oid, err := primitive.ObjectIDFromHex(id); err == nil {
			objectIDs = append(objectIDs, oid)
			if d, err := h.Repo.GetByID(c.Request.Context(), oid); err == nil {
				targetNames = append(targetNames, d.DomainName)
			}
		}
	}

	if len(objectIDs) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "沒有有效的 ID"})
		return
	}

	if err := h.Repo.BatchUpdateSettings(c.Request.Context(), objectIDs, req.IsIgnored); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	actionType := "批量開啟監控"
	if req.IsIgnored {
		actionType = "批量忽略/停止監控"
	}
	displayList := limitList(targetNames, 15) // 最多顯示 15 個，超過顯示 "...及其他 x 個"

	details := fmt.Sprintf(
		"動作: %s\n影響數量: %d 個域名\n列表:\n- %s",
		actionType,
		len(objectIDs),
		strings.Join(displayList, "\n- "),
	)
	h.Notifier.NotifyOperation(c.Request.Context(), service.EventAdd, "Multiple Domains", details)

	c.JSON(http.StatusOK, gin.H{"message": "批量更新成功"})
}

// SaveSettings 儲存系統全域設定
func (h *DomainHandler) SaveSettings(c *gin.Context) {
	ctx := c.Request.Context()

	// 1. 先從資料庫取出「目前的設定」作為基底
	currentSettings, err := h.Repo.GetSettings(ctx)
	if err != nil {
		// 如果資料庫還沒有設定，則初始化一個空的
		currentSettings = &domain.NotificationSettings{}
	}

	// 2. 讀取前端傳來的原始 JSON 資料
	jsonData, err := io.ReadAll(c.Request.Body)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "讀取請求失敗"})
		return
	}

	// 3. 將前端的 JSON "Merge" 進 currentSettings
	// json.Unmarshal 會只更新 JSON 裡有的欄位，沒傳的欄位會保留 currentSettings 原本的值
	if err := json.Unmarshal(jsonData, currentSettings); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "無效的 JSON 格式"})
		return
	}

	// 4. 將合併後的完整設定寫回資料庫
	if err := h.Repo.SaveSettings(ctx, *currentSettings); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// var settings domain.NotificationSettings
	// if err := c.ShouldBindJSON(&settings); err != nil {
	// 	c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
	// 	return
	// }

	// if err := h.Repo.SaveSettings(c.Request.Context(), settings); err != nil {
	// 	c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
	// 	return
	// }

	// 通知 Cron 重載排程
	h.Cron.ReloadJobs()

	logrus.Infof("設定已更新 | Sync: %v | Telegram: %v", currentSettings.SyncEnabled, currentSettings.TelegramEnabled)
	
	c.JSON(200, gin.H{"message": "設定已儲存"})
}

// ExportDomains 匯出 CSV
func (h *DomainHandler) ExportDomains(c *gin.Context) {
	domains, _, err := h.Repo.List(c.Request.Context(), 1, 100000, "expiry_asc", "", "", "", "false", "")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.Header("Content-Type", "text/csv; charset=utf-8")
	c.Header("Content-Disposition", "attachment;filename=domains_report.csv")
	c.Writer.Write([]byte("\xEF\xBB\xBF")) // BOM

	writer := csv.NewWriter(c.Writer)
	writer.Write([]string{"Domain", "Issuer", "Expiry Date", "Days Left", "Status", "Proxy", "Zone"})
	for _, d := range domains {
		writer.Write([]string{
			d.DomainName,
			d.Issuer,
			d.NotAfter.Format("2006-01-02"),
			fmt.Sprintf("%d", d.DaysRemaining),
			string(d.Status),
			fmt.Sprintf("%v", d.IsProxied),
			d.ZoneName,
		})
	}
	writer.Flush()
}

// AddDomain 手動新增域名
func (h *DomainHandler) AddDomain(c *gin.Context) {
	var req domain.SSLCertificate
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid JSON"})
		return
	}

	if err := h.Repo.Create(c.Request.Context(), req); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	h.Notifier.NotifyOperation(c.Request.Context(), service.EventAdd, req.DomainName, fmt.Sprintf("手動新增 (IP: %s)", c.ClientIP()))
	c.JSON(http.StatusOK, gin.H{"message": "新增成功"})
}

// DeleteDomain 刪除域名
func (h *DomainHandler) DeleteDomain(c *gin.Context) {
	id := c.Param("id")
	oid, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid ID"})
		return
	}

	domainCert, _ := h.Repo.GetByID(c.Request.Context(), oid)
	domainName := "Unknown"
	if domainCert != nil {
		domainName = domainCert.DomainName
	}

	if err := h.Repo.Delete(c.Request.Context(), oid); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	h.Notifier.NotifyOperation(c.Request.Context(), service.EventDelete, domainName, fmt.Sprintf("手動刪除 (IP: %s)", c.ClientIP()))
	c.JSON(http.StatusOK, gin.H{"message": "刪除成功"})
}

// TestNotification 測試通知
func (h *DomainHandler) TestNotification(c *gin.Context) {
	var settings domain.NotificationSettings
	if err := c.ShouldBindJSON(&settings); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid JSON"})
		return
	}
	if err := h.Notifier.SendTestMessage(c.Request.Context(), settings); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "測試訊息發送成功"})
}

// 輔助函式
func limitList(names []string, limit int) []string {
	if len(names) <= limit {
		return names
	}
	result := names[:limit]
	remaining := len(names) - limit
	result = append(result, fmt.Sprintf("...及其他 %d 個", remaining))
	return result
}
