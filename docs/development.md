# 开发、检查和发布

[返回文档入口](index.md)

## 构建与检查

本机编译需要 Go 1.26+；完整发布还需要 Node.js 18+、Python 3、GNU coreutils（`sha256sum` / `mv -Tn`）以及 `go-winres`，推荐在 Linux 上运行构建脚本。

```bash
go build -o switchmgr ./cmd/switchmgr     # 本机
go install github.com/tc-hib/go-winres@latest
packaging/build.sh 0.4.1                 # Windows（带图标）/ Linux amd64 / arm64
```

发布产物在 `dist/<版本号>/`，包括 `switchmgr.exe`、Windows 便携 ZIP、两个 Linux 二进制和 `SHA256SUMS`。脚本先运行 Go 与前端检查，再在临时目录构建；全部成功后才发布。**保留旧版本，同名版本目录已存在时拒绝覆盖**。版本号支持 `a.b.c` / `a.b.c.d`，各段不超过 65535；省略时使用当天日期。

纯 Go、`CGO_ENABLED=0` 静态编译，界面文件与繁简转换词典都内嵌在二进制里，运行时无外部依赖。

```
cmd/switchmgr/        程序入口：CLI 子命令、桌面模式、服务模式
internal/nsw/         读取游戏文件：PFS0 / HFS0 / XCI 容器、NCA 头与 CNMT 解密、TitleID 规则
internal/titledb/     元数据库：加载、NCA 索引、搜索、下载、繁转简
internal/library/     业务层：扫描识别、版本比对、整理与撤销、媒体同步、合并
internal/server/      HTTP API、后台任务队列、内嵌网页界面（web/，原生 JS，无 CDN 依赖）
packaging/            构建脚本、Dockerfile、compose、systemd、Windows 资源
```

### 测试

```bash
go test ./... -count=1
go vet ./...
go test -race ./...                      # 需本机 C 工具链及 CGO 支持
node --check internal/server/web/app.js
node --test internal/server/preview.test.js
python3 packaging/build_test.py          # 临时目录 + 假编译器，验证发布失败保护
```

测试使用临时合成游戏库，不需要真实 ROM。真实 CNMT 解密和元数据库样本测试默认跳过，分别通过 `SWITCHMGR_TEST_KEYS` / `SWITCHMGR_TEST_CNMT`、`SWITCHMGR_TEST_DB` 启用（见 `internal/nsw/nca_test.go`、`internal/titledb/db_test.go`）。权限及符号链接用例在平台不支持时会明确跳过。

## 使用云构建

[build.yml](../.github/workflows/build.yml) 在 main 推送、Pull Request 和手动运行时执行 Linux/Windows 检查，Linux 还运行竞态及发布脚本回归。通过后生成 Windows amd64、Linux amd64/arm64 产物，保留为 `binaries` artifact 14 天。

非标签构建写入版本 `0.0.0`，不创建 Release。维护者推送尚未使用的数字 `v` 标签后，工作流重新测试并编译，校验 `SHA256SUMS`，再发布 Release。已有 Release 不覆盖，修复需使用新版本标签。

Windows 包由 Linux 交叉编译；CI 不能代替 Windows 实机验证。发布前用少量文件副本完成扫描、整理预览、执行、媒体同步、撤销和冗余预览。
