# my-switch-manager

Switch 游戏库管理器：**读取游戏文件 → 查官方元数据库 → 比对版本 / DLC → 按中文名整理归档**。

[下载最新版](https://github.com/hellomrli/my-switch-manager/releases/latest) · [所有版本](https://github.com/hellomrli/my-switch-manager/releases) · [云编译状态](https://github.com/hellomrli/my-switch-manager/actions/workflows/build.yml)

由旧版 `SwitchRomMgr`（walk 写的 Windows 窗口程序）重写而来：界面改为内嵌网页，Windows 双击即用，也能作为服务跑在 NAS / Docker 上，用手机或电脑浏览器访问。

**本工具不下载任何游戏内容**，只读取你已有的文件和公开的元数据库（[blawar/titledb](https://github.com/blawar/titledb)）。

---

## 功能

| | |
|---|---|
| **游戏库** | 封面网格 / 列表两种视图，按「可更新 / 缺 DLC / 已最新 / 未识别 / 缺本体 / 冗余文件」筛选，搜索中文名、TitleID、文件名；启动时自动载入上次的游戏库 |
| **详情** | 简介、截图、发行商、语言；补丁历史（标出本地版本）；DLC 清单（逐个标出已有 / 缺失） |
| **识别** | 五种来源按可信度合并，无票据的合集包也能认出来（见下文） |
| **整理** | 按中文名重命名文件夹与文件，先出方案、逐条勾选、再执行；全程写撤销日志，可一键还原 |
| **清理冗余** | 找出旧版更新、重复文件、已合并进大包的源文件，勾选后移到 `_redundant/`，不删除、可撤销 |
| **媒体同步** | 改名后同步修正 ES-DE / EmulationStation 的 `gamelist.xml` 与 `images/` 封面名 |
| **合并 NSP** | 本体 + 更新 + DLC 拼成单个 NSP，纯容器层操作，逐文件 SHA-256 校验，不删源文件 |
| **XCI → NSP** | 提取卡带 secure 分区打成 PFS0（仅作归档，见限制） |
| **导出** | 待补清单 / 完整库清单 / 中文名对照表（CSV，Excel 直接打开）；中文名表改完可导回 |

## 快速开始

### Windows

1. 解压 `my-switch-manager-*-windows-amd64.zip`，双击 `switchmgr.exe`，浏览器会自动打开界面。
2. 首次使用：**工具 → 元数据库 → 下载 / 更新**（约 100 MB，国内会自动回退到镜像源）。
3. **游戏库 → 选择游戏库目录 → 扫描**。网络路径可以直接填 `\\NAS\模拟器\Switch`。

之后每次启动会自动载入上次的游戏库（有扫描缓存，只重读有变化的文件）；在设置里换了游戏库目录也会自动重新扫描。

设置、自定义中文名、扫描缓存默认存在 exe 同目录（绿色便携）；该目录不可写时改存 `%AppData%\my-switch-manager`。
再次双击会直接打开已在运行的实例；界面左下角可退出程序。

### NAS / Docker

```bash
# 在项目根目录
docker compose -f packaging/docker-compose.yml up -d --build
```

先按需修改 `packaging/docker-compose.yml`：

- `SWITCHMGR_PASSWORD`：**局域网访问务必设置**，整理功能会改名你的文件。浏览器会弹出登录框，用户名任意。
- `user`：容器以此身份改名文件，要和游戏目录的属主一致（Unraid 一般是 `99:100`）。
- 游戏目录挂到 `/games`，界面里「游戏库目录」填 `/games`。

不用 Docker 时，Linux 上可以直接运行二进制，或参考 `packaging/switchmgr.service` 装成 systemd 服务：

```bash
SWITCHMGR_PASSWORD=xxx ./switchmgr-linux-amd64 serve --listen 0.0.0.0:8765 --data /var/lib/switchmgr
```

### 从旧版 SwitchRomMgr 迁移

```bash
switchmgr migrate "D:\SwitchRomMgr"
```

导入旧版的 `custom_names.json`（自定义中文名）与 `manual_ids.json`（手动指定的 TitleID），已有条目不覆盖。
旧目录里的 `titledb` 可以直接在设置里指定为元数据库目录，不必重新下载。
旧版整理时写下的 `_before_*.txt` 清单也能被「媒体同步」读取。

---

## 识别原理

每个文件按以下来源识别，可信度从高到低，结果合并：

| 来源 | 需要什么 | 能得到什么 |
|---|---|---|
| **手动** | 在界面里「手动匹配游戏」 | TitleID（用户说了算，优先级最高） |
| **CNMT 解密** | `prod.keys`（可选） | 精确的 TitleID、版本号、包含哪些内容 |
| **NCA 比对** | 元数据库的 `cnmts.json` | 精确的 TitleID 与版本。**不需要密钥**：NCA 的文件名就是内容 ID，与库中记录对得上即可 |
| **票据** | 无 | `.tik` / `.cert` 文件名里的 TitleID |
| **文件名** | 无 | 文件名里的 16 位 TitleID，以及 `[B+U917504+6DLC]`、`[v65536]` 这类标注 |

旧版只能靠票据和文件名，所以 XCI 转来的、没有票据的合集包（如 `ゼノブレイド２ [B+U917504+6DLC].nsp`）只能手动指定。
现在 NCA 比对能直接认出它是「异度神剑2 本体 + 更新 917504 + 6 个 DLC」，而且精确到是哪 6 个。

仍然认不出的文件（例如 NCA 被二次处理过、文件名也没有 ID），在详情里点「手动匹配游戏」，按中文名或 TitleID 搜索指定即可。结果存在数据目录的 `manual_ids.json`。

**关于 prod.keys**：本工具不附带、也不生成任何密钥。有 NCA 比对后，密钥对大多数人已经不是必需的。

**关于版本号**：`v65536` 表示第 1 个补丁（高 16 位是补丁序号），它与游戏里显示的 `1.0.1` 这类版本没有换算关系。界面上显示为「v917504（补丁 #14）」。

---

## 整理

**整理 → 生成整理方案**。生成方案不会改动任何文件；逐条确认后再执行。

默认命名（可在设置里改模板）：

```
文件夹：[{name}][{title}][{tid}]
文件：  {title}[{tid}][{kind}]{ver}{dlc}

Zenobureido 2/ゼノブレイド２ [B+U917504+6DLC].nsp
  → [异度神剑2][ゼノブレイド２][0100F3400332C000]/ゼノブレイド２[0100F3400332C000][APP][v917504][+6DLC].nsp

The Legend of Zelda Skyward Sword HD/…[01002DA013484800][v65536](switch520.com).nsp
  → [萨尔达传说 御天之剑 HD][The Legend of Zelda Skyward Sword HD][01002DA013484000]/
      The Legend of Zelda Skyward Sword HD[01002DA013484800][UPD][v65536].nsp
```

| 标记 | 含义 |
|---|---|
| `{name}` | 中文名（自定义 > 库中名 > 文件名猜测） |
| `{title}` | 从原文件名猜出的原名（去掉 `[]` `()` 标记） |
| `{tid}` | 文件夹用本体 TitleID；文件用该文件自己的主 TitleID。**文件名模板必须包含** |
| `{kind}` | `APP` / `UPD` / `DLC`（含本体的合集包记为 APP） |
| `{ver}` | `[v917504]`，版本未知或为 0 时为空 |
| `{dlc}` | `[+6DLC]`，不含 DLC 时为空 |

`{ver}` 和 `{dlc}` 会在下次扫描时被读回。旧版整理后会丢掉 `B+U917504` 这类信息，之后再检查只能显示「版本未知」，新版不会。

**安全规则**

- 只整理库中有中文名的游戏，其余保持原样。
- 目标必须在库根目录之内，**绝不改库根目录本身的名字**（旧版曾因此让 Unraid shfs 崩溃）；散放在根目录的 ROM 会新建子文件夹。
- 目标已存在一律拒绝，绝不覆盖。
- `cheats` 等子目录随游戏文件夹一起移动。
- 执行前在库根写 `_before_*.txt` 清单，执行中每一步实时写 `_undo_*.json`。中途断电或出错也能从撤销日志还原。

还原：**整理 → 撤销记录**，或命令行 `switchmgr undo _undo_xxx.json --apply`。

### 清理冗余文件

**工具 → 清理冗余文件**。同一游戏里，一个文件的每个 Title 都能在另一个文件里找到同样或更新的版本，这个文件就是冗余的：

| 情况 | 例子 |
|---|---|
| 旧版更新 | `UPD v65536` 与 `UPD v131072` 并存，前者冗余 |
| 重复文件 | 同一本体既有 NSP 又有 XCI / NSZ，保留更省空间的那个 |
| 合并后的源 | 本体 + 更新 + DLC 已合并成一个大包，原来的散件冗余 |

- 只判断**内容清单精确且完整**的文件（CNMT 解密或 NCA 比对得出）。靠文件名或票据识别的文件可能漏掉内容，一律不参与，宁可漏报也不误报。
- 冗余文件不删除，只移到库根下的 `_redundant/`（保留原相对路径），扫描与整理都会跳过这个目录。确认无误后再自己删掉。
- 同样写 `_undo_*.json`，可在「撤销记录」里一键移回。

---

## 命令行

`organize` / `undo` / `redundant` 默认只预览，加 `--apply` 才会改动文件。`merge` / `xci2nsp` 只生成新文件、不碰源文件，直接执行。

```
switchmgr                           启动界面并打开浏览器（桌面模式）
switchmgr serve [--listen 地址] [--password 密码] [--no-browser]
switchmgr scan <目录>               列出每个文件的识别结果
switchmgr check <目录> [--todo 待补清单.csv]
switchmgr organize <目录> [--apply] [--media keep|into-folder]
switchmgr undo <_undo_*.json> [--apply]
switchmgr redundant <目录> [--apply]  列出冗余文件；--apply 移到 _redundant/
switchmgr findid <关键字>            在数据库里搜 TitleID
switchmgr merge <输入1> <输入2> [...] --out <输出.nsp>
switchmgr xci2nsp <输入.xci> [--out 输出.nsp]
switchmgr fetch-db                  下载 / 更新元数据库
switchmgr migrate <旧版目录>

通用选项：--data 数据目录  --db 元数据库目录  --keys prod.keys
```

Windows 版从命令行运行子命令时会把输出挂到当前控制台。

## 数据目录

默认为程序所在目录；可用 `--data` 或环境变量 `SWITCHMGR_DATA` 指定。

| 文件 | 内容 |
|---|---|
| `config.json` | 设置 |
| `custom_names.json` | 自定义中文名（TitleID → 名称） |
| `manual_ids.json` | 手动指定（文件名 → TitleID） |
| `scan_cache.json` | 扫描缓存：只存文件本身的事实（NCA 列表、票据等），元数据库更新后无需重读文件 |
| `titledb/` | 元数据库（默认位置，可在设置里改） |
| `switchmgr.log` | 桌面模式的运行日志 |

撤销日志 `_undo_*.json`、整理清单 `_before_*.txt` 与冗余文件目录 `_redundant/` 都在**游戏库根目录**，跟着游戏走。

## 安全

- 默认只监听 `127.0.0.1`，并校验 `Host` 头，防止 DNS 重绑定攻击。
- 监听非回环地址时，未设置密码会在日志里警告。
- 所有写操作要求 `X-Requested-With` 头，普通网页无法跨站提交。

---

## 限制

- **XCI → NSP**：卡带不含票据（`.tik`），产物是「NCA 容器」，能否安装取决于环境里是否已有对应票据。适合做容器格式归档。同源 NSP 之间的合并不受影响，本体与更新各自带着票据。
- **不提供 NSP → XCI**：生成 XCI 需要构造卡带头与签名，无法验证产物有效性，请用 4NXCI 等成熟工具。
- **NSZ / XCZ**：能识别和整理，但不能合并或转换（需要先解压）。

## 云编译与发布

[GitHub Actions](https://github.com/hellomrli/my-switch-manager/actions/workflows/build.yml) 在 main 推送、Pull Request 和手动触发时运行 Linux/Windows 的 Go 测试、静态检查、前端回归；Linux 额外运行竞态检测和发布脚本回归。全部通过后，复用本地构建脚本生成三平台产物，保留为 Actions 中的 `binaries` 附件（14 天）。非标签构建使用 `0.0.0`，仅用于测试，不创建 Release。

正式发布使用数字版本的 `v` 标签，例如下一版：

```bash
git push origin main                     # 先确认 main 的 CI 通过
git tag -a v0.4.2 -m "Release v0.4.2"
git push origin v0.4.2
```

标签工作流从源码重新测试和编译，以去掉 `v` 的版本号写入程序及 Windows 版本资源；云端校验 `SHA256SUMS` 后发布到 [Releases](https://github.com/hellomrli/my-switch-manager/releases)。发布任务使用 GitHub 自动提供的 token，不需要配置个人密钥，也不会上传本地 `dist/`。已有 Release 不覆盖；修复后使用新版本标签。

## 构建

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

### 0.4.1 测试版

- 共享 NCA 不能再凭随机索引确定版本；歧义或截断文件不参与自动冗余判断，畸形容器/CNMT 偏移明确报错。
- 文件操作约束在库根内；撤销日志采用唯一文件名、先记录意图再执行，保存失败即停止。撤销冲突可修复后重试，继续兼容 v1/v2 日志。
- 媒体仅按实际成功的移动更新引用；整理保留手动匹配关系，撤销后仍有效。
- 修复纯文件夹改名、仅 XML 修复的空列表预览；整理执行校验预览 token，配置/扫描变化后须重新生成方案；后台任务启动响应改为锁内快照。

Windows 包由 Linux 交叉编译；自动化测试和交叉编译不能代替 Windows 实机验证。建议先使用少量游戏的**副本**，按「扫描 → 生成整理方案 → 执行 → 媒体同步 → 撤销」验证，再测试冗余预览和手动匹配。界面有报错时保留对应 `_undo_*.json`，不要手工删除恢复记录。
