PLUGIN_NAME=packer-post-processor-vsphere-cleanup

crosscompile:
	mkdir -p build
	CGO_ENABLED=0 GOARCH=amd64 GOOS=linux go build -a -ldflags="-s -w" -installsuffix cgo -o build/${PLUGIN_NAME}.linux-amd64 .
	CGO_ENABLED=0 GOARCH=arm64 GOOS=linux go build -a -ldflags="-s -w" -installsuffix cgo -o build/${PLUGIN_NAME}.linux-arm64 .
	CGO_ENABLED=0 GOARCH=amd64 GOOS=windows go build -a -ldflags="-s -w" -installsuffix cgo -o build/${PLUGIN_NAME}.windows-amd64 .
	CGO_ENABLED=0 GOARCH=amd64 GOOS=darwin go build -a -ldflags="-s -w" -installsuffix cgo -o build/${PLUGIN_NAME}.darwin-amd64 .

