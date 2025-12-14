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

	"github.com/hashicorp/yamux"
)

type Socks5Config struct {
	Port     int    `json:"port"`
	Username string `json:"username"`
	Password string `json:"password"`
}

func StartClient(serverAddr string, socksPort int, username, password string) error {
	if serverAddr == "" {
		return fmt.Errorf("必须指定服务器地址")
	}

	// 加载证书池
	certPool := x509.NewCertPool()
	if !certPool.AppendCertsFromPEM([]byte(config.ServerCertPEM)) {
		return fmt.Errorf("添加服务器证书到证书池失败")
	}

	// 加载客户端证书
	clientCert, err := tls.X509KeyPair([]byte(config.ClientCertPEM), []byte(config.ClientKeyPEM))
	if err != nil {
		return fmt.Errorf("加载客户端证书失败: %v", err)
	}

	tlsConfig := &tls.Config{
		Certificates:       []tls.Certificate{clientCert},
		RootCAs:            certPool,
		InsecureSkipVerify: true,
	}

	// 连接服务器
	conn, err := tls.Dial("tcp", serverAddr, tlsConfig)
	if err != nil {
		return fmt.Errorf("连接服务器失败: %v", err)
	}
	defer conn.Close()

	log.Printf("已连接到服务器: %s", serverAddr)

	// 创建yamux会话
	session, err := yamux.Client(conn, nil)
	if err != nil {
		return fmt.Errorf("创建yamux会话失败: %v", err)
	}
	defer session.Close()

	// 发送 SOCKS5 配置
	socksConfig := Socks5Config{
		Port:     socksPort,
		Username: username,
		Password: password,
	}

	configStream, err := session.Open()
	if err != nil {
		return fmt.Errorf("打开配置流失败: %v", err)
	}
	defer configStream.Close()

	encoder := json.NewEncoder(configStream)
	err = encoder.Encode(socksConfig)
	if err != nil {
		return fmt.Errorf("发送 SOCKS5 配置失败: %v", err)
	}

	log.Printf("已发送 SOCKS5 配置: 端口=%d", socksPort)

	// 等待并处理来自服务器的连接请求
	for {
		stream, err := session.Accept()
		if err != nil {
			log.Printf("接受流失败: %v", err)
			break
		}

		go handleClientStream(stream)
	}

	return nil
}

func handleClientStream(stream net.Conn) {
	defer stream.Close()

	// 读取地址类型
	buf := make([]byte, 1)
	_, err := stream.Read(buf)
	if err != nil {
		log.Printf("读取地址类型失败: %v", err)
		return
	}
	addrType := buf[0]

	var targetAddr string

	switch addrType {
	case 0x01: // IPv4
		// 读取4字节IPv4地址
		ipBuf := make([]byte, 4)
		_, err := io.ReadFull(stream, ipBuf)
		if err != nil {
			log.Printf("读取IPv4地址失败: %v", err)
			return
		}
		targetAddr = fmt.Sprintf("%d.%d.%d.%d", ipBuf[0], ipBuf[1], ipBuf[2], ipBuf[3])
	case 0x03: // 域名
		// 读取域名长度
		lenBuf := make([]byte, 1)
		_, err := io.ReadFull(stream, lenBuf)
		if err != nil {
			log.Printf("读取域名长度失败: %v", err)
			return
		}
		domainLen := int(lenBuf[0])

		// 读取域名
		domainBuf := make([]byte, domainLen)
		_, err = io.ReadFull(stream, domainBuf)
		if err != nil {
			log.Printf("读取域名失败: %v", err)
			return
		}
		targetAddr = string(domainBuf)
	default:
		log.Printf("不支持的地址类型: %d", addrType)
		return
	}

	// 读取端口（2字节）
	portBuf := make([]byte, 2)
	_, err = io.ReadFull(stream, portBuf)
	if err != nil {
		log.Printf("读取端口失败: %v", err)
		return
	}
	port := int(portBuf[0])<<8 + int(portBuf[1])

	// 组合完整的目标地址
	fullTargetAddr := fmt.Sprintf("%s:%d", targetAddr, port)
	//log.Printf("接收到目标地址: %s", fullTargetAddr)

	// 连接到目标地址
	targetConn, err := net.Dial("tcp", fullTargetAddr)
	if err != nil {
		log.Printf("连接目标地址失败: %s, error: %v", fullTargetAddr, err)
		return
	}
	defer targetConn.Close()

	// 向服务端发送连接成功确认
	_, err = stream.Write([]byte{0x01})
	if err != nil {
		log.Printf("发送确认失败: %v", err)
		return
	}

	// 使用缓冲提高性能
	copyBuf := make([]byte, 32*1024)

	// 使用WaitGroup等待两个方向的数据传输完成
	var wg sync.WaitGroup
	wg.Add(2)

	// 从stream到targetConn的全双工传输
	go func() {
		defer wg.Done()
		_, err := io.CopyBuffer(targetConn, stream, copyBuf)
		if err != nil && err != io.EOF {
			log.Printf("从stream到targetConn传输数据失败: %v", err)
		}
		// 关闭目标连接的写入端，触发另一端的EOF
		if tcpConn, ok := targetConn.(*net.TCPConn); ok {
			tcpConn.CloseWrite()
		}
	}()

	// 从targetConn到stream的全双工传输
	go func() {
		defer wg.Done()
		_, err := io.CopyBuffer(stream, targetConn, copyBuf)
		if err != nil && err != io.EOF {
			log.Printf("从targetConn到stream传输数据失败: %v", err)
		}
	}()

	// 等待两个方向的数据传输都完成
	wg.Wait()
}
