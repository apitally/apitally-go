.PHONY: test

test:
	go test -v -race -coverprofile=coverage.out ./...
