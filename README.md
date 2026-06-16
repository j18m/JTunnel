# JTunnel

JTunnel 是一个基于 mTLS + yamux 的 TCP 隧道工具。客户端在本机启动 SOCKS5 代理，流量通过 Server 或多级 Relay 转发到目标地址。

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

使用 SOCKS5 用户名密码认证：

```bash
./jtunnel client 服务器IP:23336 -l 127.0.0.1:1080 -u user -p pass
```

测试代理：

```bash
curl --socks5 127.0.0.1:1080 https://example.com
```

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
| `--username` | `-u` | 空 | SOCKS5 认证用户名 |
| `--password` | `-p` | 空 | SOCKS5 认证密码 |
| `--debug` | `-d` | `false` | 输出调试日志 |
| `--timeout` | `-t` | `10` | 网络操作超时时间，单位秒 |

兼容旧参数：`--server/-s` 仍可指定服务器地址，`--socks-port` 仍可指定本地端口。

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

## 故障排除

- 端口占用：使用 `lsof -i :1080` 或 `lsof -i :23336` 找到占用进程。
- 证书验证失败：确认所有节点使用同一套证书链。
- 连接目标失败：用 `--debug` 查看目标地址、端口和拨号错误。
- 代理无响应：确认客户端连接的是 Server 或第一层 Relay，而不是最终目标服务地址。
