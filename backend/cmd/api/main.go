package main

import (
	"cert-manager/internal/api"
	"cert-manager/internal/conf"
	"cert-manager/internal/database"
	"cert-manager/internal/repository"
	"cert-manager/internal/service"
	"context"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"
)

func main() {

	// 設定 Log 格式與層級
	logrus.SetFormatter(&logrus.TextFormatter{
		FullTimestamp: true,
		ForceColors:   true,
	})

	// [關鍵] 設定為 InfoLevel 或 DebugLevel
	logrus.SetLevel(logrus.InfoLevel)

	// 1. Config
	cfg, err := conf.LoadConfig()
	if err != nil {
		logrus.Fatalf("Config error: %v", err)
	}

	// 2. Database
	mongoClient, err := database.Connect(cfg.MongoDB)
	if err != nil {
		logrus.Fatalf("Database error: %v", err)
	}
	defer mongoClient.Disconnect(context.Background())

	db := mongoClient.Database(cfg.MongoDB.Database)

	// 3. Dependency Injection (依賴注入)
	// Repo -> Service -> Handler
	domainRepo := repository.NewMongoDomainRepo(db)

	// 初始化基礎 Service (順序很重要)
	notifierService := service.NewNotifierService(domainRepo)
	cfService := service.NewCloudflareService(cfg.Cloudflare.APIToken, domainRepo) // Cloudflare 服務
	scannerService := service.NewScannerService(domainRepo, notifierService, cfService)

	// [關鍵修正 1] 這裡必須傳入 cfService，不能傳 nil！
	cronService := service.NewCronService(domainRepo, cfService, scannerService, notifierService)

	// [關鍵修正 2] 啟動 Cron 排程服務！
	cronService.Start()


	// 初始化 Handler
	domainHandler := api.NewDomainHandler(domainRepo, cfService, scannerService, notifierService, cronService)
	toolHandler := api.NewToolHandler()
	// [建議] 舊的 Scheduler 應該可以移除了，因為現在由 CronService 接管
	// scheduler := service.NewSchedulerService(scannerService, cfService)
	// scheduler.Start()
	// defer scheduler.Stop()

	// 4. Gin Router Setup
	r := gin.Default()

	// 設定 CORS
	r.Use(func(c *gin.Context) {
		c.Writer.Header().Set("Access-Control-Allow-Origin", "*")
		c.Writer.Header().Set("Access-Control-Allow-Methods", "POST, GET, OPTIONS, PUT, DELETE, PATCH")
		c.Writer.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(204)
			return
		}
		c.Next()
	})

	// 初始化 Auth Service
	authService := service.NewAuthService(mongoClient.Database(cfg.MongoDB.Database), "my-secret-key")
	authService.InitAdmin()

	// Login Handler
	r.POST("/api/login", func(c *gin.Context) {
		var req struct {
			Username string `json:"username"`
			Password string `json:"password"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "格式錯誤"})
			return
		}
		token, err := authService.Login(c.Request.Context(), req.Username, req.Password)
		if err != nil {
			c.JSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"token": token})
	})

	// API V1 Group (受保護)
	v1 := r.Group("/api/v1")
	v1.Use(api.AuthMiddleware("my-secret-key"))

	{
		v1.POST("/domains/sync", domainHandler.SyncDomains) // 觸發同步 (現在會正常運作了)
		v1.POST("/domains/scan", domainHandler.ScanDomains) // 觸發掃描
		v1.POST("/domains/:id/scan", domainHandler.ScanOneDomain)
		v1.POST("/domains/batch-scan", domainHandler.BatchScanDomains)
		v1.GET("/domains", domainHandler.GetDomains)                    // 列表查詢
		v1.PATCH("/domains/:id/settings", domainHandler.UpdateSettings) // 更新設定
		v1.GET("/zones", domainHandler.GetZones)                        // 獲取下拉選單資料
		v1.GET("/settings", domainHandler.GetSettings)                  // 獲取設定
		v1.POST("/settings", domainHandler.SaveSettings)                // 儲存設定
		v1.POST("/settings/test", domainHandler.TestNotification)       // 測試通知
		v1.GET("/stats", domainHandler.GetStatistics)                   // 獲取儀表板數據
		v1.POST("/domains/batch-settings", domainHandler.BatchUpdateSettings)
		v1.GET("/domains/export", domainHandler.ExportDomains)
		v1.POST("/domains", domainHandler.AddDomain)
		v1.DELETE("/domains/:id", domainHandler.DeleteDomain)
		v1.POST("/tools/decode-cert", toolHandler.DecodeCertificate)
	}

	// 5. Start Server
	logrus.Infof("Server starting on %s", cfg.Server.Port)
	if err := r.Run(cfg.Server.Port); err != nil {
		logrus.Fatalf("Server startup failed: %v", err)
	}
}
