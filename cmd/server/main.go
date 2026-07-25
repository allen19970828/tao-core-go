package main

import (
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/spf13/viper"
	"go.uber.org/zap"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"tao-core-go/internal/domain/models"
	"tao-core-go/internal/handler"
	"tao-core-go/internal/middleware"
	"tao-core-go/internal/service"
)

// main 是 tao-core-go 高效能測驗核心引擎的系統進入點。
// 負責執行：
// 1. 初始化 Zap 日誌與 Viper 系統配置（支援環境變數自動覆蓋）
// 2. 建立資料庫連線並執行 GORM Auto-Migrations 模型自動遷移
// 3. 初始化 EventBus 事件總線與所有業務服務 (Services)
// 4. 註冊 HTTP 控制器 (Handlers) 與中間件 (Middlewares)
// 5. 啟動 Gin HTTP 伺服器
func main() {
	// 1. 初始化 Zap 高效能結構化日誌記錄器
	logger, err := zap.NewDevelopment()
	if err != nil {
		log.Fatalf("無法初始化 Zap 日誌記錄器: %v", err)
	}
	defer logger.Sync()

	logger.Info("正在啟動 TAO Core Go 現代化測驗核心引擎...")

	// 2. 初始化 Viper 系統配置載入器
	viper.SetConfigName("config")
	viper.SetConfigType("yaml")
	viper.AddConfigPath("./config")
	viper.AddConfigPath("../config")
	viper.SetDefault("server.port", 8080)
	viper.SetDefault("database.dsn", "tao_core.db")
	viper.SetDefault("jwt.secret", "tao-core-go-default-secret")

	// 支援 Docker / 雲端環境變數自動覆蓋 (例如: DATABASE_DRIVER=postgres)
	viper.AutomaticEnv()
	viper.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))

	if err := viper.ReadInConfig(); err != nil {
		logger.Warn("未找到預設配置檔案，將使用預設參數運作", zap.Error(err))
	}

	// 3. 初始化 GORM 資料庫連線與資料表自動遷移 (Auto-Migrations)
	dsn := viper.GetString("database.dsn")
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		logger.Fatal("資料庫連線失敗", zap.Error(err))
	}

	logger.Info("正在執行資料庫 Auto-Migrations 自動遷移...")
	err = db.AutoMigrate(
		&models.User{},
		&models.Role{},
		&models.Permission{},
		&models.UserRole{},
		&models.RolePermission{},
		&models.Item{},
		&models.TestSection{},
		&models.TestItem{},
		&models.Test{},
		&models.Delivery{},
		&models.TestSession{},
		&models.ItemResponse{},
		&models.WebhookConfig{},
		&models.WebhookLog{},
		&models.LTIPlatform{},
		&models.LTILinkSession{},
		&models.ProctorEvent{},
		&models.Group{},
		&models.UserGroup{},
		&models.DeliveryGroup{},
	)
	if err != nil {
		logger.Fatal("資料庫 Migration 遷移失敗", zap.Error(err))
	}

	// 若資料庫為空，自動寫入測試示範資料 (Demo Data)
	seedDemoData(db, logger)

	// 4. 初始化解耦事件總線 (EventBus) 與核心業務服務 (Services)
	eventBus := service.NewEventBus(logger)
	scoringSvc := service.NewScoringService()
	webhookSvc := service.NewWebhookService(db, logger, viper.GetInt("webhook.worker_pool_size"))
	sessionSvc := service.NewSessionService(db, scoringSvc, webhookSvc)
	qtiSvc := service.NewQTIService(db, scoringSvc)
	ltiSvc := service.NewLTIService(db, logger, sessionSvc)
	proctorSvc := service.NewProctorService(db)
	exportSvc := service.NewResultsExportService(db, proctorSvc)

	// 注入 SessionService 的依賴組件 (EventBus 與 LTIService)
	if impl, ok := sessionSvc.(interface {
		SetLTIService(service.LTIService)
		SetEventBus(service.EventBus)
	}); ok {
		impl.SetLTIService(ltiSvc)
		impl.SetEventBus(eventBus)
	}

	// 5. 初始化 HTTP 控制器 (Handlers)
	sessionHandler := handler.NewSessionHandler(sessionSvc, webhookSvc)
	qtiHandler := handler.NewQTIHandler(qtiSvc, "./uploads/media")
	ltiHandler := handler.NewLTIHandler(ltiSvc)
	proctorHandler := handler.NewProctorHandler(proctorSvc)
	resultsHandler := handler.NewResultsHandler(exportSvc)

	// 6. 設定 Gin Router 路由與中間件 (Middlewares)
	mode := viper.GetString("server.mode")
	if mode == "release" {
		gin.SetMode(gin.ReleaseMode)
	}

	r := gin.Default()

	// 全域載入 Prometheus 效能與流量收集中間件
	r.Use(middleware.MetricsCollector())

	// 初始化 API 令牌桶防刷限流器 (限制 10 req/s)
	rateLimiter := middleware.NewRateLimiter(10, time.Second)

	// 掛載 QTI 題目提取出的靜態多媒體檔案目錄 (/uploads)
	r.Static("/uploads", "./uploads")

	// 根目錄歡迎頁面 (HTML Web UI 儀表板)
	r.GET("/", func(c *gin.Context) {
		html := `<!DOCTYPE html>
<html>
<head>
    <title>TAO Core Go Engine</title>
    <style>
        body { font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif; background: #0f172a; color: #f8fafc; padding: 40px; }
        .card { background: #1e293b; padding: 30px; border-radius: 12px; max-width: 750px; margin: 0 auto; box-shadow: 0 10px 25px rgba(0,0,0,0.3); }
        h1 { color: #38bdf8; margin-top: 0; }
        .badge { background: #22c55e; color: #052e16; padding: 4px 10px; border-radius: 9999px; font-size: 14px; font-weight: bold; }
        ul { background: #0f172a; padding: 20px; border-radius: 8px; font-family: monospace; }
        li { margin-bottom: 8px; }
        a { color: #38bdf8; text-decoration: none; }
        a:hover { text-decoration: underline; }
    </style>
</head>
<body>
    <div class="card">
        <h1>🚀 TAO Core Go Engine <span class="badge">ONLINE</span></h1>
        <p>歡迎使用基於 Go 語言重構的高效能電腦化測驗核心引擎。</p>
        <h3>可用 API 列表：</h3>
        <ul>
            <li>GET  <a href="/health">/health</a> - 系統健康檢查</li>
            <li>GET  <a href="/metrics">/metrics</a> - Prometheus 效能與流量監控端點</li>
            <li>POST /api/v1/sessions/start - 開始測驗會話</li>
            <li>GET  /api/v1/sessions/:id - 查詢測驗狀態</li>
            <li>POST /api/v1/sessions/:id/response - 暫存題目答案 (含 Rate Limit 限流)</li>
            <li>POST /api/v1/sessions/:id/submit - 終止交卷與自動計分</li>
            <li>GET  /api/v1/deliveries/:id/results/csv - 匯出成績與 Raw Data CSV</li>
            <li>POST /api/v1/sessions/:id/proctor/event - 記錄切頁/黑屏監考事件</li>
            <li>GET  /api/v1/sessions/:id/proctor/analytics - 取得監考風險分析報告</li>
            <li>POST /api/v1/items/import-qti - 匯入 QTI 3.0 .zip 試題包</li>
            <li>POST /api/v1/lti/platforms - 註冊 LTI 1.3 平台 (Moodle/Canvas)</li>
            <li>GET/POST /api/v1/lti/login - LTI 1.3 OIDC SSO 登入發起</li>
            <li>POST /api/v1/lti/launch - LTI 1.3 測驗開啟入口</li>
            <li>POST /api/v1/webhooks/configs - 註冊異步 Webhook</li>
        </ul>
        <p><small>演示 Delivery ID: <code>delivery-demo-01</code></small></p>
    </div>
</body>
</html>`
		c.Header("Content-Type", "text/html; charset=utf-8")
		c.String(200, html)
	})

	// 系統健康檢查端點
	r.GET("/health", func(c *gin.Context) {
		c.JSON(200, gin.H{"status": "UP", "engine": "tao-core-go", "timestamp": time.Now()})
	})

	// Prometheus 效能與流量監控端點
	r.GET("/metrics", func(c *gin.Context) {
		c.JSON(200, middleware.GetMetricsSummary())
	})

	// API v1 路由群組
	api := r.Group("/api/v1")
	{
		// 測驗會話 (TestSession) 路由
		sessions := api.Group("/sessions")
		{
			sessions.POST("/start", sessionHandler.StartSession)
			sessions.GET("/:id", sessionHandler.GetSession)
			sessions.POST("/:id/response", rateLimiter.Middleware(), sessionHandler.SaveResponse)
			sessions.POST("/:id/submit", sessionHandler.SubmitSession)

			// 監考防作弊與切頁數據分析路由
			sessions.POST("/:id/proctor/event", proctorHandler.RecordEvent)
			sessions.GET("/:id/proctor/log", proctorHandler.GetProctorLog)
			sessions.GET("/:id/proctor/analytics", proctorHandler.GetProctorAnalytics)
		}

		// 測驗發布與成績 CSV 匯出路由
		deliveries := api.Group("/deliveries")
		{
			deliveries.GET("/:id/results/csv", resultsHandler.ExportResultsCSV)
		}

		// QTI 3.0 試題包匯入路由
		items := api.Group("/items")
		{
			items.POST("/import-qti", qtiHandler.ImportQTIPackage)
		}

		// LTI 1.3 Advantage 跨平台單點登入與成績回寫路由
		lti := api.Group("/lti")
		{
			lti.POST("/platforms", ltiHandler.RegisterPlatform)
			lti.GET("/login", ltiHandler.InitiateLogin)
			lti.POST("/login", ltiHandler.InitiateLogin)
			lti.POST("/launch", ltiHandler.HandleLaunch)
		}

		// Webhook 訂冊與日誌查詢路由
		webhooks := api.Group("/webhooks")
		{
			webhooks.POST("/configs", sessionHandler.RegisterWebhook)
			webhooks.GET("/logs", sessionHandler.GetWebhookLogs)
		}
	}

	// 7. 啟動 HTTP 伺服器
	port := viper.GetInt("server.port")
	addr := fmt.Sprintf(":%d", port)
	logger.Info("TAO Core Go 核心引擎已準備就緒！", zap.String("listen_address", addr))
	if err := r.Run(addr); err != nil {
		logger.Fatal("伺服器啟動失敗", zap.Error(err))
	}
}

// seedDemoData 在資料庫無資料時，自動寫入一組測試用試卷、題目與試驗發布。
func seedDemoData(db *gorm.DB, logger *zap.Logger) {
	var count int64
	db.Model(&models.Delivery{}).Count(&count)
	if count > 0 {
		return
	}

	logger.Info("正在初始化示範資料 (Demo Data)...")

	// 1. 建立測試示範題目 1 (單選題)
	item1 := models.Item{
		ID:            "item-demo-01",
		Title:         "台灣的首都是哪裡？",
		Prompt:        "請選出正確的城市：",
		ItemType:      models.ItemTypeSingleChoice,
		CorrectAnswer: "A",
		MaxScore:      10.0,
	}
	opts1 := []models.Option{
		{Identifier: "A", Text: "台北市"},
		{Identifier: "B", Text: "高雄市"},
		{Identifier: "C", Text: "台中市"},
		{Identifier: "D", Text: "台南市"},
	}
	optsJson1, _ := json.Marshal(opts1)
	item1.OptionsJSON = string(optsJson1)
	db.Create(&item1)

	// 2. 建立測試示範題目 2 (多選題)
	item2 := models.Item{
		ID:            "item-demo-02",
		Title:         "以下哪些程式語言支援高併發與強型別？",
		Prompt:        "請選出所有正確選項：",
		ItemType:      models.ItemTypeMultipleChoice,
		CorrectAnswer: "A,C",
		MaxScore:      10.0,
	}
	opts2 := []models.Option{
		{Identifier: "A", Text: "Go (Golang)"},
		{Identifier: "B", Text: "PHP 5.3"},
		{Identifier: "C", Text: "Rust"},
		{Identifier: "D", Text: "Bash"},
	}
	optsJson2, _ := json.Marshal(opts2)
	item2.OptionsJSON = string(optsJson2)
	db.Create(&item2)

	// 3. 建立測驗卷與結構
	testID := uuid.New().String()
	test := models.Test{
		ID:               testID,
		Title:            "TAO Core Go 核心能力演示測驗卷",
		QTIVersion:       "3.0",
		TimeLimitSeconds: 1800,
	}
	db.Create(&test)

	section := models.TestSection{
		ID:          uuid.New().String(),
		TestID:      testID,
		Title:       "第一大題：基礎能力測試",
		OrderIndex:  1,
		SectionType: "MAIN",
	}
	db.Create(&section)

	ti1 := models.TestItem{ID: uuid.New().String(), SectionID: section.ID, ItemID: item1.ID, OrderIndex: 1, Weight: 1.0}
	ti2 := models.TestItem{ID: uuid.New().String(), SectionID: section.ID, ItemID: item2.ID, OrderIndex: 2, Weight: 1.0}
	db.Create(&ti1)
	db.Create(&ti2)

	// 4. 建立試驗發布 (Delivery)
	delivery := models.Delivery{
		ID:         "delivery-demo-01",
		TestID:     testID,
		Title:      "2026 全國模擬考演示場次",
		IsActive:   true,
		MaxAttempts: 1,
	}
	db.Create(&delivery)

	logger.Info("示範資料初始化完成！Delivery ID: delivery-demo-01")
}
