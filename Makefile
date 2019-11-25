VERSION?=git-$(shell git rev-parse --abbrev-ref HEAD)-$(shell git rev-parse --short HEAD).$(shell date +%Y-%m-%d.%H-%M-%S)

.PHONY: all
all: build

.PHONY: build
build:
	go build -ldflags "-X main.version=$(VERSION)" -o bin/mysql-manticore ./cmd/mysql-manticore/

.PHONY: test
test:
	go test $(TEST_FLAGS) ./river/
