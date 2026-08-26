BINARY_NAME := terraform-provider-atlassian
VERSION ?= dev

.PHONY: build fmt generate test testacc

build:
	go build -ldflags "-X main.version=$(VERSION)" -o bin/$(BINARY_NAME) .

fmt:
	go fmt ./...

generate:
	go generate ./...

test:
	go test $(TESTARGS) $(if $(TEST),$(TEST),./...)

testacc:
	TF_ACC=1 go test $(TESTARGS) -timeout 120m
