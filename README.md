# JTunnel

## 使用说明

### 命令行参数

#### 服务器命令

```bash
jtunnel server [参数]
```

| 参数 | 简写 | 说明 | 默认值 |
|------|------|------|--------|
| --bind | -b | 服务器绑定地址 | 0.0.0.0:23336 |

#### 客户端命令

```bash
jtunnel client [参数]
```

| 参数 | 简写 | 说明 | 默认值 |
|------|------|------|--------|
| --server | -s | 服务器地址 | 必填 |
| --socks-port | -l | 本地SOCKS5代理端口 | 必填 |
| --username | -u | 认证用户名 | 空 |
| --password | -p | 认证密码 | 空 |

### 示例

#### 启动服务器

```bash
go run main.go server --bind 0.0.0.0:23336
```

或使用编译后的二进制文件：

```bash
./jtunnel server --bind 0.0.0.0:23336
```

#### 启动客户端

```bash
go run main.go client --server 127.0.0.1:23336 --socks-port 1080
```

或使用编译后的二进制文件：

```bash
./jtunnel client --server 127.0.0.1:23336 --socks-port 1080
```

#### 带认证的客户端连接

```bash
./jtunnel client --server 192.168.1.100:23336 --socks-port 1080 --username user --password pass
```
