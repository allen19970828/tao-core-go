.PHONY: test verify e2e staging-readiness db-verify db-migrate-staging

test:
	go test ./...

verify:
	go mod verify
	test -z "$$(gofmt -l .)"
	go vet ./...
	go test -race ./...

e2e:
	./scripts/run-e2e.sh

staging-readiness:
	./scripts/staging-readiness.sh

db-verify:
	go run ./cmd/migrate -mode verify

db-migrate-staging:
	./scripts/migrate-staging.sh
