.PHONY: check check-root check-chi check-gin check-fiber

check: check-root check-chi check-gin check-fiber

check-root:
	go build -o /dev/null ./...
	go vet ./...
	gofmt -l .
	go mod verify && go mod tidy -v

check-chi:
	cd chi && go build -o /dev/null ./...
	cd chi && go vet ./...
	cd chi && gofmt -l .
	cd chi && go mod verify && go mod tidy -v

check-gin:
	cd gin && go build -o /dev/null ./...
	cd gin && go vet ./...
	cd gin && gofmt -l .
	cd gin && go mod verify && go mod tidy -v

check-fiber:
	cd fiber && go build -o /dev/null ./...
	cd fiber && go vet ./...
	cd fiber && gofmt -l .
	cd fiber && go mod verify && go mod tidy -v

.PHONY: test test-root test-chi test-gin test-fiber

test: test-root test-chi test-gin test-fiber

test-root:
	go test -p 1 -v -race -coverprofile=coverage.out ./...

test-chi:
	cd chi && go test -p 1 -v -race -coverprofile=coverage.out ./...

test-gin:
	cd gin && go test -p 1 -v -race -coverprofile=coverage.out ./...

test-fiber:
	cd fiber && go test -p 1 -v -race -coverprofile=coverage.out ./...
