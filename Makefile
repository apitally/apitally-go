MODULES := . chi-v5 echo-v4 echo-v5 gin-v1

.PHONY: check test

check:
	@for m in $(MODULES); do \
		echo "Checking $$m"; \
		(cd $$m \
			&& go build ./... \
			&& go vet ./... \
			&& unformatted=$$(gofmt -l $$(go list -f '{{.Dir}}/*.go' ./...)) \
			&& { test -z "$$unformatted" || { echo "Not gofmt-formatted: $$unformatted"; false; }; } \
			&& go mod verify \
			&& go mod tidy -diff) || exit 1; \
	done

test:
	@for m in $(MODULES); do \
		echo "Testing $$m"; \
		(cd $$m && go test -race ./...) || exit 1; \
	done
