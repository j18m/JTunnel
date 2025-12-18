package server

import (
	"JTunnel/config"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/hashicorp/yamux"
)

type Socks5Config struct {
	Port     int    `json:"port"`
	Username string `json:"username"`
	Password string `json:"password"`
}

func StartServer(listenAddr string, debugMode bool, timeout int) error {
	// 日志输出函数
	debugLog := func(format string, args ...interface{}) {
		if debugMode {
			log.Printf("[DEBUG] "+format, args...)
		}
	}

	debugLog("开始启动服务器，监听地址: %s", listenAddr)

	// 加载客户端证书和服务器证书到证书池
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

	// 加载服务器证书
	serverCert, err := tls.X509KeyPair([]byte(config.ServerCertPEM), []byte(config.ServerKeyPEM))
	if err != nil {
		debugLog("加载服务器证书失败: %v", err)
		return fmt.Errorf("加载服务器证书失败: %v", err)
	}
	debugLog("服务器证书加载成功")

	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{serverCert},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    certPool, // 信任客户端和服务器证书
	}
	debugLog("TLS配置完成")

	// 启动监听
	listener, err := tls.Listen("tcp", listenAddr, tlsConfig)
	if err != nil {
		debugLog("监听失败: %v", err)
		return fmt.Errorf("监听失败: %v", err)
	}
	defer listener.Close()
	debugLog("监听启动成功")

	log.Printf("服务端启动，监听地址: %s (支持客户端和端点连接)", listenAddr)

	for {
		conn, err := listener.Accept()
		if err != nil {
			log.Printf("接受连接失败: %v", err)
			continue
		}
		debugLog("接受新的TLS连接: %s", conn.RemoteAddr().String())
		go handleClientConnection(conn.(*tls.Conn), debugLog)
	}
}

func handleClientConnection(conn *tls.Conn, debugLog func(format string, args ...interface{})) {
	defer conn.Close()

	// 创建yamux会话，增加超时时间
	yamuxConfig := yamux.DefaultConfig()
	yamuxConfig.ConnectionWriteTimeout = 5 * time.Minute
	yamuxConfig.KeepAliveInterval = 30 * time.Second
	debugLog("创建yamux会话配置完成")

	session, err := yamux.Server(conn, yamuxConfig)
	if err != nil {
		debugLog("创建yamux会话失败: %v", err)
		log.Printf("创建yamux会话失败: %v", err)
		return
	}
	defer session.Close()
	debugLog("yamux会话创建成功")

	log.Printf("新的连接建立，客户端地址: %s", conn.RemoteAddr().String())

	// 读取节点发送的 SOCKS5 配置
	configStream, err := session.Accept()
	if err != nil {
		debugLog("接受配置流失败: %v", err)
		log.Printf("接受配置流失败: %v", err)
		return
	}
	defer configStream.Close()
	debugLog("接受配置流成功")

	var socksConfig Socks5Config
	decoder := json.NewDecoder(configStream)
	debugLog("开始解析SOCKS5配置")
	err = decoder.Decode(&socksConfig)
	if err != nil {
		debugLog("读取 SOCKS5 配置失败: %v", err)
		log.Printf("读取 SOCKS5 配置失败: %v", err)
		return
	}
	debugLog("SOCKS5配置解析成功")

	log.Printf("接收到 SOCKS5 配置: 端口=%d, 用户名=%s (来自: %s)", socksConfig.Port, socksConfig.Username, conn.RemoteAddr().String())

	// 直接处理来自客户端的流请求
	for {
		stream, err := session.Accept()
		if err != nil {
			debugLog("接受流失败: %v", err)
			log.Printf("接受流失败: %v", err)
			break
		}
		debugLog("接受流成功")

		// 启动goroutine处理每个流请求
		go func(stream net.Conn) {
			defer stream.Close()
			handleClientStream(stream, debugLog)
		}(stream)
	}

	debugLog("连接处理完成")
}

func handleSocks5Connection(socksConn net.Conn, session *yamux.Session, config Socks5Config, debugLog func(format string, args ...interface{})) {
	defer socksConn.Close()
	debugLog("服务端收到新的SOCKS5连接请求")

	// 实现 SOCKS5 认证
	if err := socks5Auth(socksConn, config.Username, config.Password); err != nil {
		debugLog("SOCKS5 认证失败: %v", err)
		log.Printf("SOCKS5 认证失败: %v", err)
		return
	}
	debugLog("SOCKS5 认证成功")

	// 获取目标地址
	targetAddr, atyp, err := getTargetAddress(socksConn)
	if err != nil {
		debugLog("获取目标地址失败: %v", err)
		log.Printf("获取目标地址失败: %v", err)
		return
	}
	debugLog("获取到目标地址: %s, 地址类型: %d", targetAddr, atyp)

	// 解析目标地址
	host, portStr, err := net.SplitHostPort(targetAddr)
	if err != nil {
		debugLog("解析目标地址失败: %v", err)
		log.Printf("解析目标地址失败: %v", err)
		return
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		debugLog("解析端口失败: %v", err)
		log.Printf("解析端口失败: %v", err)
		return
	}
	debugLog("解析地址为: 主机=%s, 端口=%d", host, port)

	// 发送SOCKS5响应
	debugLog("开始构建SOCKS5响应")
	response := []byte{0x05, 0x00, 0x00} // VER, REP, RSV

	// 根据请求的 ATYP 构建响应
	switch atyp {
	case 0x01: // IPv4
		response = append(response, 0x01)
		response = append(response, 0x00, 0x00, 0x00, 0x00) // BND.ADDR (4 bytes)
	case 0x03: // 域名
		response = append(response, 0x03)
		response = append(response, 0x04)                 // 域名长度为4
		response = append(response, []byte("0.0.0.0")...) // 绑定地址
	case 0x04: // IPv6
		debugLog("IPv6 不支持")
		log.Printf("IPv6 不支持")
		return
	default:
		debugLog("不支持的地址类型")
		log.Printf("不支持的地址类型")
		return
	}

	// 添加端口 (2 bytes)
	response = append(response, byte(port>>8), byte(port&0xFF))

	// 响应客户端
	_, err = socksConn.Write(response)
	if err != nil {
		debugLog("发送SOCKS5响应失败: %v", err)
		log.Printf("发送SOCKS5响应失败: %v", err)
		return
	}
	debugLog("SOCKS5响应发送成功")

	// 通过 yamux 会话打开一个新的流
	stream, err := session.Open()
	if err != nil {
		debugLog("打开流失败: %v", err)
		log.Printf("打开流失败: %v", err)
		return
	}
	defer stream.Close()
	debugLog("yamux流打开成功")

	// 构建地址数据
	var addrData []byte
	switch atyp {
	case 0x01: // IPv4
		addrData = append(addrData, 0x01)
		// 直接使用原始IP地址而不是重新解析
		_, _, err = net.SplitHostPort(targetAddr)
		if err == nil {
			// 如果是IPv4地址格式
			ip := net.ParseIP(host).To4()
			if ip != nil {
				addrData = append(addrData, ip...)
				debugLog("构建IPv4地址数据成功")
			} else {
				// 如果解析失败，切换到域名模式
				atyp = 0x03
				addrData = []byte{0x03}
				addrData = append(addrData, byte(len(host)))
				addrData = append(addrData, []byte(host)...)
				debugLog("IPv4地址解析失败，切换到域名模式")
			}
		}
	case 0x03: // 域名
		addrData = append(addrData, 0x03)
		addrData = append(addrData, byte(len(host)))
		addrData = append(addrData, []byte(host)...)
		debugLog("构建域名地址数据成功")
	case 0x04: // IPv6
		debugLog("IPv6 不支持")
		log.Printf("IPv6 不支持")
		return
	default:
		debugLog("不支持的地址类型")
		log.Printf("不支持的地址类型")
		return
	}

	// 添加端口
	addrData = append(addrData, byte(port>>8), byte(port&0xFF))

	// 发送目标地址到客户端
	_, err = stream.Write(addrData)
	if err != nil {
		debugLog("发送目标地址到客户端失败: %v", err)
		log.Printf("发送目标地址到客户端失败: %v", err)
		return
	}
	debugLog("发送目标地址到客户端成功")

	// 等待客户端确认连接建立
	ack := make([]byte, 1)
	_, err = stream.Read(ack)
	if err != nil || ack[0] != 0x01 {
		debugLog("客户端连接确认失败: %v", err)
		log.Printf("客户端连接确认失败: %v", err)
		return
	}
	debugLog("客户端连接确认成功")

	// 使用缓冲提高性能
	copyBuf := make([]byte, 32*1024)

	// 使用WaitGroup等待两个方向的数据传输完成
	var wg sync.WaitGroup
	wg.Add(2)

	// 从stream到socksConn的全双工传输
	go func() {
		defer wg.Done()
		_, err := io.CopyBuffer(socksConn, stream, copyBuf)
		if err != nil && err != io.EOF {
			// 检查是否是连接重置错误（客户端正常关闭连接）
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				debugLog("从stream到socksConn传输数据超时: %v", err)
				log.Printf("从stream到socksConn传输数据超时: %v", err)
			} else if !strings.Contains(err.Error(), "forcibly closed by the remote host") {
				// 只记录除了连接重置之外的错误
				debugLog("从stream到socksConn传输数据失败: %v", err)
				log.Printf("从stream到socksConn传输数据失败: %v", err)
			}
		}
		// 关闭socksConn的写入端，触发另一端的EOF
		if tcpConn, ok := socksConn.(*net.TCPConn); ok {
			tcpConn.CloseWrite()
		}
		debugLog("从stream到socksConn的数据传输完成")
	}()

	// 从socksConn到stream的全双工传输
	go func() {
		defer wg.Done()
		_, err := io.CopyBuffer(stream, socksConn, copyBuf)
		if err != nil && err != io.EOF {
			// 检查是否是连接重置错误（客户端正常关闭连接）
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				debugLog("从socksConn到stream传输数据超时: %v", err)
				log.Printf("从socksConn到stream传输数据超时: %v", err)
			} else if !strings.Contains(err.Error(), "forcibly closed by the remote host") {
				// 只记录除了连接重置之外的错误
				debugLog("从socksConn到stream传输数据失败: %v", err)
				log.Printf("从socksConn到stream传输数据失败: %v", err)
			}
		}
		// 关闭stream，触发另一端的EOF
		if yamuxStream, ok := stream.(*yamux.Stream); ok {
			yamuxStream.Close()
		}
		debugLog("从socksConn到stream的数据传输完成")
	}()

	// 等待两个方向的数据传输都完成
	wg.Wait()
	debugLog("SOCKS5连接处理完成")
}

func handleClientStream(stream net.Conn, debugLog func(format string, args ...interface{})) {
	defer stream.Close()
	debugLog("服务端收到新的流请求")

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
	var port int

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
	case 0x04: // IPv6
		// 读取16字节IPv6地址
		debugLog("开始读取IPv6地址")
		ipBuf := make([]byte, 16)
		_, err := io.ReadFull(stream, ipBuf)
		if err != nil {
			debugLog("读取IPv6地址失败: %v", err)
			log.Printf("读取IPv6地址失败: %v", err)
			return
		}
		targetAddr = net.IP(ipBuf).String()
		debugLog("解析IPv6地址: %s", targetAddr)
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
	port = int(portBuf[0])<<8 + int(portBuf[1])
	debugLog("解析端口: %d", port)

	// 组合完整的目标地址
	fullTargetAddr := fmt.Sprintf("%s:%d", targetAddr, port)
	debugLog("完整目标地址: %s", fullTargetAddr)

	// 连接到目标地址
	debugLog("开始连接目标地址: %s", fullTargetAddr)
	targetConn, err := net.Dial("tcp", fullTargetAddr)
	if err != nil {
		debugLog("连接目标地址失败: %s, error: %v", fullTargetAddr, err)
		log.Printf("连接目标地址失败: %s, error: %v", fullTargetAddr, err)
		return
	}
	defer targetConn.Close()
	debugLog("成功连接到目标地址: %s", fullTargetAddr)

	// 向客户端发送连接成功确认
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
		debugLog("targetConn到stream的数据传输完成")
	}()

	// 等待两个方向的数据传输都完成
	wg.Wait()
	debugLog("所有数据传输完成，关闭连接")
}

func socks5Auth(conn net.Conn, username, password string) error {
	buf := make([]byte, 256)

	// 读取客户端版本和方法数量
	_, err := io.ReadFull(conn, buf[:2])
	if err != nil {
		return err
	}

	// 读取方法
	nmethods := buf[1]
	_, err = io.ReadFull(conn, buf[:nmethods])
	if err != nil {
		return err
	}

	// 如果需要认证
	if username != "" && password != "" {
		// 告诉客户端需要用户名/密码认证
		conn.Write([]byte{0x05, 0x02})

		// 读取认证版本
		_, err := io.ReadFull(conn, buf[:2])
		if err != nil {
			return err
		}

		// 读取用户名
		ulen := buf[1]
		_, err = io.ReadFull(conn, buf[:ulen])
		if err != nil {
			return err
		}
		user := string(buf[:ulen])

		// 读取密码
		_, err = io.ReadFull(conn, buf[:1])
		if err != nil {
			return err
		}
		plen := buf[0]
		_, err = io.ReadFull(conn, buf[:plen])
		if err != nil {
			return err
		}
		pass := string(buf[:plen])

		// 验证用户名密码
		if user != username || pass != password {
			conn.Write([]byte{0x01, 0x01})
			return fmt.Errorf("认证失败")
		}

		// 认证成功
		conn.Write([]byte{0x01, 0x00})
	} else {
		// 告诉客户端不需要认证
		conn.Write([]byte{0x05, 0x00})
	}

	return nil
}

func getTargetAddress(conn net.Conn) (string, byte, error) {
	buf := make([]byte, 256)

	// 读取 SOCKS5 请求头
	_, err := io.ReadFull(conn, buf[:4])
	if err != nil {
		return "", 0, err
	}

	// 检查版本
	if buf[0] != 0x05 {
		return "", 0, fmt.Errorf("不支持的SOCKS版本")
	}

	// 检查命令类型
	if buf[1] != 0x01 { // 只支持CONNECT命令
		return "", 0, fmt.Errorf("不支持的命令类型")
	}

	// 读取目标地址
	var addr string
	atyp := buf[3]
	
	// 先读取地址部分
	switch atyp {
	case 0x01: // IPv4
		// 读取4字节IPv4地址
		_, err = io.ReadFull(conn, buf[:4])
		if err != nil {
			return "", 0, err
		}
		addr = fmt.Sprintf("%d.%d.%d.%d", buf[0], buf[1], buf[2], buf[3])
	case 0x03: // 域名
		// 读取域名长度
		_, err = io.ReadFull(conn, buf[:1])
		if err != nil {
			return "", 0, err
		}
		addrLen := int(buf[0])
		
		// 读取域名
		_, err = io.ReadFull(conn, buf[:addrLen])
		if err != nil {
			return "", 0, err
		}
		addr = string(buf[:addrLen])
	case 0x04: // IPv6
		return "", 0, fmt.Errorf("IPv6 不支持")
	default:
		return "", 0, fmt.Errorf("不支持的地址类型")
	}

	// 读取端口
	_, err = io.ReadFull(conn, buf[:2])
	if err != nil {
		return "", 0, err
	}
	port := int(buf[0])<<8 + int(buf[1])

	// 返回完整的目标地址
	return fmt.Sprintf("%s:%d", addr, port), atyp, nil
}
