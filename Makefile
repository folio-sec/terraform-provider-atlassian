BINARY_NAME := terraform-provider-atlassian
VERSION ?= dev

.PHONY: build fmt generate generate/api-client generate/docs lint release/check test testacc

build:
	go build -ldflags "-X main.version=$(VERSION)" -o bin/$(BINARY_NAME) .

fmt:
	go fmt ./...

generate: generate/api-client generate/docs

generate/api-client:
	go run github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen -config api/admin/organization/oapi-codegen.yaml api/admin/organization/upstream.json

generate/docs:
	go generate ./...

lint:
	aqua exec -- golangci-lint run

release/check:
	aqua exec -- goreleaser check

test:
	go test $(TESTARGS) $(if $(TEST),$(TEST),./...)

testacc:
	TF_ACC=1 go test $(TESTARGS) -timeout 120m
