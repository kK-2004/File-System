# File System

一个使用 Go 实现的多用户、多级目录虚拟文件系统。项目通过一个本地磁盘文件保存目录、文件、用户和权限信息，命令行交互风格接近简化版 Linux shell。
<img width="1280" height="680" alt="PixPin_2026-05-26_22-05-18" src="https://github.com/user-attachments/assets/a31bba41-46fb-4edd-9794-23871d48b517" />

在 Windows、macOS、Linux 上都可以用同一套命令构建和运行。

## 运行

PowerShell:

```powershell
.\launch.ps1
go run ./cmd/pfs init
go run ./cmd/pfs run
go run ./cmd/pfs web
go run ./cmd/pfs
```

Git Bash、WSL、Linux 或 macOS:

```bash
./launch.sh
go run ./cmd/pfs init
go run ./cmd/pfs run
go run ./cmd/pfs web
go run ./cmd/pfs
```

不带参数时默认进入 `run`。如果当前目录还没有 `fms.pfs`，程序会自动初始化磁盘后再进入登录流程。

如果希望一键启动，PowerShell 下可以直接运行：

```powershell
.\launch.ps1
```

Git Bash、WSL、Linux 或 macOS 下可以直接运行：

```bash
./launch.sh
```

启动脚本会直接调用 `go run ./cmd/pfs`，这样每次启动都会使用当前源码版本。

如果要使用浏览器终端：

```powershell
go run ./cmd/pfs web
```

启动后访问：

```text
http://127.0.0.1:8080
```

浏览器终端使用 Vue3 + Element Plus 实现，后端使用 Gin。前端静态文件会嵌入到 Go 程序中，由同一个 Gin 服务托管，所以只需要启动后端并访问一个端口。终端支持命令历史、Tab 补全、vim 弹窗编辑和磁盘块空间侧边栏。后端通过 WebSocket 推送磁盘和用户变化事件，前端会实时刷新状态。默认登录账号仍然是 `root` / `root`。

初始化或启动时可以指定磁盘块参数：

```powershell
go run ./cmd/pfs init -block-size 64 -total-blocks 1024
go run ./cmd/pfs web -block-size 128 -total-blocks 2048
```

如果磁盘文件已经存在，`run` 或 `web` 启动时传入新的块参数会自动迁移磁盘：先按新参数生成新磁盘文件，迁移成功后切换到新文件并删除旧文件。Web 终端的磁盘块侧边栏也支持 root 用户在线调整参数。

如果要指定磁盘文件：

```bash
go run ./cmd/pfs init -disk ./data/fms.pfs
go run ./cmd/pfs run -disk ./data/fms.pfs
```

初始化后默认用户为：

```text
username: root
password: root
```

进入交互终端后输入 `main` 或 `help` 查看命令。

## 构建

```bash
go build -o pfs ./cmd/pfs
```

Windows 可以构建为：

```bash
go build -o pfs.exe ./cmd/pfs
```

## Docker

构建镜像：

```bash
docker build -t pfs:latest .
```

运行 Web 终端：

```bash
docker compose up -d --build
```

启动后访问：

```text
http://127.0.0.1:8086
```

容器默认执行：

```text
pfs web -addr 0.0.0.0:8086 -disk /data/fms.pfs
```

如果镜像已经推送到镜像仓库，可以把 `compose.yaml` 中的 `image` 改为仓库地址，并删除 `build`：

```yaml
services:
  pfs:
    image: your-registry.example.com/pfs:latest
    container_name: pfs
    restart: unless-stopped
    ports:
      - "8086:8086"
    volumes:
      - pfs-data:/data

volumes:
  pfs-data:
```

服务器部署：

```bash
docker compose pull
docker compose up -d
```

如果使用 GitHub Actions 自动部署 `docker` 分支，需要在仓库 Settings -> Secrets and variables -> Actions 中配置：

```text
PFS_SERVER_HOST      服务器 IP 或域名
PFS_SERVER_USER      SSH 用户名
PFS_SERVER_SSH_KEY   SSH 私钥
PFS_SERVER_PORT      SSH 端口，可选，默认 22
PFS_DEPLOY_PATH      服务器部署目录，可选，默认远程用户家目录下的 pfs
```

工作流会在 `docker` 分支有新提交时通过 SSH 进入服务器，拉取最新代码，并执行：

```bash
docker compose up -d --build
```

这种方式会直接在服务器上构建 `pfs:latest` 本地镜像，不需要再手动推送到镜像仓库。

## 支持命令

```text
useradd [name]          添加用户，仅 root 可用
su username             切换用户
passwd [username]       修改密码，root 可指定用户
pwd                     显示当前路径
clear | cls             清屏，自动兼容不同系统
mkdir name              新建目录
touch name              新建文件
vim name [content...]   写入文件内容
more name               查看文件内容
cd path                 切换目录，支持 /、..、~
cp src target_dir       复制文件
mv src target_dir       移动文件或目录
rename old new          重命名
tree [-d depth]         默认递归显示全部，`-d` 可限制层数
ls                      显示目录内容
ll                      显示目录详细信息
stat name               显示文件或目录元数据
detail                  显示虚拟磁盘概览
rm [-r] name            删除文件或目录
exit                    保存并退出
```

## 测试

```bash
go test ./...
```

## 目录结构

```text
.
├── cmd/pfs        # Go 命令行入口
├── internal/pfs   # 虚拟文件系统核心实现
├── docs           # 课程设计相关说明
├── go.mod
├── LICENSE
└── README.md
```
