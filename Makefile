BINARY_NAME := terraform-provider-atlassian
VERSION ?= dev

.PHONY: build fmt generate generate/api-client generate/api-client/confluence generate/api-client/confluence-v1 generate/api-client/confluence-v2 generate/api-client/control generate/api-client/organization generate/docs lint release/check test testacc

build:
	go build -ldflags "-X main.version=$(VERSION)" -o bin/$(BINARY_NAME) .

fmt:
	go fmt ./...

generate: generate/api-client generate/docs

generate/api-client: generate/api-client/organization generate/api-client/control generate/api-client/confluence

generate/api-client/confluence: generate/api-client/confluence-v2 generate/api-client/confluence-v1

generate/api-client/organization:
	go run github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen -config api/admin/organization/oapi-codegen.yaml api/admin/organization/upstream.json

generate/api-client/control:
	go run github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen -config api/admin/control/oapi-codegen.yaml api/admin/control/upstream.json

generate/api-client/confluence-v2:
	go run github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen -config api/confluence/v2/oapi-codegen.yaml api/confluence/v2/upstream.json

generate/api-client/confluence-v1:
	go run github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen -config api/confluence/v1/oapi-codegen.yaml api/confluence/v1/upstream.json

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
