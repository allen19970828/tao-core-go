package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/spf13/viper"
	"go.uber.org/zap"
	"gorm.io/driver/postgres"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"tao-core-go/internal/domain/models"
	"tao-core-go/internal/handler"
	"tao-core-go/internal/middleware"
	"tao-core-go/internal/security"
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
	viper.SetDefault("database.max_open_connections", 50)
	viper.SetDefault("database.max_idle_connections", 10)
	viper.SetDefault("database.connection_max_lifetime_minutes", 30)
	viper.SetDefault("demo.seed_enabled", false)

	// 支援 Docker / 雲端環境變數自動覆蓋 (例如: DATABASE_DRIVER=postgres)
	viper.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	viper.AutomaticEnv()
	if err := viper.BindEnv("security.encryption_key", "APP_ENCRYPTION_KEY"); err != nil {
		logger.Fatal("無法綁定 APP_ENCRYPTION_KEY", zap.Error(err))
	}

	if err := viper.ReadInConfig(); err != nil {
		logger.Warn("未找到預設配置檔案，將使用預設參數運作", zap.Error(err))
	}

	jwtConfig := middleware.JWTConfig{
		Secret:   viper.GetString("jwt.secret"),
		Issuer:   viper.GetString("jwt.issuer"),
		Audience: viper.GetString("jwt.audience"),
	}
	if err := middleware.ValidateJWTConfig(jwtConfig); err != nil {
		logger.Fatal("JWT 認證設定無效，請透過 JWT_SECRET 提供高強度金鑰", zap.Error(err))
	}
	secretCipher, err := security.NewSecretCipher(viper.GetString("security.encryption_key"))
	if err != nil {
		logger.Fatal("秘密加密設定無效，請透過 APP_ENCRYPTION_KEY 提供 32-byte base64 金鑰", zap.Error(err))
	}

	// 3. 初始化 GORM 資料庫連線與資料表自動遷移 (Auto-Migrations)
	dsn := viper.GetString("database.dsn")
	databaseDriver := viper.GetString("database.driver")
	if err := validateDatabaseSecurity(viper.GetString("server.mode"), databaseDriver, dsn, viper.GetBool("database.allow_insecure_internal")); err != nil {
		logger.Fatal("資料庫 TLS 設定不安全", zap.Error(err))
	}
	db, err := openDatabase(databaseDriver, dsn)
	if err != nil {
		logger.Fatal("資料庫連線失敗", zap.Error(err))
	}
	sqlDB, err := db.DB()
	if err != nil {
		logger.Fatal("無法設定資料庫連線池", zap.Error(err))
	}
	defer sqlDB.Close()
	maxOpen := viper.GetInt("database.max_open_connections")
	maxIdle := viper.GetInt("database.max_idle_connections")
	if isSQLiteDriver(databaseDriver) {
		maxOpen, maxIdle = 1, 1
	}
	if maxOpen <= 0 || maxIdle < 0 || maxIdle > maxOpen {
		logger.Fatal("資料庫連線池設定無效")
	}
	sqlDB.SetMaxOpenConns(maxOpen)
	sqlDB.SetMaxIdleConns(maxIdle)
	sqlDB.SetConnMaxLifetime(time.Duration(viper.GetInt("database.connection_max_lifetime_minutes")) * time.Minute)
	pingCtx, pingCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer pingCancel()
	if err := sqlDB.PingContext(pingCtx); err != nil {
		logger.Fatal("資料庫健康檢查失敗", zap.Error(err))
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
		&models.LTIOIDCState{},
		&models.LTIResourceLink{},
		&models.ProctorEvent{},
		&models.Group{},
		&models.UserGroup{},
		&models.DeliveryGroup{},
	)
	if err != nil {
		logger.Fatal("資料庫 Migration 遷移失敗", zap.Error(err))
	}
	// 舊版只允許每個 issuer 一個 client；移除舊索引後改由 issuer + client_id 複合唯一索引管理。
	if db.Migrator().HasIndex(&models.LTIPlatform{}, "idx_lti_platforms_issuer") {
		if err := db.Migrator().DropIndex(&models.LTIPlatform{}, "idx_lti_platforms_issuer"); err != nil {
			logger.Fatal("無法移除舊版 LTI issuer 唯一索引", zap.Error(err))
		}
	}

	// Demo data 必須顯式啟用，避免空白的生產資料庫自動產生公開測驗。
	if viper.GetBool("demo.seed_enabled") {
		seedDemoData(db, logger)
	}

	// 4. 初始化解耦事件總線 (EventBus) 與核心業務服務 (Services)
	eventBus := service.NewEventBus(logger)
	scoringSvc := service.NewScoringService()
	webhookHosts := strings.FieldsFunc(viper.GetString("webhook.allowed_hosts"), func(r rune) bool { return r == ',' })
	webhookSvc, err := service.NewWebhookService(db, logger, viper.GetInt("webhook.worker_pool_size"), secretCipher, webhookHosts)
	if err != nil {
		logger.Fatal("Webhook 安全設定無效", zap.Error(err))
	}
	sessionSvc := service.NewSessionService(db, scoringSvc, webhookSvc)
	qtiSvc := service.NewQTIService(db, scoringSvc)
	ltiSvc := service.NewLTIService(db, logger, sessionSvc, secretCipher)
	proctorSvc := service.NewProctorService(db)
	exportSvc := service.NewResultsExportService(db, proctorSvc)

	// 注入事件與已完成驗證的 LTI AGS 回傳服務。
	if impl, ok := sessionSvc.(interface {
		SetEventBus(service.EventBus)
		SetLTIService(service.LTIService)
	}); ok {
		impl.SetEventBus(eventBus)
		impl.SetLTIService(ltiSvc)
	}

	// 5. 初始化 HTTP 控制器 (Handlers)
	sessionHandler := handler.NewSessionHandler(sessionSvc, webhookSvc)
	qtiHandler := handler.NewQTIHandler(qtiSvc, "./uploads/media")
	tokenTTL := time.Duration(viper.GetInt("jwt.expire_hours")) * time.Hour
	ltiHandler := handler.NewLTIHandler(ltiSvc, jwtConfig, tokenTTL)
	proctorHandler := handler.NewProctorHandler(proctorSvc)
	resultsHandler := handler.NewResultsHandler(exportSvc)

	// 6. 設定 Gin Router 路由與中間件 (Middlewares)
	mode := viper.GetString("server.mode")
	if mode == "release" {
		gin.SetMode(gin.ReleaseMode)
	}

	r := gin.Default()
	trustedProxies := strings.FieldsFunc(viper.GetString("server.trusted_proxies"), func(r rune) bool { return r == ',' })
	if err := r.SetTrustedProxies(trustedProxies); err != nil {
		logger.Fatal("trusted proxy 設定無效", zap.Error(err))
	}

	// 全域載入 Prometheus 效能與流量收集中間件
	r.Use(middleware.MetricsCollector())
	r.Use(middleware.SecurityHeaders())
	r.Use(middleware.JSONBodyLimit(1 << 20))

	// 初始化 API 令牌桶防刷限流器 (限制 10 req/s)
	rateLimiter := middleware.NewRateLimiter(10, time.Second)

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
            <li>GET  /metrics - Prometheus 效能與流量監控端點 (ADMIN)</li>
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
            <li>POST /api/v1/lti/launch - LTI 1.3 驗證與測驗開啟入口</li>
            <li>POST /api/v1/webhooks/configs - 註冊異步 Webhook</li>
        </ul>
        <p><small>健康檢查與 LTI 協定入口為公開端點；其餘 API 與媒體均需要有效的 Bearer JWT。</small></p>
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

	// 監控與 API 安全邊界集中於 registerProtectedRoutes。

	registerProtectedRoutes(r, jwtConfig, rateLimiter, apiRouteHandlers{
		session: sessionHandler,
		qti:     qtiHandler,
		lti:     ltiHandler,
		proctor: proctorHandler,
		results: resultsHandler,
	})

	// 7. 啟動 HTTP 伺服器
	port := viper.GetInt("server.port")
	addr := fmt.Sprintf(":%d", port)
	logger.Info("TAO Core Go 核心引擎已準備就緒！", zap.String("listen_address", addr))
	server := &http.Server{
		Addr:              addr,
		Handler:           r,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       60 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}
	serverErrors := make(chan error, 1)
	go func() { serverErrors <- server.ListenAndServe() }()
	signalCtx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	select {
	case err := <-serverErrors:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Fatal("伺服器啟動失敗", zap.Error(err))
		}
	case <-signalCtx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			logger.Error("伺服器無法優雅關閉", zap.Error(err))
		}
	}
}

func openDatabase(driver, dsn string) (*gorm.DB, error) {
	switch strings.ToLower(strings.TrimSpace(driver)) {
	case "postgres", "postgresql":
		return gorm.Open(postgres.Open(dsn), &gorm.Config{})
	case "sqlite", "sqlite3", "":
		return gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	default:
		return nil, fmt.Errorf("不支援的 database.driver: %s", driver)
	}
}

func isSQLiteDriver(driver string) bool {
	switch strings.ToLower(strings.TrimSpace(driver)) {
	case "", "sqlite", "sqlite3":
		return true
	default:
		return false
	}
}

func validateDatabaseSecurity(mode, driver, dsn string, allowInsecureInternal bool) error {
	if strings.EqualFold(strings.TrimSpace(mode), "release") && !isSQLiteDriver(driver) &&
		strings.Contains(strings.ToLower(dsn), "sslmode=disable") && !allowInsecureInternal {
		return errors.New("release 模式的 PostgreSQL 不可使用 sslmode=disable；僅隔離的內部容器網路可顯式設定 DATABASE_ALLOW_INSECURE_INTERNAL=true")
	}
	return nil
}

type apiRouteHandlers struct {
	session *handler.SessionHandler
	qti     *handler.QTIHandler
	lti     *handler.LTIHandler
	proctor *handler.ProctorHandler
	results *handler.ResultsHandler
}

// registerProtectedRoutes 集中定義實際 API 安全邊界，避免新增路由時漏掛認證。
func registerProtectedRoutes(r *gin.Engine, jwtConfig middleware.JWTConfig, rateLimiter *middleware.RateLimiter, handlers apiRouteHandlers) {
	auth := middleware.JWTAuthMiddleware(jwtConfig)
	admin := middleware.RequireRole("ADMIN")
	protectedMedia := r.Group("")
	protectedMedia.Use(auth)
	protectedMedia.StaticFS("/uploads", gin.Dir("./uploads", false))

	r.GET("/metrics", auth, admin, func(c *gin.Context) {
		c.JSON(200, middleware.GetMetricsSummary())
	})

	api := r.Group("/api/v1")
	authenticated := api.Group("")
	authenticated.Use(auth)

	sessions := authenticated.Group("/sessions")
	sessions.POST("/start", handlers.session.StartSession)
	sessions.GET("/:id", handlers.session.GetSession)
	sessions.POST("/:id/response", rateLimiter.Middleware(), handlers.session.SaveResponse)
	sessions.POST("/:id/submit", handlers.session.SubmitSession)
	sessions.POST("/:id/proctor/event", handlers.proctor.RecordEvent)

	adminAPI := api.Group("")
	adminAPI.Use(auth, admin)
	adminAPI.GET("/sessions/:id/proctor/log", handlers.proctor.GetProctorLog)
	adminAPI.GET("/sessions/:id/proctor/analytics", handlers.proctor.GetProctorAnalytics)
	adminAPI.GET("/deliveries/:id/results/csv", handlers.results.ExportResultsCSV)
	adminAPI.POST("/items/import-qti", handlers.qti.ImportQTIPackage)
	adminAPI.POST("/lti/platforms", handlers.lti.RegisterPlatform)
	adminAPI.POST("/lti/resource-links", handlers.lti.RegisterResourceLink)
	adminAPI.POST("/webhooks/configs", handlers.session.RegisterWebhook)
	adminAPI.GET("/webhooks/logs", handlers.session.GetWebhookLogs)

	lti := api.Group("/lti")
	lti.Use(rateLimiter.Middleware())
	lti.GET("/login", handlers.lti.InitiateLogin)
	lti.POST("/login", handlers.lti.InitiateLogin)
	lti.POST("/launch", handlers.lti.HandleLaunch)
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
		ID:          "delivery-demo-01",
		TestID:      testID,
		Title:       "2026 全國模擬考演示場次",
		IsActive:    true,
		MaxAttempts: 1,
	}
	db.Create(&delivery)

	logger.Info("示範資料初始化完成！Delivery ID: delivery-demo-01")
}
