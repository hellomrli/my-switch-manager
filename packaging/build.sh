#!/usr/bin/env bash
# 交叉编译发布包：Windows（带图标、双击不弹黑框）/ Linux amd64 / Linux arm64
#
#   packaging/build.sh [版本号]      产物在 dist/<版本号>/，已有版本不覆盖
#
# 纯 Go、CGO_ENABLED=0，无需外部链接器；发布需要 Node.js、Python 3、go-winres。
set -euo pipefail
cd "$(dirname "$0")/.."

VERSION="${1-$(date +%Y.%m.%d)}"
export PATH="$HOME/.local/go/bin:$HOME/go/bin:$PATH"
export CGO_ENABLED=0
if [[ ! "$VERSION" =~ ^[0-9]{1,5}\.[0-9]{1,5}\.[0-9]{1,5}(\.[0-9]{1,5})?$ ]]; then
	printf '版本号必须是 a.b.c 或 a.b.c.d 数字形式：%s\n' "$VERSION" >&2
	exit 1
fi
IFS=. read -r -a parts <<< "$VERSION"
for part in "${parts[@]}"; do
	if (( 10#$part > 65535 )); then
		printf '版本号每一段不能超过 65535：%s\n' "$VERSION" >&2
		exit 1
	fi
done
WINVER="$VERSION"
if (( ${#parts[@]} == 3 )); then WINVER+=.0; fi
LDFLAGS="-s -w -X main.version=$VERSION"
DEST="dist/$VERSION"
if [[ -e "$DEST" || -L "$DEST" ]]; then
	printf '发布目录已存在，拒绝覆盖：%s\n' "$DEST" >&2
	exit 1
fi
for tool in go node python3 go-winres sha256sum; do
	if ! command -v "$tool" >/dev/null 2>&1; then
		printf '发布需要 %s，请先安装并加入 PATH\n' "$tool" >&2
		exit 1
	fi
done

go vet ./...
go test ./... -count=1
node --check internal/server/web/app.js
node --test internal/server/preview.test.js

# 所有产物先落在本次暂存目录，失败只清理本次文件，不触碰旧版本。
mkdir -p dist
OUT=$(mktemp -d "dist/.build-$VERSION-XXXXXX")
trap 'rm -rf -- "$OUT"' EXIT

# 必须重新生成资源，不能在缺少工具或生成失败时沿用旧版 .syso。
(cd packaging/winres && go-winres make --arch amd64 --in winres.json \
	--out ../../cmd/switchmgr/rsrc --product-version "$WINVER" --file-version "$WINVER")

build() { # GOOS GOARCH 输出名 [额外 ldflags]
	printf '构建 %s/%s → %s/%s\n' "$1" "$2" "$OUT" "$3"
	GOOS=$1 GOARCH=$2 go build -trimpath -ldflags "$LDFLAGS ${4:-}" -o "$OUT/$3" ./cmd/switchmgr
}
build windows amd64 switchmgr.exe "-H windowsgui"
build linux amd64 switchmgr-linux-amd64
build linux arm64 switchmgr-linux-arm64

# Windows 绿色包：exe + 说明，解压即用（数据默认存在 exe 同目录）。
PKG="my-switch-manager-$VERSION-windows-amd64"
mkdir "$OUT/$PKG"
cp "$OUT/switchmgr.exe" README.md "$OUT/$PKG/"
(cd "$OUT" && python3 -m zipfile -c "$PKG.zip" "$PKG" && python3 -m zipfile -t "$PKG.zip")
rm -rf -- "$OUT/$PKG"
(cd "$OUT" && sha256sum switchmgr.exe switchmgr-linux-amd64 switchmgr-linux-arm64 "$PKG.zip" > SHA256SUMS \
	&& sha256sum -c SHA256SUMS)

# -T 禁止误移进已有目录，-n 禁止覆盖并发构建刚发布的同一版本。
mv -Tn -- "$OUT" "$DEST"
if [[ -d "$OUT" ]]; then
	printf '发布目录已存在，拒绝覆盖：%s\n' "$DEST" >&2
	exit 1
fi
trap - EXIT
ls -lh "$DEST"
