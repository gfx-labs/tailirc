BIN_OUT := ./tailirc
INSTALL_DIR := $(HOME)/.local/bin
GO_SRC := $(shell find pkg cmd -name '*.go')

.PHONY: build install clean

build: $(BIN_OUT)

$(BIN_OUT): $(GO_SRC) go.mod go.sum
	go build -o $(BIN_OUT) ./cmd/tailirc

install: $(BIN_OUT)
	install -D -m 0755 $(BIN_OUT) $(INSTALL_DIR)/tailirc

clean:
	rm -f $(BIN_OUT)
