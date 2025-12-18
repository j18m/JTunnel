package client

import (
	"JTunnel/config"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"sync"
	"time"

	"github.com/hashicorp/yamux"
)

type Socks5Config struct {
	Port     int    `json:"port"`
	Username string `json:"username"`
	Password string `json:"password"`
}

func StartClient(serverAddr string, socksPort int, username, password string, debugMode bool, timeout int) error {
	// 日志输出函数
	debugLog := func(format string, args ...interface{}) {
		if debugMode {
			log.Printf("[DEBUG] "+format, args...)
		}
	}

	debugLog("开始启动客户端，服务器地址: %s", serverAddr)

	if serverAddr == "" {
		debugLog("服务器地址不能为空")
		return fmt.Errorf("必须指定服务器地址")
	}

	// 加载客户端证书和服务器证书到证书池
	debugLog("开始加载证书")
	certPool := x509.NewCertPool()
	if !certPool.AppendCertsFromPEM([]byte(config.ClientCertPEM)) {
		debugLog("添加客户端证书到证书池失败")
		return fmt.Errorf("添加客户端证书到证书池失败")
	}
	debugLog("客户端证书添加到证书池成功")
	if !certPool.AppendCertsFromPEM([]byte(config.ServerCertPEM)) {
		debugLog("添加服务器证书到证书池失败")
		return fmt.Errorf("添加服务器证书到证书池失败")
	}
	debugLog("服务器证书添加到证书池成功")

	// 加载客户端证书
	clientCert, err := tls.X509KeyPair([]byte(config.ClientCertPEM), []byte(config.ClientKeyPEM))
	if err != nil {
		debugLog("加载客户端证书失败: %v", err)
		return fmt.Errorf("加载客户端证书失败: %v", err)
	}
	debugLog("客户端证书加载成功")

	tlsConfig := &tls.Config{
		Certificates:       []tls.Certificate{clientCert},
		RootCAs:            certPool, // 信任客户端和服务器证书
		InsecureSkipVerify: true,
	}

	// 设置连接超时
	connTimeout := time.Duration(timeout) * time.Second
	debugLog("连接超时时间设置为: %v", connTimeout)

	// 连接服务器或端点
	debugLog("连接到服务器/端点: %s", serverAddr)
	tcpConn, err := net.DialTimeout("tcp", serverAddr, connTimeout)
	if err != nil {
		debugLog("创建TCP连接失败: %v", err)
		return fmt.Errorf("创建TCP连接失败: %v", err)
	}
	conn := tls.Client(tcpConn, tlsConfig)
	defer conn.Close()
	debugLog("已连接到服务器/端点: %s", serverAddr)

	// 创建yamux会话，增加超时时间
	debugLog("开始创建yamux会话")
	yamuxConfig := yamux.DefaultConfig()
	yamuxConfig.ConnectionWriteTimeout = 5 * time.Minute
	yamuxConfig.KeepAliveInterval = 30 * time.Second
	session, err := yamux.Client(conn, yamuxConfig)
	if err != nil {
		debugLog("创建yamux会话失败: %v", err)
		return fmt.Errorf("创建yamux会话失败: %v", err)
	}
	defer session.Close()
	debugLog("yamux会话创建成功")

	// 发送 SOCKS5 配置
	debugLog("开始发送SOCKS5配置")
	socksConfig := Socks5Config{
		Port:     socksPort,
		Username: username,
		Password: password,
	}

	configStream, err := session.Open()
	if err != nil {
		debugLog("打开配置流失败: %v", err)
		return fmt.Errorf("打开配置流失败: %v", err)
	}
	defer configStream.Close()
	debugLog("配置流打开成功")

	encoder := json.NewEncoder(configStream)
	err = encoder.Encode(socksConfig)
	if err != nil {
		debugLog("发送 SOCKS5 配置失败: %v", err)
		return fmt.Errorf("发送 SOCKS5 配置失败: %v", err)
	}
	debugLog("SOCKS5配置发送成功")

	// 启动本地SOCKS5代理
	debugLog("开始启动本地SOCKS5代理服务器，监听端口: %d", socksPort)
	socksListener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", socksPort))
	if err != nil {
		debugLog("启动本地SOCKS5代理失败: %v", err)
		return fmt.Errorf("启动本地SOCKS5代理失败: %v", err)
	}
	debugLog("本地SOCKS5代理服务器启动成功，监听端口: %d", socksPort)

	// 创建一个通道用于通知SOCKS5代理服务停止
	stopChan := make(chan struct{})

	// 启动goroutine处理本地SOCKS5代理连接请求
	go func() {
		defer socksListener.Close()
		debugLog("开始接受本地SOCKS5连接请求")
		for {
			select {
			case <-stopChan:
				debugLog("收到停止信号，停止接受新的SOCKS5连接")
				return
			default:
				localConn, err := socksListener.Accept()
				if err != nil {
					debugLog("接受本地SOCKS5连接失败: %v", err)
					continue
				}
				debugLog("接受本地SOCKS5连接成功: %v", localConn.RemoteAddr())
				go handleLocalSocksRequest(localConn, session, username, password, debugLog, timeout)
			}
		}
	}()

	// 等待并处理来自服务器的连接请求
	debugLog("开始等待服务器连接请求")
	for {
		stream, err := session.Accept()
		if err != nil {
			debugLog("接受流失败: %v", err)
			log.Printf("接受流失败: %v", err)
			break
		}
		debugLog("接受流成功")

		go handleClientStream(stream, debugLog, timeout)
	}

	// 当主循环结束时，通知SOCKS5代理服务停止
	debugLog("主循环结束，通知SOCKS5代理服务停止")
	close(stopChan)

	debugLog("客户端运行结束")
	return nil
}

func handleClientStream(stream net.Conn, debugLog func(format string, args ...interface{}), timeout int) {
	defer stream.Close()
	debugLog("客户端收到新的流请求")

	// 读取地址类型
	debugLog("开始读取地址类型")
	buf := make([]byte, 1)
	_, err := io.ReadFull(stream, buf)
	if err != nil {
		debugLog("读取地址类型失败: %v", err)
		log.Printf("读取地址类型失败: %v", err)
		return
	}
	addrType := buf[0]
	debugLog("收到地址类型: %d", addrType)

	var targetAddr string

	switch addrType {
	case 0x01: // IPv4
		// 读取4字节IPv4地址
		debugLog("开始读取IPv4地址")
		ipBuf := make([]byte, 4)
		_, err := io.ReadFull(stream, ipBuf)
		if err != nil {
			debugLog("读取IPv4地址失败: %v", err)
			log.Printf("读取IPv4地址失败: %v", err)
			return
		}
		targetAddr = fmt.Sprintf("%d.%d.%d.%d", ipBuf[0], ipBuf[1], ipBuf[2], ipBuf[3])
		debugLog("解析IPv4地址: %s", targetAddr)
	case 0x03: // 域名
		// 读取域名长度
		debugLog("开始读取域名长度")
		lenBuf := make([]byte, 1)
		_, err := io.ReadFull(stream, lenBuf)
		if err != nil {
			debugLog("读取域名长度失败: %v", err)
			log.Printf("读取域名长度失败: %v", err)
			return
		}
		domainLen := int(lenBuf[0])
		debugLog("域名长度: %d", domainLen)

		// 读取域名
		debugLog("开始读取域名")
		domainBuf := make([]byte, domainLen)
		_, err = io.ReadFull(stream, domainBuf)
		if err != nil {
			debugLog("读取域名失败: %v", err)
			log.Printf("读取域名失败: %v", err)
			return
		}
		targetAddr = string(domainBuf)
		debugLog("解析域名: %s", targetAddr)
	default:
		debugLog("不支持的地址类型: %d", addrType)
		log.Printf("不支持的地址类型: %d", addrType)
		return
	}

	// 读取端口（2字节）
	debugLog("开始读取端口")
	portBuf := make([]byte, 2)
	_, err = io.ReadFull(stream, portBuf)
	if err != nil {
		debugLog("读取端口失败: %v", err)
		log.Printf("读取端口失败: %v", err)
		return
	}
	port := int(portBuf[0])<<8 + int(portBuf[1])
	debugLog("解析端口: %d", port)

	// 组合完整的目标地址
	fullTargetAddr := fmt.Sprintf("%s:%d", targetAddr, port)
	debugLog("完整目标地址: %s", fullTargetAddr)

	// 连接到目标地址
	debugLog("开始连接目标地址: %s，超时时间: %d秒", fullTargetAddr, timeout)
	targetConn, err := net.DialTimeout("tcp", fullTargetAddr, time.Duration(timeout)*time.Second)
	if err != nil {
		debugLog("连接目标地址失败: %s, error: %v", fullTargetAddr, err)
		log.Printf("连接目标地址失败: %s, error: %v", fullTargetAddr, err)
		return
	}
	defer targetConn.Close()
	debugLog("成功连接到目标地址: %s", fullTargetAddr)

	// 向服务端发送连接成功确认
	debugLog("开始发送连接成功确认")
	_, err = stream.Write([]byte{0x01})
	if err != nil {
		debugLog("发送确认失败: %v", err)
		log.Printf("发送确认失败: %v", err)
		return
	}
	debugLog("已发送连接成功确认")

	// 使用缓冲提高性能
	debugLog("开始全双工数据传输")
	copyBuf := make([]byte, 32*1024)

	// 使用WaitGroup等待两个方向的数据传输完成
	var wg sync.WaitGroup
	wg.Add(2)

	// 从stream到targetConn的全双工传输
	go func() {
		defer wg.Done()
		debugLog("启动stream到targetConn的数据传输")
		_, err := io.CopyBuffer(targetConn, stream, copyBuf)
		if err != nil && err != io.EOF {
			debugLog("从stream到targetConn传输数据失败: %v", err)
			log.Printf("从stream到targetConn传输数据失败: %v", err)
		}
		// 关闭目标连接的写入端，触发另一端的EOF
		if tcpConn, ok := targetConn.(*net.TCPConn); ok {
			tcpConn.CloseWrite()
		}
		debugLog("stream到targetConn的数据传输完成")
	}()

	// 从targetConn到stream的全双工传输
	go func() {
		defer wg.Done()
		debugLog("启动targetConn到stream的数据传输")
		_, err := io.CopyBuffer(stream, targetConn, copyBuf)
		if err != nil && err != io.EOF {
			debugLog("从targetConn到stream传输数据失败: %v", err)
			log.Printf("从targetConn到stream传输数据失败: %v", err)
		}
		// 关闭stream，触发另一端的EOF
		if yamuxStream, ok := stream.(*yamux.Stream); ok {
			yamuxStream.Close()
		}
		debugLog("targetConn到stream的数据传输完成")
	}()

	// 等待两个方向的数据传输都完成
	wg.Wait()
	debugLog("所有数据传输完成，关闭连接")
}

func handleLocalSocksRequest(localConn net.Conn, session *yamux.Session, username, password string, debugLog func(format string, args ...interface{}), timeout int) {
	defer localConn.Close()
	debugLog("开始处理本地SOCKS5连接请求")

	// 处理SOCKS5协议握手
	// 1. 版本标识和认证方法选择
	debugLog("开始处理SOCKS5协议握手")
	handshakeBuf := make([]byte, 256)
	n, err := localConn.Read(handshakeBuf)
	if err != nil {
		debugLog("读取SOCKS5握手数据失败: %v", err)
		return
	}

	// 检查版本号是否为5
	if handshakeBuf[0] != 0x05 {
		debugLog("不支持的SOCKS版本: %d", handshakeBuf[0])
		return
	}

	// 协商认证方法
	numMethods := handshakeBuf[1]
	debugLog("客户端支持的认证方法数量: %d", numMethods)

	// 如果提供了用户名和密码，使用用户名密码认证
	if username != "" && password != "" {
		debugLog("使用用户名密码认证")
		// 检查客户端是否支持用户名密码认证(0x02)
		hasUserPassMethod := false
		for i := 2; i < 2+int(numMethods); i++ {
			if handshakeBuf[i] == 0x02 {
				hasUserPassMethod = true
				break
			}
		}

		if hasUserPassMethod {
			// 选择用户名密码认证
			debugLog("选择用户名密码认证")
			_, err = localConn.Write([]byte{0x05, 0x02})
			if err != nil {
				debugLog("发送认证方法选择失败: %v", err)
				return
			}

			// 处理用户名密码认证
		debugLog("开始处理用户名密码认证")
		authBuf := make([]byte, 256)
		_, err := localConn.Read(authBuf)
		if err != nil {
			debugLog("读取认证数据失败: %v", err)
			return
		}

			// 检查认证版本
			if authBuf[0] != 0x01 {
				debugLog("不支持的认证版本: %d", authBuf[0])
				// 发送认证失败响应
				localConn.Write([]byte{0x01, 0x01})
				return
			}

			// 提取用户名和密码
			usernameLen := int(authBuf[1])
			userBuf := authBuf[2 : 2+usernameLen]
			passwordLen := int(authBuf[2+usernameLen])
			passBuf := authBuf[3+usernameLen : 3+usernameLen+passwordLen]

			debugLog("收到用户名: %s, 密码: %s", string(userBuf), string(passBuf))

			// 验证用户名和密码
			if string(userBuf) != username || string(passBuf) != password {
				debugLog("用户名或密码验证失败")
				// 发送认证失败响应
				localConn.Write([]byte{0x01, 0x01})
				return
			}

			// 发送认证成功响应
			debugLog("用户名密码认证成功")
			_, err = localConn.Write([]byte{0x01, 0x00})
			if err != nil {
				debugLog("发送认证成功响应失败: %v", err)
				return
			}
		} else {
			// 客户端不支持用户名密码认证，使用无认证
			debugLog("客户端不支持用户名密码认证，使用无认证")
			_, err = localConn.Write([]byte{0x05, 0x00})
			if err != nil {
				debugLog("发送认证方法选择失败: %v", err)
				return
			}
		}
	} else {
		// 不需要认证，使用无认证方法(0x00)
		debugLog("使用无认证")
		_, err = localConn.Write([]byte{0x05, 0x00})
		if err != nil {
			debugLog("发送认证方法选择失败: %v", err)
			return
		}
	}

	// 2. 处理客户端的连接请求
	debugLog("开始处理客户端的连接请求")
	reqBuf := make([]byte, 256)
	n, err = localConn.Read(reqBuf)
	if err != nil {
		debugLog("读取SOCKS5请求数据失败: %v", err)
		return
	}

	// 检查版本号
	if reqBuf[0] != 0x05 {
		debugLog("不支持的SOCKS版本: %d", reqBuf[0])
		return
	}

	// 检查命令类型，只支持CONNECT命令(0x01)
	if reqBuf[1] != 0x01 {
		debugLog("不支持的SOCKS命令: %d", reqBuf[1])
		// 发送命令不支持响应
		localConn.Write([]byte{0x05, 0x07, 0x00, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00})
		return
	}

	// 解析目标地址
	debugLog("开始解析目标地址")
	addrType := reqBuf[3]
	var targetAddr string
	var port int
	var offset int = 4

	switch addrType {
	case 0x01: // IPv4地址
		debugLog("解析IPv4地址")
		if n < offset+4+2 { // 地址4字节 + 端口2字节
			debugLog("IPv4地址数据不完整")
			return
		}
		ip := net.IPv4(reqBuf[offset], reqBuf[offset+1], reqBuf[offset+2], reqBuf[offset+3])
		targetAddr = ip.String()
		offset += 4
	case 0x03: // 域名
		debugLog("解析域名")
		if n < offset+1 { // 域名长度1字节
			debugLog("域名数据不完整")
			return
		}
		domainLen := int(reqBuf[offset])
		offset += 1
		if n < offset+domainLen+2 { // 域名 + 端口2字节
			debugLog("域名数据不完整")
			return
		}
		targetAddr = string(reqBuf[offset : offset+domainLen])
		offset += domainLen
	case 0x04: // IPv6地址
		debugLog("解析IPv6地址")
		if n < offset+16+2 { // 地址16字节 + 端口2字节
			debugLog("IPv6地址数据不完整")
			return
		}
		ip := net.IP(reqBuf[offset : offset+16])
		targetAddr = ip.String()
		offset += 16
	default:
		debugLog("不支持的地址类型: %d", addrType)
		// 发送地址类型不支持响应
		localConn.Write([]byte{0x05, 0x08, 0x00, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00})
		return
	}

	// 解析端口
	port = int(reqBuf[offset])<<8 + int(reqBuf[offset+1])
	fullTargetAddr := fmt.Sprintf("%s:%d", targetAddr, port)
	debugLog("解析出的完整目标地址: %s", fullTargetAddr)

	// 3. 创建到服务器的Yamux流
	debugLog("开始创建到服务器的Yamux流")
	stream, err := session.Open()
	if err != nil {
		debugLog("创建Yamux流失败: %v", err)
		// 发送网络不可达响应
		localConn.Write([]byte{0x05, 0x03, 0x00, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00})
		return
	}
	defer stream.Close()
	debugLog("Yamux流创建成功")

	// 4. 通过Yamux流将目标地址发送到服务器
	debugLog("开始将目标地址发送到服务器")
	// 发送地址类型
	_, err = stream.Write([]byte{addrType})
	if err != nil {
		debugLog("发送地址类型失败: %v", err)
		return
	}

	// 发送目标地址和端口
	switch addrType {
	case 0x01: // IPv4
		ip := net.ParseIP(targetAddr).To4()
		_, err = stream.Write(ip)
		if err != nil {
			debugLog("发送IPv4地址失败: %v", err)
			return
		}
	case 0x03: // 域名
		_, err = stream.Write([]byte{byte(len(targetAddr))})
		if err != nil {
			debugLog("发送域名长度失败: %v", err)
			return
		}
		_, err = stream.Write([]byte(targetAddr))
		if err != nil {
			debugLog("发送域名失败: %v", err)
			return
		}
	case 0x04: // IPv6
		ip := net.ParseIP(targetAddr).To16()
		_, err = stream.Write(ip)
		if err != nil {
			debugLog("发送IPv6地址失败: %v", err)
			return
		}
	}

	// 发送端口
	portBytes := []byte{byte(port >> 8), byte(port & 0xff)}
	_, err = stream.Write(portBytes)
	if err != nil {
		debugLog("发送端口失败: %v", err)
		return
	}

	// 5. 等待服务器的连接确认
	debugLog("等待服务器的连接确认")
	confirmBuf := make([]byte, 1)
	_, err = io.ReadFull(stream, confirmBuf)
	if err != nil {
		debugLog("读取连接确认失败: %v", err)
		// 发送连接失败响应
		localConn.Write([]byte{0x05, 0x01, 0x00, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00})
		return
	}

	if confirmBuf[0] != 0x01 {
		debugLog("服务器连接目标失败")
		// 发送连接失败响应
		localConn.Write([]byte{0x05, 0x01, 0x00, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00})
		return
	}

	// 6. 发送SOCKS5连接成功响应
	debugLog("发送SOCKS5连接成功响应")
	// 响应格式: 版本(0x05), 状态(0x00成功), RSV(0x00), 地址类型(0x01), 绑定地址, 绑定端口
	localConn.Write([]byte{0x05, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00})

	// 7. 建立双向数据传输
	debugLog("开始建立双向数据传输")
	copyBuf := make([]byte, 32*1024)

	var wg sync.WaitGroup
	wg.Add(2)

	// 从本地连接到服务器流
	go func() {
		defer wg.Done()
		debugLog("启动本地连接到服务器流的数据传输")
		_, err := io.CopyBuffer(stream, localConn, copyBuf)
		if err != nil && err != io.EOF {
			debugLog("本地连接到服务器流数据传输失败: %v", err)
		}
		debugLog("本地连接到服务器流数据传输完成")
	}()

	// 从服务器流到本地连接
	go func() {
		defer wg.Done()
		debugLog("启动服务器流到本地连接的数据传输")
		_, err := io.CopyBuffer(localConn, stream, copyBuf)
		if err != nil && err != io.EOF {
			debugLog("服务器流到本地连接数据传输失败: %v", err)
		}
		debugLog("服务器流到本地连接数据传输完成")
	}()

	// 等待数据传输完成
	wg.Wait()
	debugLog("本地SOCKS5连接请求处理完成")
}
