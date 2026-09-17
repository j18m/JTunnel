# JTunnel

JTunnel 是一个基于 mTLS + yamux 的 TCP 隧道工具。客户端在本机启动 SOCKS5 代理，流量通过 Server 或多级 Relay 转发到目标地址；也可以不启动 Server，直接进行本机 TCP 端口转发。

## 架构

```text
应用程序 -> 本地 SOCKS5(Client) -> JTunnel 隧道 -> Server -> 目标服务
                                  \-> Relay -> ... -> Server
```

- `server`：隧道出口，负责连接最终目标地址。
- `client`：本地入口，启动 SOCKS5 代理并连接到 Server 或 Relay。
- `relay`：中继节点，接收上游连接并转发到下一个 Server 或 Relay。

## 构建

```bash
go build -o jtunnel .
```

交叉编译示例：

```bash
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o jtunnel-linux-amd64 .
CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build -o jtunnel-darwin-amd64 .
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -o jtunnel-windows-amd64.exe .
```

## 快速开始

下面的示例会在客户端开启一个仅本机可访问的 SOCKS5 代理，并通过加密隧道从服务端访问目标地址。

启动出口服务器：

```bash
./jtunnel server -l :23336
```

启动客户端，本地 SOCKS5 默认监听 `127.0.0.1:1080`：

```bash
./jtunnel client 服务器IP:23336
```

指定本地 SOCKS5 监听地址：

```bash
./jtunnel client 服务器IP:23336 -l 127.0.0.1:1080
```

`--socks5-listen` 是 `--listen` 的同义参数，适合在脚本中明确表示这是 SOCKS5 监听地址：

```bash
./jtunnel client 服务器IP:23336 --socks5-listen 0.0.0.0:1080
```

使用 SOCKS5 用户名密码认证：

```bash
./jtunnel client 服务器IP:23336 -l 127.0.0.1:1080 -u user -p pass
```

测试代理。推荐使用 `socks5h`，这样域名也会通过隧道交给服务端解析：

```bash
curl --proxy socks5h://127.0.0.1:1080 https://example.com
```

## 本地 SOCKS5 转发

运行 `client` 命令时会自动开启本地 SOCKS5 监听，不需要额外的开关。默认监听地址为 `127.0.0.1:1080`：

```text
本地应用 -> 127.0.0.1:1080 -> mTLS/yamux 隧道 -> Server -> 目标地址
```

支持的能力：

- SOCKS5 `CONNECT` 命令，即 TCP 转发。
- IPv4、IPv6 和域名目标地址。
- 免认证模式，以及用户名密码认证模式。
- 多个本地连接复用同一条 yamux 隧道。
- 经一个或多个 Relay 转发。

当前不支持 SOCKS5 `BIND` 和 `UDP ASSOCIATE`，因此不提供 UDP 转发。

### 配置应用程序

单次使用 `curl`：

```bash
curl --proxy socks5h://127.0.0.1:1080 https://example.com
```

为支持 `ALL_PROXY` 的命令设置代理：

```bash
export ALL_PROXY=socks5h://127.0.0.1:1080
```

启用认证后：

```bash
./jtunnel client 服务器IP:23336 -u user -p pass
curl --proxy socks5h://user:pass@127.0.0.1:1080 https://example.com
```

用户名和密码必须同时提供。只设置其中一项时，客户端会拒绝启动。

### 监听范围

默认的 `127.0.0.1:1080` 只允许本机程序访问。若确实需要让局域网内的其他设备使用，可以监听所有网卡：

```bash
./jtunnel client 服务器IP:23336 --socks5-listen 0.0.0.0:1080 -u user -p pass
```

监听 `0.0.0.0` 会将代理暴露给网络中的其他设备，请同时配置认证和防火墙，避免形成公开代理。

## 直接 TCP 端口转发

推荐使用与 GOST 类似的 `-L` 服务 URL。它直接在当前机器监听 TCP 端口并连接目标地址，不需要启动 JTunnel Server，也不经过 mTLS/yamux 隧道：

```text
本地应用 -> 127.0.0.1:8080 -> 10.0.0.5:80
```

GOST 风格用法：

```bash
./jtunnel -L tcp://:8080/10.0.0.5:80
```

`-L` 可以重复指定，单个进程可同时运行多个转发服务：

```bash
./jtunnel \
  -L tcp://127.0.0.1:8080/10.0.0.5:80 \
  -L tcp://127.0.0.1:5432/db.internal:5432
```

为兼容 GOST 语义，`:8080` 会监听所有可用网卡；若仅供本机使用，请显式写 `127.0.0.1:8080`。目标支持 IPv4、域名和带方括号的 IPv6：

```bash
./jtunnel -L 'tcp://[::1]:8080/[2001:db8::1]:80'
```

也保留了更直观的兼容子命令。只填写端口时，这种写法默认监听 `127.0.0.1`：

```bash
./jtunnel forward 8080 10.0.0.5:80
```

当前 `-L` 服务 URL 仅实现 `tcp://监听地址/目标地址`。直接转发不提供加密或身份认证，仅支持 TCP。监听 `:8080`、`0.0.0.0:8080` 或 `[::]:8080` 会向其他设备开放端口，请配合防火墙使用。

## 中继

单层中继：

```bash
# Server
./jtunnel server -l :23336

# Relay
./jtunnel relay -l :23337 服务器IP:23336

# Client
./jtunnel client 中继IP:23337 -l 127.0.0.1:1080
```

多层中继：

```bash
./jtunnel relay -l :23337 第二层中继IP:23338
./jtunnel relay -l :23338 服务器IP:23336
./jtunnel client 第一层中继IP:23337 -l 127.0.0.1:1080
```

## 命令

### GOST 风格直接转发

```bash
jtunnel -L tcp://<listen-address>/<target-address>
```

根命令参数：

| 参数 | 简写 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `--listen` | `-L` | 空 | 本地服务 URL，可重复指定 |
| `--debug` | `-D` | `false` | 输出直接转发的调试日志 |
| `--timeout` | `-t` | `10` | 连接目标的超时时间，单位秒 |

当前支持的服务 URL：`tcp://监听地址/目标地址`。现有子命令继续兼容。

### server

```bash
jtunnel server [listen-address]
```

常用参数：

| 参数 | 简写 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `--listen` | `-l` | `0.0.0.0:23336` | 服务端监听地址 |
| `--debug` | `-d` | `false` | 输出调试日志 |
| `--timeout` | `-t` | `10` | 网络操作超时时间，单位秒 |

兼容旧参数：`--bind/-b` 等价于 `--listen/-l`。

### client

```bash
jtunnel client <server-address>
```

常用参数：

| 参数 | 简写 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `--listen` | `-l` | `127.0.0.1:1080` | 本地 SOCKS5 监听地址 |
| `--socks5-listen` | — | `127.0.0.1:1080` | `--listen` 的同义参数 |
| `--username` | `-u` | 空 | SOCKS5 认证用户名 |
| `--password` | `-p` | 空 | SOCKS5 认证密码 |
| `--debug` | `-d` | `false` | 输出调试日志 |
| `--timeout` | `-t` | `10` | 网络操作超时时间，单位秒 |

兼容旧参数：`--server/-s` 仍可指定服务器地址；`--socks-port 1080` 等价于监听 `127.0.0.1:1080`。
认证模式仅在同时指定 `--username` 与 `--password` 时启用；两者都不指定时为免认证模式。

### forward

```bash
jtunnel forward <listen-port-or-address> <target-address>
```

常用参数：

| 参数 | 简写 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `--debug` | `-d` | `false` | 输出每个连接的调试日志 |
| `--timeout` | `-t` | `10` | 连接目标的超时时间，单位秒 |

`listen-port-or-address` 可以只写端口（默认绑定 `127.0.0.1`），也可以写完整的 `host:port`。

### relay

```bash
jtunnel relay [flags] <next-address>
```

常用参数：

| 参数 | 简写 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `--listen` | `-l` | `0.0.0.0:23337` | 中继监听地址 |
| `--next` | `-n` | 空 | 下一个 Server 或 Relay 地址 |
| `--debug` | `-d` | `false` | 输出调试日志 |
| `--timeout` | `-t` | `10` | 网络操作超时时间，单位秒 |

## 安全说明

当前版本的证书硬编码在 `config/config.go` 中，适合测试和私有环境快速使用。生产环境建议改为从文件或环境变量加载证书，并为每个部署生成独立 CA、服务端证书和客户端证书。

客户端和中继连接下游节点时会校验证书链，但为了兼容当前内置证书，不校验证书主机名。

## 测试

运行全部测试：

```bash
go test ./...
```

检查双向转发中的数据竞争：

```bash
go test -race ./...
```

## 故障排除

- 端口占用：使用 `lsof -i :1080` 或 `lsof -i :23336` 找到占用进程。
- 证书验证失败：确认所有节点使用同一套证书链。
- 连接目标失败：用 `--debug` 查看目标地址、端口和拨号错误。
- 代理无响应：确认客户端连接的是 Server 或第一层 Relay，而不是最终目标服务地址。
- `curl` 能连接 IP 但域名失败：使用 `socks5h://`，让服务端通过隧道解析域名。
- 提示用户名和密码必须同时指定：同时提供 `--username` 和 `--password`，或同时移除两项以使用免认证模式。
