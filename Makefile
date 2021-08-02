PLUGIN_NAME=packer-post-processor-vsphere-cleanup
GOOPTS_AMD := GOARCH=amd64 CGO_ENABLED=0
GOOPTS_ARM := GOARCH=arm64 CGO_ENABLED=0

build: crosscompile

linux-amd: modules bin generate
	$(GOOPTS_AMD) GOOS=linux   go build -a -ldflags="-s -w" -installsuffix cgo -o bin/${PLUGIN_NAME}.linux-amd64 .

linux-arm: modules bin generate
	$(GOOPTS_ARM) GOOS=linux   go build -a -ldflags="-s -w" -installsuffix cgo -o bin/${PLUGIN_NAME}.linux-arm64 .

windows: modules bin generate
	$(GOOPTS_AMD) GOOS=windows go build -a -ldflags="-s -w" -installsuffix cgo -o bin/${PLUGIN_NAME}.windows-amd64 .

macos: modules bin generate
	$(GOOPTS_AMD) GOOS=darwin  go build -a -ldflags="-s -w" -installsuffix cgo -o bin/${PLUGIN_NAME}.macos-amd64 .

crosscompile: linux-amd linux-arm windows macos

modules:
	go mod download

tools:
	go install github.com/hashicorp/packer-plugin-sdk/cmd/packer-sdc@latest

generate: tools modules
	go generate ./...

bin:
	mkdir -p bin
	rm -f bin/*

.PHONY: bin
