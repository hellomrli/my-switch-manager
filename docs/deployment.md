# 部署并迁移数据

[返回文档入口](index.md)

## 运行桌面版

解压 Windows 便携包后运行 `switchmgr.exe`。程序启动本机服务并打开浏览器，重复启动会打开已有实例；可在界面左下角退出。

首次下载元数据库后选择游戏库目录。程序保存目录并在下次启动载入；扫描缓存只重读有变化的文件。设置中换目录也会重新扫描。

## 运行 Linux 服务

下载匹配 CPU 架构的二进制，赋予执行权限。先在本机运行 `serve` 并访问 `http://127.0.0.1:8765`。

需要局域网访问时，用环境变量提供自己设置的密码：

```bash
read -r -s -p '访问密码: ' SWITCHMGR_PASSWORD
export SWITCHMGR_PASSWORD
./switchmgr-linux-amd64 serve --listen 0.0.0.0:8765 --data ./switchmgr-data
```

浏览器使用 HTTP Basic 认证，用户名任意。不要把该 HTTP 服务直接暴露到公网；远程访问可通过自己的 HTTPS 反向代理。非回环监听没有密码时程序只会警告，不会拒绝启动。

[systemd 示例](../packaging/switchmgr.service) 需要创建服务用户，安装二进制到 `/usr/local/bin/switchmgr`，并调整服务用户、游戏目录权限和 `SWITCHMGR_PASSWORD`。示例里的 `change-me` 必须替换后再启动。

## 运行 Docker

克隆仓库后，先修改 [packaging/docker-compose.yml](../packaging/docker-compose.yml)：

- 将 `SWITCHMGR_PASSWORD=change-me` 改成自己的密码。
- 将 `user: "99:100"` 调整为有权操作游戏目录的 UID/GID。
- 将宿主机游戏路径改成自己的目录，映射到 `/games`。
- 确认数据卷的位置和写入权限；示例相对目录 `./data` 位于 compose 文件目录下。

然后在仓库根目录执行：

```bash
docker compose -f packaging/docker-compose.yml up -d --build
```

浏览器访问 NAS 地址的 8765 端口，界面的游戏库目录填写 `/games`。[Dockerfile](../packaging/Dockerfile) 内嵌应用及网页界面，运行时需要联网的主要操作是元数据库下载。

## 迁移旧版 SwitchRomMgr

使用当前程序执行：

```powershell
.\switchmgr.exe migrate "D:\SwitchRomMgr"
```

导入 `custom_names.json` 和 `manual_ids.json`，已有条目不覆盖。旧 `titledb` 可在设置里直接指定，无需再次下载。旧整理生成的 `_before_*.txt` 也能供媒体同步使用。

## 查找和备份数据

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

备份数据目录及游戏库根目录的撤销日志。默认监听回环地址；HTTP 写操作还检查请求头和 Host。网络访问时仍需正确设置密码及访问边界。
