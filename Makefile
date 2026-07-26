.PHONY: build-web build-go build clean dev-web dev-go

build-web:
	cd web && pnpm run build

build-go:
	CGO_ENABLED=0 go build -ldflags="-w -s" -o farouter .

build: build-web build-go

clean:
	rm -rf web/dist farouter

dev-web:
	cd web && pnpm run dev

dev-go:
	go run .

dev: dev-go
