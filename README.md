# 🚀 tao-core-go

![Go Version](https://img.shields.io/badge/Go-1.26.5-00ADD8.svg?style=for-the-badge&logo=go)
![Build Status](https://img.shields.io/badge/CI-passing-brightgreen.svg?style=for-the-badge&logo=githubactions)
![License](https://img.shields.io/badge/License-MIT-blue.svg?style=for-the-badge)
![QTI Version](https://img.shields.io/badge/QTI-3.0-orange.svg?style=for-the-badge)
![LTI Version](https://img.shields.io/badge/LTI-1.3%20Advantage-purple.svg?style=for-the-badge)

[ **English** ](#-english) | [ **繁體中文** ](#-繁體中文)

---

## 🌐 English

Modern, High-Concurrency Computer-Based Assessment (CBA) Core Engine rewritten in **Go 1.26.5**.

`tao-core-go` modernizes the legacy PHP-based TAO (`tao-core`) architecture for concurrent examination workloads, integrating **QTI 3.0 Assessment Item Standards**, **IMS LTI 1.3 Advantage** (OIDC SSO & Automatic Grade Service), proctoring telemetry, and a decoupled **In-Memory EventBus**.

---

### 📑 Table of Contents (English)

1. [Key Features & Tech Stack](#-key-features--tech-stack)
2. [System Architecture & Diagrams](#-system-architecture--diagrams)
   - [1. System Layered Architecture](#1-system-layered-architecture)
   - [2. Exam Session Lifecycle & Async Sequence Diagram](#2-exam-session-lifecycle--async-sequence-diagram)
   - [3. QTI 3.0 Package Extraction Pipeline](#3-qti-30-package-extraction-pipeline)
   - [4. Modern ER Model Diagram](#4-modern-er-model-diagram)
3. [Project Directory & Code Maintainability Guide](#-project-directory--code-maintainability-guide)
4. [Complete API Reference & cURL Handbook](#-complete-api-reference--curl-handbook)
5. [Local Development, Testing & Docker Deployment](#-local-development-testing--docker-deployment)

---

### ⚡ Key Features & Tech Stack

* **Core Runtime**: Go 1.26.5, Gin Web Framework, GORM ORM.
* **Production Runtime**: bounded HTTP timeouts, graceful shutdown, and configurable PostgreSQL connection pooling.
* **EdTech Standards**:
  * **QTI 3.0 Package Engine**: Unzips `.zip` packages, parses QTI 3.0 XML, extracts media assets & replaces relative image URLs.
  * **IMS LTI 1.3 Advantage**: OIDC 3rd-party initiated SSO & background AGS (Assignment & Grade Services) grade posting back to Moodle / Canvas.
* **Anti-Cheating & Proctoring Analytics**:
  * Tab switch / focus lost event audit trail (logged for analytics without forced termination).
  * Browser focus and screenshot-attempt telemetry for proctor review (not presented as screenshot prevention).
  * Analytical risk level evaluation (`LOW` / `MEDIUM` / `HIGH`).
* **Core Infrastructure**:
  * Decoupled **EventBus** (Pub/Sub pattern).
  * **Rate Limiter Middleware** (10 req/s per IP).
  * Prometheus Telemetry & Performance endpoint (`/metrics`).
  * Cryptographically secure option shuffle with correct-answer remapping.
* **Cloud-Native Deployment**: non-root, read-only multi-stage container + `docker-compose.yml` (Go + PostgreSQL 16).

---

### 📐 System Architecture & Diagrams

#### 1. System Layered Architecture

```mermaid
flowchart TD
    subgraph Clients ["Clients Layer"]
        Browser["Student Browser / Test Taker"]
        Moodle["Moodle / Canvas LMS"]
        AdminDashboard["Admin / Proctor Dashboard"]
    end

    subgraph Gateway ["Router & Middleware Layer"]
        GinRouter["Gin API Router"]
        JWTAuth["JWT Authentication"]
        RBAC["RBAC Role Guard (RequireRole)"]
        RateLimit["RateLimiter (10 req/s)"]
        MetricsCollector["Prometheus Metrics Collector"]
    end

    subgraph Services ["Core Services Layer"]
        SessionSvc["SessionService"]
        ScoringSvc["ScoringService & Key Balancer"]
        QTISvc["QTIService (QTI 3.0 Engine)"]
        LTISvc["LTIService (LTI 1.3 Engine)"]
        ProctorSvc["ProctorService (Anti-Cheating)"]
        ExportSvc["ResultsExportService (CSV)"]
        EventBus["EventBus (In-Memory Pub/Sub)"]
        WebhookSvc["WebhookWorker (Goroutine Pool)"]
    end

    subgraph Storage ["Storage Layer"]
        DB[(PostgreSQL 16 / SQLite)]
        MediaDir["Authenticated /uploads/media/ Assets"]
        ExternalLMS["Moodle / Canvas Gradebook API"]
    end

    Browser -->|HTTP POST Answer/Switch| GinRouter
    Moodle -->|LTI 1.3 Launch / SSO| GinRouter
    AdminDashboard -->|CSV Export / Metrics| GinRouter

    GinRouter --> JWTAuth --> RBAC --> RateLimit --> MetricsCollector
    MetricsCollector --> Services

    SessionSvc --> ScoringSvc
    SessionSvc --> EventBus
    EventBus -->|Async Event| WebhookSvc
    EventBus -->|Async Grade Return| LTISvc

    QTISvc --> MediaDir
    LTISvc -->|AGS Grade POST| ExternalLMS
    Services --> DB
```

#### 2. Exam Session Lifecycle & Async Sequence Diagram

```mermaid
sequenceDiagram
    autonumber
    actor Student as Student (Test Taker)
    participant API as Gin Router
    participant Session as SessionService
    participant Scoring as ScoringService
    participant EventBus as EventBus
    participant Webhook as WebhookWorker
    participant LTI as LTIService (Moodle)

    Student->>API: POST /api/v1/sessions/start
    API->>Session: StartSession(delivery_id, user_id)
    Session-->>API: Return TestSession (Status: IN_PROGRESS)
    API-->>Student: Render Test Items

    loop Test Taking & Anti-Cheating
        Student->>API: POST /api/v1/sessions/:id/response
        API->>Session: SaveResponse(item_id, response_data)
        
        opt Tab Switch / Screenshot Attempt
            Student->>API: POST /api/v1/sessions/:id/proctor/event
            API->>Session: Record focus/screenshot-attempt telemetry
        end
    end

    Student->>API: POST /api/v1/sessions/:id/submit
    API->>Session: SubmitSession(session_id)
    Session->>Scoring: ScoreItem() Compute Scores
    Scoring-->>Session: Return TotalScore
    Session->>Session: Update Status = COMPLETED

    Session->>EventBus: Publish("session.completed", session)
    
    par Async 1: Webhook Dispatch
        EventBus->>Webhook: Trigger Webhook Event
        Webhook->>Webhook: HTTP POST to registered URLs
    and Async 2: LTI Grade Posting
        EventBus->>LTI: Trigger SubmitGradeToLMS
        LTI->>LTI: HTTP POST AGS to Moodle Gradebook
    end

    Session-->>API: Return Session Result (COMPLETED)
    API-->>Student: Exam Completed & Final Score
```

#### 3. QTI 3.0 Package Extraction Pipeline

```mermaid
flowchart LR
    A[Upload QTI3.zip File] --> B[archive/zip Extraction]
    B --> C{File Type?}
    C -->|XML Question| D[parseQTIXMLFile]
    C -->|Image/Audio| E[saveMediaFile]
    E --> F[Save to ./uploads/media/uuid.png]
    D --> G[Parse qti-assessment-item Tags]
    F --> H[Replace Relative Asset Paths with Server URL]
    G --> H
    H --> I["Cryptographic Option Shuffle<br/>+ Answer Remapping"]
    I --> J[GORM Write to ITEMS & OPTIONS Tables]
```

#### 4. Modern ER Model Diagram

```mermaid
erDiagram
    USERS ||--o{ USER_ROLES : has
    ROLES ||--o{ USER_ROLES : belongs_to
    ROLES ||--o{ ROLE_PERMISSIONS : contains
    PERMISSIONS ||--o{ ROLE_PERMISSIONS : defined_in

    TESTS ||--|{ TEST_SECTIONS : contains
    TEST_SECTIONS ||--|{ TEST_ITEMS : includes
    ITEMS ||--|| TEST_ITEMS : references

    TESTS ||--o{ DELIVERIES : deploys
    DELIVERIES ||--o{ TEST_SESSIONS : creates
    USERS ||--o{ TEST_SESSIONS : takes

    TEST_SESSIONS ||--o{ ITEM_RESPONSES : records
    ITEMS ||--o{ ITEM_RESPONSES : targets

    GROUPS ||--o{ USER_GROUPS : includes
    USERS ||--o{ USER_GROUPS : belongs_to
    DELIVERIES ||--o{ DELIVERY_GROUPS : restricts
    GROUPS ||--o{ DELIVERY_GROUPS : authorizes

    WEBHOOK_CONFIGS ||--o{ WEBHOOK_LOGS : triggers
    TEST_SESSIONS ||--o{ PROCTOR_EVENTS : logs

    USERS {
        string id PK
        string username
        string email
        boolean is_active
    }

    TESTS {
        string id PK
        string title
        string qti_version
        int time_limit_seconds
    }

    ITEMS {
        string id PK
        string title
        string prompt
        string item_type "SINGLE_CHOICE/MULTIPLE_CHOICE"
        string options_json
        string correct_answer
        float max_score
        string layout_hint "AUTO/1_COL/2_COL/4_COL"
    }

    DELIVERIES {
        string id PK
        string test_id FK
        string title
        datetime start_time
        datetime end_time
    }

    TEST_SESSIONS {
        string id PK
        string delivery_id FK
        string user_id FK
        string status "NOT_STARTED/IN_PROGRESS/COMPLETED"
        float total_score
        int time_spent_seconds
    }

    ITEM_RESPONSES {
        string id PK
        string session_id FK
        string item_id FK
        string response_data
        float score_given
        boolean is_correct
    }

    PROCTOR_EVENTS {
        string id PK
        string session_id FK
        string event_type "TAB_SWITCH/FOCUS_LOST/SCREENSHOT_ATTEMPT"
        int duration_seconds
    }

    LTI_PLATFORMS {
        string id PK
        string issuer
        string client_id
        string auth_login_url
    }
```

---

### 🛠️ Project Directory & Code Maintainability Guide

```text
tao-core-go/
├── Dockerfile                       # Multi-stage non-root Docker build
├── docker-compose.yml               # One-click deployment environment (Go + PostgreSQL)
├── go.mod                           # Go module manifest
├── go.sum                           # Go dependency checksum lock
├── config/
│   └── config.yaml                  # System default configuration
├── cmd/
│   └── server/
│       └── main.go                  # System entrypoint (Auto-Migration, Services, Middleware, Routes)
├── uploads/                         # Media asset storage
│   └── media/
└── internal/
    ├── domain/
    │   └── models/                  # [Data Models] GORM strongly-typed models (User, Group, Test, Session, LTI, Proctor)
    ├── middleware/                  # [Middlewares] (JWT Auth, RBAC, RateLimiter, Prometheus Metrics)
    ├── handler/                     # [Controllers] HTTP Handlers (Session, QTI, LTI, Proctor, Results CSV)
    └── service/                     # [Business Logic] (EventBus, Session, Scoring, QTI, LTI, Proctor, Webhook)
```

---

### 📖 Complete API Reference & cURL Handbook

Server default address: `http://localhost:8080`

Security defaults:

* `JWT_SECRET` (32+ bytes), `JWT_ISSUER`, `JWT_AUDIENCE`, a base64-encoded 32-byte `APP_ENCRYPTION_KEY`, and `WEBHOOK_ALLOWED_HOSTS` are required at startup.
* Application APIs, `/metrics`, and `/uploads` require a Bearer JWT. Metrics, result export, QTI import, LTI platform management, proctor reports, and webhook management additionally require the `ADMIN` role.
* LTI login/launch are public protocol endpoints. Launch tokens are verified with the registered platform JWKS plus one-time, expiring state and nonce values before a scoped student token is issued.
* Webhook receivers should verify `X-Tao-Signature` as `v1=HMAC-SHA256(secret, timestamp + "." + delivery + "." + raw_body)`, reject stale `X-Tao-Timestamp` values, and deduplicate `X-Tao-Delivery`.
* This service does not currently issue login tokens; set `JWT_TOKEN` below to a token issued by the trusted identity service.

```bash
export JWT_TOKEN="<SIGNED_JWT>"
```

#### 1. System & Telemetry
* **Healthcheck (`GET /health`)**
* **Metrics (`GET /metrics`)**: Prometheus metrics (total requests, active sessions, goroutine count, memory allocated).

#### 2. Test Session API
* **Start Session (`POST /api/v1/sessions/start`)**
  ```bash
  curl -X POST http://localhost:8080/api/v1/sessions/start \
       -H "Authorization: Bearer ${JWT_TOKEN}" \
       -H "Content-Type: application/json" \
       -d '{"delivery_id":"delivery-demo-01"}'
  ```
* **Save Response (`POST /api/v1/sessions/:id/response`)** *(Rate-limited: 10 req/s)*
  ```bash
  curl -X POST http://localhost:8080/api/v1/sessions/<SESSION_ID>/response \
       -H "Authorization: Bearer ${JWT_TOKEN}" \
       -H "Content-Type: application/json" \
       -d '{"item_id":"item-demo-01", "response_data":"A"}'
  ```
* **Submit Session (`POST /api/v1/sessions/:id/submit`)** *(Idempotent weighted scoring, EventBus/Webhook dispatch, and verified LTI AGS grade return)*
  ```bash
  curl -X POST http://localhost:8080/api/v1/sessions/<SESSION_ID>/submit \
       -H "Authorization: Bearer ${JWT_TOKEN}"
  ```

#### 3. Proctoring & Anti-Cheating Analytics API
* **Record Event (`POST /api/v1/sessions/:id/proctor/event`)**
  ```bash
  curl -X POST http://localhost:8080/api/v1/sessions/<SESSION_ID>/proctor/event \
       -H "Authorization: Bearer ${JWT_TOKEN}" \
       -H "Content-Type: application/json" \
       -d '{"event_type":"TAB_SWITCH", "duration_seconds":15, "details":"Tab switched to browser"}'
  ```
* **Get Risk Analytics (`GET /api/v1/sessions/:id/proctor/analytics`)**
  ```bash
  curl http://localhost:8080/api/v1/sessions/<SESSION_ID>/proctor/analytics \
       -H "Authorization: Bearer ${JWT_TOKEN}"
  ```

#### 4. CSV Results Export API
* **Export Delivery CSV (`GET /api/v1/deliveries/:id/results/csv`)**
  ```bash
  curl -o results.csv http://localhost:8080/api/v1/deliveries/delivery-demo-01/results/csv \
       -H "Authorization: Bearer ${JWT_TOKEN}"
  ```

#### 5. QTI 3.0 Package Import API
* **Import QTI 3.0 ZIP (`POST /api/v1/items/import-qti`)**
  ```bash
  curl -X POST http://localhost:8080/api/v1/items/import-qti \
       -H "Authorization: Bearer ${JWT_TOKEN}" \
       -F "file=@/path/to/qti3_exam_package.zip"
  ```

---

### 🐳 Local Development, Testing & Docker Deployment

The Compose port binds to `127.0.0.1:8080`; expose it through a TLS-terminating reverse proxy for remote production traffic. The bundled PostgreSQL uses `sslmode=disable` only inside the unexposed Compose network. For an external PostgreSQL service, remove `DATABASE_ALLOW_INSECURE_INTERNAL` and use `sslmode=verify-full` with a trusted CA.

When upgrading from a version that stored LTI or Webhook secrets in plaintext, re-register those records. Plaintext legacy secrets are intentionally refused; rotating `APP_ENCRYPTION_KEY` also requires decrypting/re-encrypting stored secrets before deployment.

```bash
cd tao-core-go

# 1. Configure Go proxy mirror
go env -w GOPROXY=https://proxy.golang.org,direct
go env -w GOSUMDB=sum.golang.org

# 2. Run unit tests
go test -v ./...

# 3. Configure required secrets, then launch Docker Compose
cp .env.example .env
# Replace every placeholder in .env before continuing.
docker compose up -d --build
```

---
---

## 🌐 繁體中文

基於 **Go 1.26.5** 語言重構的高效能、高併發電腦化測驗與評量核心引擎 (Modern High-Concurrency Computer-Based Assessment Core Engine)。

本專案旨在全面升級傳統 PHP 版 TAO (`tao-core`) 系統，解決大考時的高併發交卷瓶頸，並整合最新 **QTI 3.0 試題規範**、**IMS LTI 1.3 Advantage** 跨平台單點登入與成績自動回寫、**監考防作弊數據分析 (切頁黑屏遮蓋)** 與 **解耦事件總線 (EventBus)**。

---

### 📑 目錄 (繁體中文)

1. [核心特點與技術堆疊](#-核心特點與技術堆疊-1)
2. [詳細系統架構與圖解](#-詳細系統架構與圖解-1)
   - [1. 系統整體分層架構圖](#1-系統整體分層架構圖)
   - [2. 測驗會話生命週期與異步事件時序圖](#2-測驗會話生命週期與異步事件時序圖)
   - [3. QTI 3.0 試題包解構管道流程圖](#3-qti-30-試題包解構管道流程圖)
   - [4. 現代化強型別 ER Model 關聯圖](#4-現代化強型別-er-model-關聯圖)
3. [專案目錄結構與原始碼維護指南](#-專案目錄結構與原始碼維護指南-1)
4. [完整 API 介面說明與操作手冊](#-完整-api-介面說明與操作手冊-1)
5. [本地開發、測試與 Docker 部署指南](#-本地開發測試與-docker-部署指南-1)

---

### ⚡ 核心特點與技術堆疊

* **核心語言與框架**：Go 1.26.5、Gin Web Framework、GORM ORM。
* **生產執行環境**：具備 HTTP timeout、優雅關閉及可設定的 PostgreSQL 連線池。
* **國際標準整合**：
  * **QTI 3.0**：支援上傳 `.zip` 試題包、解壓 XML、自動提取圖片並儲存。
  * **IMS LTI 1.3 Advantage**：支援 OIDC 免登入 SSO 開考與 AGS (Assignment & Grade Services) 成績背景自動回寫至 Moodle / Canvas。
* **監考防作弊與數據分析**：
  * 切頁/離頁事件日誌紀錄（不強制交卷，供後續監考分析）。
  * 瀏覽器焦點與截圖嘗試遙測，供監考人員複核（不宣稱能阻止作業系統截圖）。
  * 自動評估 `LOW` / `MEDIUM` / `HIGH` 風險等級報告。
* **基礎設施與安全性**：
  * 內建解耦內部事件總線 (**EventBus**)。
  * API 防刷限流中間件 (**Rate Limiter - 10 req/s per IP**)。
  * Prometheus 流量與效能監控端點 (**`/metrics`**)。
  * 使用密碼學安全亂數洗牌，並同步重映射標準答案。
* **安全部署**：非 root、唯讀 Multi-stage `Dockerfile` + `docker-compose.yml` (Go + PostgreSQL)。

---

### 📐 詳細系統架構與圖解

#### 1. 系統整體分層架構圖

```mermaid
flowchart TD
    subgraph Clients ["用戶端層 (Clients)"]
        Browser["學生瀏覽器 / 考場端"]
        Moodle["Moodle / Canvas LMS"]
        AdminDashboard["管理員 / 監考儀表板"]
    end

    subgraph Gateway ["路由與中間件層 (Router & Middleware)"]
        GinRouter["Gin API Router"]
        JWTAuth["JWT 認證中間件"]
        RBAC["RBAC 角色檢查 (RequireRole)"]
        RateLimit["RateLimiter 限流 (10 req/s)"]
        MetricsCollector["Prometheus Metrics 收集器"]
    end

    subgraph Services ["核心業務邏輯層 (Core Services)"]
        SessionSvc["測驗會話服務 (SessionService)"]
        ScoringSvc["自動計分與答案平衡 (ScoringService)"]
        QTISvc["QTI 3.0 解析與匯入 (QTIService)"]
        LTISvc["LTI 1.3 SSO 與成績回寫 (LTIService)"]
        ProctorSvc["防作弊與風險分析 (ProctorService)"]
        ExportSvc["成績 Raw Data CSV 匯出 (ResultsExportService)"]
        EventBus["內部解耦事件總線 (EventBus)"]
        WebhookSvc["Goroutine Webhook 派送器 (WebhookService)"]
    end

    subgraph Storage ["資料與儲存層 (Storage Layer)"]
        DB[(PostgreSQL / SQLite)]
        MediaDir["需驗證的 /uploads/media/ 媒體檔"]
        ExternalLMS["Moodle / Canvas 遠端 Gradebook API"]
    end

    Browser -->|HTTP POST 答題/切頁| GinRouter
    Moodle -->|LTI 1.3 Launch / SSO| GinRouter
    AdminDashboard -->|CSV 匯出 / Metrics 監控| GinRouter

    GinRouter --> JWTAuth --> RBAC --> RateLimit --> MetricsCollector
    MetricsCollector --> Services

    SessionSvc --> ScoringSvc
    SessionSvc --> EventBus
    EventBus -->|異步觸發 session.completed| WebhookSvc
    EventBus -->|異步觸發 LTI 成績回傳| LTISvc

    QTISvc --> MediaDir
    LTISvc -->|AGS Grade POST| ExternalLMS
    Services --> DB
```

#### 2. 測驗會話生命週期與異步事件時序圖

```mermaid
sequenceDiagram
    autonumber
    actor Student as 考生 (Student)
    participant API as Gin Router
    participant Session as SessionService
    participant Scoring as ScoringService
    participant EventBus as EventBus
    participant Webhook as WebhookWorker
    participant LTI as LTIService (Moodle)

    Student->>API: POST /api/v1/sessions/start
    API->>Session: StartSession(delivery_id, user_id)
    Session-->>API: 回傳 Session (Status: IN_PROGRESS)
    API-->>Student: 開始考試並渲染題目

    loop 答題過程 (含防作弊監控)
        Student->>API: POST /api/v1/sessions/:id/response (暫存答案)
        API->>Session: SaveResponse(item_id, response_data)
        
        opt 考生切頁或嘗試截圖
            Student->>API: POST /api/v1/sessions/:id/proctor/event
            API->>Session: 記錄切頁 / 觸發 Web 黑屏遮蓋
        end
    end

    Student->>API: POST /api/v1/sessions/:id/submit (終止交卷)
    API->>Session: SubmitSession(session_id)
    Session->>Scoring: ScoreItem() 計算總分
    Scoring-->>Session: 回傳 TotalScore
    Session->>Session: 更新 Status = COMPLETED

    Session->>EventBus: Publish("session.completed", session)
    
    par 異步處理 1: Webhook 派送
        EventBus->>Webhook: 觸發 Webhook Worker
        Webhook->>Webhook: HTTP POST 發送事件給第三方 Server
    and 異步處理 2: LTI 成績回寫
        EventBus->>LTI: 觸發 SubmitGradeToLMS
        LTI->>LTI: HTTP POST AGS 回寫成績給 Moodle Gradebook
    end

    Session-->>API: 回傳最終計分結果 (COMPLETED)
    API-->>Student: 顯示交卷成功與得分！
```

#### 3. QTI 3.0 試題包解構管道流程圖

```mermaid
flowchart LR
    A[上傳 QTI3.zip 檔] --> B[archive/zip 解壓]
    B --> C{讀取檔案類型}
    C -->|XML 題目| D[parseQTIXMLFile]
    C -->|圖片/音訊| E[saveMediaFile]
    E --> F[儲存至 ./uploads/media/uuid.png]
    D --> G[解析 qti-assessment-item 標籤]
    F --> H[取代題目 HTML 中的相對圖片路徑為伺服器 URL]
    G --> H
    H --> I["密碼學安全選項洗牌<br/>並同步重映射答案"]
    I --> J[GORM 寫入 ITEMS & OPTIONS 資料表]
```

#### 4. 現代化強型別 ER Model 關聯圖

```mermaid
erDiagram
    USERS ||--o{ USER_ROLES : has
    ROLES ||--o{ USER_ROLES : belongs_to
    ROLES ||--o{ ROLE_PERMISSIONS : contains
    PERMISSIONS ||--o{ ROLE_PERMISSIONS : defined_in

    TESTS ||--|{ TEST_SECTIONS : contains
    TEST_SECTIONS ||--|{ TEST_ITEMS : includes
    ITEMS ||--|| TEST_ITEMS : references

    TESTS ||--o{ DELIVERIES : deploys
    DELIVERIES ||--o{ TEST_SESSIONS : creates
    USERS ||--o{ TEST_SESSIONS : takes

    TEST_SESSIONS ||--o{ ITEM_RESPONSES : records
    ITEMS ||--o{ ITEM_RESPONSES : targets

    GROUPS ||--o{ USER_GROUPS : includes
    USERS ||--o{ USER_GROUPS : belongs_to
    DELIVERIES ||--o{ DELIVERY_GROUPS : restricts
    GROUPS ||--o{ DELIVERY_GROUPS : authorizes

    WEBHOOK_CONFIGS ||--o{ WEBHOOK_LOGS : triggers
    TEST_SESSIONS ||--o{ PROCTOR_EVENTS : logs

    USERS {
        string id PK
        string username
        string email
        boolean is_active
    }

    TESTS {
        string id PK
        string title
        string qti_version
        int time_limit_seconds
    }

    ITEMS {
        string id PK
        string title
        string prompt
        string item_type "SINGLE_CHOICE/MULTIPLE_CHOICE"
        string options_json
        string correct_answer
        float max_score
        string layout_hint "AUTO/1_COL/2_COL/4_COL"
    }

    DELIVERIES {
        string id PK
        string test_id FK
        string title
        datetime start_time
        datetime end_time
    }

    TEST_SESSIONS {
        string id PK
        string delivery_id FK
        string user_id FK
        string status "NOT_STARTED/IN_PROGRESS/COMPLETED"
        float total_score
        int time_spent_seconds
    }

    ITEM_RESPONSES {
        string id PK
        string session_id FK
        string item_id FK
        string response_data
        float score_given
        boolean is_correct
    }

    PROCTOR_EVENTS {
        string id PK
        string session_id FK
        string event_type "TAB_SWITCH/FOCUS_LOST/SCREENSHOT_ATTEMPT"
        int duration_seconds
    }

    LTI_PLATFORMS {
        string id PK
        string issuer
        string client_id
        string auth_login_url
    }
```

---

### 🛠️ 專案目錄結構與原始碼維護指南

```text
tao-core-go/
├── Dockerfile                       # Multi-stage 非 root Docker 建構檔
├── docker-compose.yml               # 一鍵部署配置 (Go Engine + PostgreSQL)
├── go.mod                           # Go 模組定義與依賴套件
├── go.sum                           # 套件版本鎖定檔
├── config/
│   └── config.yaml                  # 系統預設設定檔
├── cmd/
│   └── server/
│       └── main.go                  # 系統進入點 (Auto-Migration, Services, Middleware, Routes)
├── uploads/                         # 靜態媒體檔案儲存區
│   └── media/
└── internal/
    ├── domain/
    │   └── models/                  # [資料實體層] GORM 強型別資料結構 (User, Group, Test, Session, LTI, Proctor)
    ├── middleware/                  # [中間件層] (JWT Auth, RBAC, RateLimiter, Prometheus Metrics)
    ├── handler/                     # [控制層] HTTP API 控制器 (Session, QTI, LTI, Proctor, Results CSV)
    └── service/                     # [業務邏輯層] (EventBus, Session, Scoring, QTI, LTI, Proctor, Webhook)
```

---

### 📖 完整 API 介面說明與操作手冊

伺服器預設網址：`http://localhost:8080`

安全預設值：

* 服務啟動時必須提供至少 32 bytes 的 `JWT_SECRET`、`JWT_ISSUER`、`JWT_AUDIENCE`、base64 編碼的 32-byte `APP_ENCRYPTION_KEY`，以及 `WEBHOOK_ALLOWED_HOSTS`。
* 應用 API、`/metrics` 與 `/uploads` 均要求 Bearer JWT；監控、成績匯出、QTI 匯入、LTI 平台管理、監考報告及 Webhook 管理還需要 `ADMIN` 角色。
* LTI login/launch 是公開協定入口；系統會以平台 JWKS、一次性且具時效的 state/nonce 驗證 launch，成功後才簽發學生權限 token。
* Webhook 接收端應將 `X-Tao-Signature` 驗證為 `v1=HMAC-SHA256(secret, timestamp + "." + delivery + "." + raw_body)`，拒絕過期的 `X-Tao-Timestamp`，並以 `X-Tao-Delivery` 去重。
* 本服務目前不簽發登入 token；以下 `JWT_TOKEN` 必須由受信任的身分服務簽發。

```bash
export JWT_TOKEN="<SIGNED_JWT>"
```

#### 1. 系統健康檢查與監控
* **GET `/health`**：檢視系統健康狀態。
* **GET `/metrics`**：檢視 Prometheus 即時監控數據（總請求數、進行中會話數、Goroutine 數量、記憶體開銷）。

#### 2. 測驗會話 (TestSession) API
* **開始考試 (`POST /api/v1/sessions/start`)**
  ```bash
  curl -X POST http://localhost:8080/api/v1/sessions/start \
       -H "Authorization: Bearer ${JWT_TOKEN}" \
       -H "Content-Type: application/json" \
       -d '{"delivery_id":"delivery-demo-01"}'
  ```
* **暫存答案 (`POST /api/v1/sessions/:id/response`)** *(內建 10 req/s 限流)*
  ```bash
  curl -X POST http://localhost:8080/api/v1/sessions/<SESSION_ID>/response \
       -H "Authorization: Bearer ${JWT_TOKEN}" \
       -H "Content-Type: application/json" \
       -d '{"item_id":"item-001", "response_data":"A"}'
  ```
* **終止交卷 (`POST /api/v1/sessions/:id/submit`)** *(冪等加權計分、EventBus/Webhook 派送與已驗證的 LTI AGS 成績回寫)*
  ```bash
  curl -X POST http://localhost:8080/api/v1/sessions/<SESSION_ID>/submit \
       -H "Authorization: Bearer ${JWT_TOKEN}"
  ```

#### 3. 監考防作弊與切頁數據分析 API
* **記錄切頁 / 黑屏防截圖事件 (`POST /api/v1/sessions/:id/proctor/event`)**
  ```bash
  curl -X POST http://localhost:8080/api/v1/sessions/<SESSION_ID>/proctor/event \
       -H "Authorization: Bearer ${JWT_TOKEN}" \
       -H "Content-Type: application/json" \
       -d '{"event_type":"TAB_SWITCH", "duration_seconds":15, "details":"Tab switched to browser"}'
  ```
* **取得監考風險分析報告 (`GET /api/v1/sessions/:id/proctor/analytics`)**
  ```bash
  curl http://localhost:8080/api/v1/sessions/<SESSION_ID>/proctor/analytics \
       -H "Authorization: Bearer ${JWT_TOKEN}"
  ```

#### 4. 成績 Raw Data CSV 匯出 API
* **匯出指定測驗全體成績 CSV (`GET /api/v1/deliveries/:id/results/csv`)**
  ```bash
  curl -o results.csv http://localhost:8080/api/v1/deliveries/delivery-demo-01/results/csv \
       -H "Authorization: Bearer ${JWT_TOKEN}"
  ```

#### 5. QTI 3.0 試題包匯入 API
* **上傳 QTI3.zip 檔案 (`POST /api/v1/items/import-qti`)**
  ```bash
  curl -X POST http://localhost:8080/api/v1/items/import-qti \
       -H "Authorization: Bearer ${JWT_TOKEN}" \
       -F "file=@/path/to/qti3_exam_package.zip"
  ```

---

### 🐳 本地開發、測試與 Docker 部署指南

Compose 將服務綁定於 `127.0.0.1:8080`；遠端生產流量應透過終止 TLS 的反向代理公開。內附 PostgreSQL 的 `sslmode=disable` 僅限未發布連接埠的 Compose 內部網路；使用外部 PostgreSQL 時，請移除 `DATABASE_ALLOW_INSECURE_INTERNAL`，並搭配受信任 CA 使用 `sslmode=verify-full`。

若從曾以明文保存 LTI 或 Webhook 秘密的版本升級，必須重新註冊相關紀錄。系統會刻意拒絕讀取舊明文；輪替 `APP_ENCRYPTION_KEY` 前也必須先完成既有秘密的解密與重新加密。

```bash
cd tao-core-go

# 1. 設定 Go 代理連線
go env -w GOPROXY=https://proxy.golang.org,direct
go env -w GOSUMDB=sum.golang.org

# 2. 執行全套單元測試
go test -v ./...

# 3. 設定必要密鑰後啟動 Docker Compose
cp .env.example .env
# 繼續前請先替換 .env 內所有 placeholder。
docker compose up -d --build
```

---

## 📜 Acknowledgements & Trademark Disclaimer (致謝與商標免責聲明)

* **TAO®** and **Open Assessment Technologies** are registered trademarks of **Open Assessment Technologies S.A.**
* `tao-core-go` is an independent, clean-room Go implementation inspired by the concepts of computerized testing and assessment standards (QTI & LTI).
* This project is **not** affiliated with, endorsed by, or sponsored by Open Assessment Technologies S.A.
