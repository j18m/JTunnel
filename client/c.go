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
