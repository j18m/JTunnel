# JTunnel

## 使用说明

### 架构说明

JTunnel采用多层隧道架构，支持Server ↔ Relay ↔ Client的双向数据传输：

```
+----------------+       +----------------+       +----------------+
|                |       |                |       |                |
|   客户端应用    | <-->  |   SOCKS5代理   | <-->  |                |
|                |       |   (客户端)     |       |                |
+----------------+       +----------------+       |                |
                                                 |   JTunnel隧道   |
+----------------+       +----------------+       |  (Server ↔ Relay|
|                |       |                |       |       ↔ Client)| 
|   目标服务器    | <-->  |   服务器节点    | <-->  |                |
|                |       |                |       |                |
+----------------+       +----------------+       +----------------+
```

- **服务器节点(Server)**：接收来自中继或客户端的连接，处理目标服务器的连接请求
- **中继节点(Relay)**：转发流量，支持多层级联，扩展隧道覆盖范围
- **客户端(Client)**：提供本地SOCKS5代理，将应用流量转发至JTunnel隧道

### mTLS认证

JTunnel使用mTLS(Mutual TLS)进行节点间身份验证，确保隧道通信安全：

1. **证书配置**：
   - 预配置的证书存放在`config`目录中
   - 包含客户端证书(`client.crt`)、客户端密钥(`client.key`)、服务器证书(`server.crt`)和CA证书(`ca.crt`)

2. **工作原理**：
   - 每个节点在建立连接时都会验证对方的证书
   - 只有持有有效证书的节点才能建立隧道连接

### yamux多路复用

JTunnel使用yamux协议进行连接多路复用，优化长连接管理：

#### 核心参数
- **ConnectionWriteTimeout**: 5分钟（写入超时时间）
- **KeepAliveInterval**: 30秒（心跳间隔，维持连接活跃）
- **Stream管理**: 每个数据流通过`yamux.Stream`处理

#### 使用注意事项
- 必须使用`Close()`方法关闭流，而不是`CloseWrite()`，以确保EOF正确传播
- 流关闭时会自动触发数据刷新和连接清理

### 命令行参数

#### 通用参数

| 参数 | 简写 | 说明 | 默认值 |
|------|------|------|--------|
| --debug | -d | 启用调试模式，输出详细日志 | false |
| --timeout | -t | 网络操作超时时间（秒） | 5 |

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

#### 中继服务器命令

```bash
jtunnel relay [参数]
```

| 参数 | 简写 | 说明 | 默认值 |
|------|------|------|--------|
| --listen | -l | 中继服务器监听地址 | 必填 |
| --next | -n | 下一个节点（服务器或中继）地址 | 必填 |

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

#### 启动中继服务器

##### 单层中继示例

```bash
# 中继服务器监听在0.0.0.0:23337，并转发到服务器192.168.1.100:23336
go run main.go relay --listen 0.0.0.0:23337 --next 192.168.1.100:23336
```

##### 多层中继示例

```bash
# 第一层中继：监听0.0.0.0:23337，转发到第二层中继192.168.1.101:23338
go run main.go relay --listen 0.0.0.0:23337 --next 192.168.1.101:23338

# 第二层中继：监听0.0.0.0:23338，转发到服务器192.168.1.102:23336
go run main.go relay --listen 0.0.0.0:23338 --next 192.168.1.102:23336

# 客户端连接到第一层中继
go run main.go client --server 192.168.1.100:23337 --socks-port 1080
```

## 故障排除

### 常见错误及解决方法

1. **端口占用错误**
   - 错误信息：`Only one usage of each socket address`
   - 解决方法：查找并关闭占用端口的进程
     ```bash
     # Windows
     netstat -ano | findstr :1080
     taskkill /F /PID <占用端口的PID>
     
     # Linux/Mac
     lsof -i :1080
     kill -9 <占用端口的PID>
     ```

2. **连接被强制关闭**
   - 错误信息：`An existing connection was forcibly closed by the remote host`
   - 说明：这通常是客户端应用（如curl、浏览器）在请求完成后正常关闭连接，不是真正的错误
   - 解决方法：无需处理，JTunnel已过滤此类日志

3. **无效的Web响应**
   - 错误信息：`从目标主机接收到的响应不象普通的Web服务器响应`或`目标主机响应 = 0`
   - 原因：数据流未正确关闭，导致EOF信号未传播
   - 解决方法：确保使用最新版本的JTunnel，内部已修复此问题

4. **证书验证失败**
   - 错误信息：`x509: certificate signed by unknown authority`
   - 解决方法：检查config目录下的证书是否完整，确保所有节点使用相同的证书链

### 调试技巧

1. **启用调试模式**：使用`--debug`参数获取详细日志
   ```bash
   go run main.go server --bind 0.0.0.0:23336 --debug
   ```

2. **检查连接状态**：使用网络工具验证节点间连接
   ```bash
   # 检查服务器是否监听端口
   netstat -ano | findstr :23336
   
   # 测试连接
   telnet 192.168.1.100 23336
   ```

## 构建说明

### 编译二进制文件

1. **环境准备**：
   - 安装Go 1.16或更高版本
   - 设置`GOPATH`环境变量

2. **编译当前平台**：
   ```bash
   cd JTunnel
   go build -o jtunnel main.go
   ```

3. **交叉编译其他平台**：
   
   #### Windows 64位
   ```bash
   SET CGO_ENABLED=0
   SET GOOS=windows
   SET GOARCH=amd64
   go build -o jtunnel-windows-amd64.exe main.go
   ```

   #### Linux 64位
   ```bash
   CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o jtunnel-linux-amd64 main.go
   ```

   #### macOS 64位
   ```bash
   CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build -o jtunnel-darwin-amd64 main.go
   ```

### 运行编译后的二进制文件

```bash
# Linux/macOS
chmod +x jtunnel-linux-amd64
./jtunnel-linux-amd64 server --bind 0.0.0.0:23336

# Windows
jtunnel-windows-amd64.exe server --bind 0.0.0.0:23336
```
