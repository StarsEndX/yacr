BIN := bin/yacr

.PHONY: help build test vet lint clean install _require-destdir

help:
	@printf '可用目标:\n'
	@printf '  build              构建二进制到 %s\n' "$(BIN)"
	@printf '  test               运行测试 go test ./...\n'
	@printf '  vet                静态检查 go vet ./...\n'
	@printf '  lint               vet 别名\n'
	@printf '  install DESTDIR=…  安装到指定目录（必填；目录须在 PATH，否则 MCP 配置需绝对路径）\n'
	@printf '  clean              清理 bin/\n'

build:
	go build -o $(BIN) ./cmd/yacr

test:
	go test ./...

vet:
	go vet ./...

lint: vet

install: _require-destdir build
	install -d "$(DESTDIR)"
	install -m 0755 "$(BIN)" "$(DESTDIR)/yacr"
	@printf '已安装到 %s/yacr\n' "$(DESTDIR)"

_require-destdir:
	@test -n "$(DESTDIR)" || { printf 'error: 需指定安装目录，例如 make install DESTDIR=$$HOME/.local/bin\n' >&2; exit 1; }

clean:
	rm -rf bin
