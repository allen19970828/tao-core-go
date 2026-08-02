# Staging 部署、驗收與回復手冊

本手冊提供 tao-core-go 上線前的可重複流程：備份 PostgreSQL、套用並驗證 migration、部署 staging、執行 readiness 與黑箱 E2E，以及在必要時回復 staging 資料庫。

所有工具都透過環境變數接收資料庫 DSN 與秘密，避免將憑證放入 command-line argument、Git history 或一般 log。範例中的值均為 placeholder，不可直接用於正式環境。

## 1. 安全邊界

- `scripts/staging-readiness.sh` 是讀取設定與呼叫 HTTP API 的 preflight；只有在提供 staging 專用 delivery 時，E2E 才會新增測驗 session、response 與 proctor event。
- `scripts/postgres-backup.sh` 只建立 custom-format PostgreSQL backup 與 SHA-256 checksum。
- `scripts/migrate-staging.sh` 會修改 schema，必須同時提供 staging target、明確 confirmation 與已存在的 backup 檔案。
- `scripts/restore-staging.sh` 會清理並回復 archive 中的資料庫物件，必須同時提供兩道 staging confirmation，以及 restore 前的新備份。
- 本流程不會合併 PR，也不會部署 production。

## 2. 工具需求

- Go 1.26.5
- `curl`、`openssl`
- 備份／回復：與目標 PostgreSQL major version 相容的 `pg_dump`、`pg_restore`
- Compose staging：Docker 與 `docker compose`

外部 PostgreSQL staging 必須使用 `sslmode=verify-full` 與受信任 CA。只有未發布資料庫連接埠的隔離 Compose 網路，才可將 `DATABASE_ALLOW_INSECURE_INTERNAL=true` 與 `sslmode=disable` 一起使用。

## 3. 準備 staging 設定

以 [`deploy/staging.env.example`](../deploy/staging.env.example) 建立 secret manager 項目或本機未追蹤的環境檔。至少需要：

- `STAGING_BASE_URL`
- `SERVER_MODE=release`
- `DEMO_SEED_ENABLED=false`
- `JWT_SECRET`、`JWT_ISSUER`、`JWT_AUDIENCE`
- `APP_ENCRYPTION_KEY`
- `WEBHOOK_ALLOWED_HOSTS`
- `DATABASE_DRIVER=postgres`
- `DATABASE_DSN`

不要將 staging 或 production 的實際環境檔加入 Git。

## 4. 建立 migration 前備份

指定一個受存取控制、具足夠空間且不在 repository 內的目錄：

```bash
export DATABASE_DSN='<staging PostgreSQL DSN>'
export BACKUP_DIR='/secure/tao-staging-backups'
./scripts/postgres-backup.sh
```

成功時會輸出：

```text
backup_file=/secure/tao-staging-backups/tao-core-go-<timestamp>.dump
checksum_file=/secure/tao-staging-backups/tao-core-go-<timestamp>.dump.sha256
```

工具會先以 `pg_restore --list` 檢查 archive，完成後才把 `.partial` 檔案移為最終檔名。部署紀錄必須保存 backup 與 checksum 路徑。

## 5. 套用與驗證 migration

`cmd/migrate` 提供兩種模式：

- `-mode up`：執行 additive GORM migration、移除 legacy LTI issuer index，並驗證必要 schema。
- `-mode verify`：只讀檢查必要 tables 與關鍵 unique indexes。

套用 staging migration：

```bash
export SERVER_MODE='release'
export DATABASE_DRIVER='postgres'
export DATABASE_DSN='<staging PostgreSQL DSN with sslmode=verify-full>'
export DATABASE_ALLOW_INSECURE_INTERNAL='false'
export STAGING_BACKUP_FILE='/secure/tao-staging-backups/<verified-backup>.dump'
export STAGING_BACKUP_CHECKSUM_FILE='/secure/tao-staging-backups/<verified-backup>.dump.sha256'
export MIGRATION_TARGET='staging'
export MIGRATION_CONFIRMATION='APPLY_STAGING_MIGRATION'
./scripts/migrate-staging.sh
```

若只需要 read-only schema verification：

```bash
go run ./cmd/migrate -mode verify
```

必要 schema 包含所有 domain tables，並特別檢查下列併發與 mapping 約束：

- `idx_delivery_user_attempt`
- `idx_session_item`
- `idx_lti_platform_registration`
- `idx_lti_resource_mapping`

## 6. 部署與流量前 readiness

部署新 binary／container 後，先保持在不接收正式流量的狀態。服務提供兩個不同用途的 endpoint：

- `GET /health`：只確認 HTTP process 存活。
- `GET /ready`：另外以兩秒 timeout 驗證資料庫連線；資料庫不可用時回傳 HTTP 503。

執行 preflight：

```bash
export STAGING_BASE_URL='https://staging-tao.example.com'
export STAGING_DEPLOYMENT_MODE='external'
export STAGING_RUN_E2E='true'
export SERVER_MODE='release'
export DEMO_SEED_ENABLED='false'
export JWT_SECRET='<staging secret>'
export JWT_ISSUER='tao-core-go-staging'
export JWT_AUDIENCE='tao-core-go-staging-client'
export APP_ENCRYPTION_KEY='<base64 encoded 32-byte key>'
export WEBHOOK_ALLOWED_HOSTS='hooks.staging.example.com'
export DATABASE_DRIVER='postgres'
export DATABASE_DSN='<staging PostgreSQL DSN with sslmode=verify-full>'
make staging-readiness
```

Preflight 會檢查：

- release mode、demo seed 關閉及 PostgreSQL transport policy。
- JWT secret 長度與 AES key base64／32-byte 格式。
- Webhook allowlist 未包含 localhost、`.local` 或 literal IPv4。
- `/health`、`/ready` 與主要 security headers。
- 匿名 401、student 403、ADMIN route、JSON body limit、LTI 缺參數拒絕。
- Webhook SSRF 阻擋與 SVG QTI 拒絕。

## 7. 完整測驗 E2E

若設定以下變數，preflight 會額外執行具狀態的完整流程：

```bash
export E2E_DELIVERY_ID='<staging-only delivery id>'
export E2E_ITEM_ID='<item in that delivery>'
export E2E_ITEM_RESPONSE='<known response>'
export E2E_EXPECTED_SCORE='<expected score>'
make staging-readiness
```

此流程會建立 staging 測驗紀錄並驗證：

1. 開始 session 與重複開始冪等。
2. 跨學生查詢被拒絕。
3. 不屬於 delivery 的 item 被拒絕。
4. 監考事件可在 active session 寫入。
5. 儲存答案、交卷與分數。
6. 重複交卷冪等，完成後不可再修改答案。
7. ADMIN analytics 與 CSV export 包含該 session。

若 staging token 必須由外部 IdP 簽發，可設定 `E2E_STUDENT_TOKEN`、`E2E_OTHER_STUDENT_TOKEN` 與 `E2E_ADMIN_TOKEN`；否則測試會使用 staging JWT 設定產生 15 分鐘 token。E2E delivery 必須專供測試，避免污染人工驗收資料。

## 8. 回復 staging

回復前先停止寫入並再建立一份「restore 前」備份。確認兩份 archive 都能通過 `pg_restore --list` 後，才執行：

```bash
export RESTORE_TARGET='staging'
export RESTORE_CONFIRMATION='RESTORE_STAGING_DATABASE'
export RESTORE_DATABASE_DSN='<staging PostgreSQL DSN>'
export BACKUP_FILE='/secure/tao-staging-backups/<backup-to-restore>.dump'
export BACKUP_CHECKSUM_FILE='/secure/tao-staging-backups/<backup-to-restore>.dump.sha256'
export PRE_RESTORE_BACKUP_FILE='/secure/tao-staging-backups/<backup-created-immediately-before-restore>.dump'
export PRE_RESTORE_BACKUP_CHECKSUM_FILE='/secure/tao-staging-backups/<backup-created-immediately-before-restore>.dump.sha256'
./scripts/restore-staging.sh
```

回復完成後，流量仍應維持關閉，並依序執行：

```bash
go run ./cmd/migrate -mode verify
make staging-readiness
```

如果 schema verify 或 E2E 失敗，不可開放流量；應保留應用 log、資料庫 snapshot 與失敗輸出，選擇 forward-fix 或再次回復。

## 9. 驗收通過條件

- PostgreSQL backup archive 與 checksum 已保存。
- Migration `up` 與 `verify` 均成功。
- `/health` 與 `/ready` 均回傳 200；資料庫中斷時 `/ready` 回傳 503。
- 黑箱安全邊界與完整測驗 E2E 全數通過。
- GitHub Actions 的 security、race、coverage、PostgreSQL migration/E2E、Linux build 與 Docker build 全數成功。
- staging log 未出現 token、secret、DSN 或 private key。
- 完成另一位 reviewer 的認證、migration、LTI 與 SSRF 審查後，才將 PR 轉為 Ready for review。
