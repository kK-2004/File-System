# File System

一个使用 Go 实现的多用户、多级目录虚拟文件系统。项目通过一个本地磁盘文件保存目录、文件、用户和权限信息，命令行交互风格接近简化版 Linux shell。

在 Windows、macOS、Linux 上都可以用同一套命令构建和运行。

## 运行

```bash
./launch.sh
go run ./cmd/pfs init
go run ./cmd/pfs run
go run ./cmd/pfs
```

不带参数时默认进入 `run`。如果当前目录还没有 `fms.pfs`，程序会自动初始化磁盘后再进入登录流程。

如果希望一键启动，也可以直接运行：

```bash
./launch.sh
```

`launch.sh` 会直接调用 `go run ./cmd/pfs`，这样每次启动都会使用当前源码版本。

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
