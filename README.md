# my-switch-manager

my-switch-manager 帮助 Windows 和 Linux 用户识别本地 Switch 游戏文件，比对版本与 DLC，并按中文名整理可撤销的游戏库。

[![Build and release](https://github.com/hellomrli/my-switch-manager/actions/workflows/build.yml/badge.svg)](https://github.com/hellomrli/my-switch-manager/actions/workflows/build.yml)

[下载](https://github.com/hellomrli/my-switch-manager/releases) · [文档](docs/index.md) · [版本记录](CHANGELOG.md)

## 扫描第一个游戏库

1. 从 Releases 下载 Windows amd64 便携 ZIP，完整解压并双击 `switchmgr.exe`。浏览器会打开本机界面。
2. 打开“工具 → 元数据库 → 下载 / 更新”，获取 [blawar/titledb](https://github.com/blawar/titledb) 的公开第三方元数据。
3. 在“游戏库”选择目录并扫描；网络目录可填 `\\NAS\模拟器\Switch`。
4. 查看版本、DLC 和未识别项目，先用少量文件的副本生成整理预览，再决定执行。

Linux 用户选择对应 amd64/arm64 二进制，在下载目录执行：

```bash
chmod +x switchmgr-linux-amd64
./switchmgr-linux-amd64 serve
```

浏览器访问 `http://127.0.0.1:8765`。默认数据保存在程序所在目录，不可写时回退到用户配置目录。NAS、Docker 和旧版迁移见 [部署指南](docs/deployment.md)。

## 管理已有文件

| 任务 | 行为 |
|---|---|
| 浏览和识别 | 封面、列表、中文名、TitleID 搜索，按来源合并识别结果 |
| 比对版本与 DLC | 根据元数据库显示可更新、缺本体、缺 DLC、版本未知等状态 |
| 整理与撤销 | 先生成方案，再移动或重命名；逐步记录撤销日志 |
| 查找冗余 | 预览旧版更新、重复包和已合并源文件，执行后移入 `_redundant/` |
| 媒体同步 | 同步 ES-DE / EmulationStation 的 `gamelist.xml` 和封面路径 |
| 合并与导出 | NSP 合并、XCI 容器归档，以及库清单、待补清单、中文名 CSV |

程序不下载游戏内容，也不提供密钥。元数据库并非 Nintendo 官方数据库，其完整性和更新速度会影响比对结果。NSZ/XCZ 能识别和整理，但不能直接合并或转换。

## 查阅详细操作

- [部署、数据目录和旧版迁移](docs/deployment.md)
- [整理、媒体同步、冗余与撤销](docs/organizing.md)
- [识别来源、版本号和容器限制](docs/identification.md)
- [命令行操作](docs/cli.md)
- [开发、测试和发布](docs/development.md)

## 反馈问题

在 [Issues](https://github.com/hellomrli/my-switch-manager/issues) 提供版本、平台、操作日志和复现步骤。保留相关 `_undo_*.json`；不要上传游戏内容或真实密钥。

## 许可信息

仓库尚未提供根目录 LICENSE。再分发或复用代码前需确认授权；依赖使用各自的许可证。
