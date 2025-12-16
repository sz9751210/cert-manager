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
	notifierService := service.NewNotifierService(domainRepo)
	cfService := service.NewCloudflareService(cfg.Cloudflare.APIToken)
	scannerService := service.NewScannerService(domainRepo, notifierService)
	acmeService := service.NewAcmeService(domainRepo, cfg.Cloudflare.APIToken)
	domainHandler := api.NewDomainHandler(domainRepo, cfService, scannerService, notifierService, acmeService)

	// 初始化 Scheduler
	scheduler := service.NewSchedulerService(scannerService, cfService)
	scheduler.Start()      // 啟動！
	defer scheduler.Stop() // 程式結束時關閉
	// 4. Gin Router Setup
	r := gin.Default()

	// 設定 CORS (因為前後分離，必須允許跨域)
	r.Use(func(c *gin.Context) {
		c.Writer.Header().Set("Access-Control-Allow-Origin", "*")
		c.Writer.Header().Set("Access-Control-Allow-Methods", "POST, GET, OPTIONS, PUT, DELETE,PATCH")
		c.Writer.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(204)
			return
		}
		c.Next()
	})

	// 初始化 Auth Service
	// 注意：這裡的 Secret "my-secret-key" 在生產環境應該從 config 讀取
	authService := service.NewAuthService(mongoClient.Database(cfg.MongoDB.Database), "my-secret-key")
	authService.InitAdmin() // 確保有預設帳號 (admin / admin123)

	// Login Handler (簡單寫在這裡或是移到 api package)
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
	v1.Use(api.AuthMiddleware("my-secret-key")) // <--- 掛上守門員

	{
		v1.POST("/domains/sync", domainHandler.SyncDomains)             // 觸發同步
		v1.POST("/domains/scan", domainHandler.ScanDomains)             // 觸發掃描
		v1.GET("/domains", domainHandler.GetDomains)                    // 列表查詢
		v1.PATCH("/domains/:id/settings", domainHandler.UpdateSettings) // 更新設定
		v1.GET("/zones", domainHandler.GetZones)                        // [新增] 獲取下拉選單資料
		// [新增] 設定路由
		v1.GET("/settings", domainHandler.GetSettings)                        // [新增] 獲取
		v1.POST("/settings", domainHandler.SaveSettings)                      // [新增] 儲存
		v1.POST("/settings/test", domainHandler.TestNotification)             // [新增] 測試通知
		v1.GET("/stats", domainHandler.GetStatistics)                         // [新增] 獲取儀表板數據
		v1.POST("/domains/batch-settings", domainHandler.BatchUpdateSettings) // [新增] 批量更新
		v1.GET("/domains/export", domainHandler.ExportDomains)                // [新增] 匯出
	}

	// 5. Start Server
	logrus.Infof("Server starting on %s", cfg.Server.Port)
	if err := r.Run(cfg.Server.Port); err != nil {
		logrus.Fatalf("Server startup failed: %v", err)
	}
}
