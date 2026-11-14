# 使用命令行处理游戏库

[返回文档入口](index.md)

执行 `switchmgr help` 查看当前二进制的命令说明；不要无参数启动来获取帮助，无参数会启动桌面服务。命令定义以 [程序入口](../cmd/switchmgr/main.go) 的 `usage()` 为准。

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

先对目录执行 `scan`，数据库准备好后执行 `check`。只有确认预览后才给整理、冗余或撤销命令加 `--apply`。`check --todo` 会写 CSV，`fetch-db` 下载并写数据库，`migrate` 写用户数据；这些也有磁盘或网络副作用。
