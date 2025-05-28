define go-module-check
	cd $(1) && go build -o /dev/null ./...
	cd $(1) && go vet ./...
	cd $(1) && gofmt -l .
	cd $(1) && go mod verify && go mod tidy -v
endef

define go-module-test
	cd $(1) && go test -p 1 -v -race -coverprofile=coverage.out ./...
endef

MODULES := chi echo fiber gin

check: $(addprefix check-,$(MODULES))
test:  $(addprefix test-,$(MODULES))

$(foreach m,$(MODULES),$(eval check-$(m): ; $(call go-module-check,$(m))))
$(foreach m,$(MODULES),$(eval test-$(m):  ; $(call go-module-test,$(m))))
