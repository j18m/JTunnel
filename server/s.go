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
	"sync"
	"time"

	"github.com/hashicorp/yamux"
)

type Socks5Config struct {
	Port     int    `json:"port"`
	Username string `json:"username"`
	Password string `json:"password"`
}

func StartServer(listenAddr string) error {
	// 加载证书池
	certPool := x509.NewCertPool()
	if !certPool.AppendCertsFromPEM([]byte(config.ClientCertPEM)) {
		return fmt.Errorf("添加客户端证书到证书池失败")
	}

	// 加载服务器证书
	serverCert, err := tls.X509KeyPair([]byte(config.ServerCertPEM), []byte(config.ServerKeyPEM))
	if err != nil {
		return fmt.Errorf("加载服务器证书失败: %v", err)
	}

	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{serverCert},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    certPool,
	}

	// 启动监听
	listener, err := tls.Listen("tcp", listenAddr, tlsConfig)
	if err != nil {
		return fmt.Errorf("监听失败: %v", err)
	}
	defer listener.Close()

	log.Printf("服务端启动，监听地址: %s", listenAddr)

	for {
		conn, err := listener.Accept()
		if err != nil {
			log.Printf("接受连接失败: %v", err)
			continue
		}
		go handleClientConnection(conn.(*tls.Conn))
	}
}

func handleClientConnection(conn *tls.Conn) {
	defer conn.Close()

	// 创建yamux会话
	session, err := yamux.Server(conn, nil)
	if err != nil {
		log.Printf("创建yamux会话失败: %v", err)
		return
	}
	defer session.Close()

	// 读取客户端发送的 SOCKS5 配置
	configStream, err := session.Accept()
	if err != nil {
		log.Printf("接受配置流失败: %v", err)
		return
	}
	defer configStream.Close()

	var socksConfig Socks5Config
	decoder := json.NewDecoder(configStream)
	err = decoder.Decode(&socksConfig)
	if err != nil {
		log.Printf("读取 SOCKS5 配置失败: %v", err)
		return
	}

	log.Printf("接收到 SOCKS5 配置: 端口=%d, 用户名=%s", socksConfig.Port, socksConfig.Username)

	// 启动 SOCKS5 服务器
	socksListener, err := net.Listen("tcp", fmt.Sprintf("0.0.0.0:%d", socksConfig.Port))
	if err != nil {
		log.Printf("启动 SOCKS5 监听失败: %v", err)
		return
	}
	defer socksListener.Close()

	log.Printf("SOCKS5 服务器启动，监听端口: %d", socksConfig.Port)

	// 使用 WaitGroup 来等待所有连接处理完成
	var wg sync.WaitGroup
	defer wg.Wait()

	// 使用 channel 来控制 SOCKS5 服务器的生命周期
	stopChan := make(chan struct{})
	defer close(stopChan)

	// 启动一个 goroutine 来监控客户端连接状态
	wg.Add(1)
	go func() {
		defer wg.Done()
		// 定期检查客户端连接是否断开
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				// 检查会话是否已关闭
				if session.IsClosed() {
					log.Printf("客户端连接已断开，关闭 SOCKS5 服务")
					socksListener.Close() // 关闭 SOCKS5 监听器
					return
				}
			case <-stopChan:
				return
			}
		}
	}()

	// 处理 SOCKS5 连接
	for {
		socksConn, err := socksListener.Accept()
		if err != nil {
			// 检查是否是因关闭监听器而产生的错误
			select {
			case <-stopChan:
				log.Printf("SOCKS5 服务正常关闭")
			default:
				log.Printf("接受 SOCKS5 连接失败: %v", err)
			}
			break
		}

		wg.Add(1)
		go func() {
			defer wg.Done()
			handleSocks5Connection(socksConn, session, socksConfig)
		}()
	}
}

func handleSocks5Connection(socksConn net.Conn, session *yamux.Session, config Socks5Config) {
	defer socksConn.Close()

	// 实现 SOCKS5 认证
	if err := socks5Auth(socksConn, config.Username, config.Password); err != nil {
		log.Printf("SOCKS5 认证失败: %v", err)
		return
	}

	// 获取目标地址
	targetAddr, atyp, err := getTargetAddress(socksConn)
	if err != nil {
		log.Printf("获取目标地址失败: %v", err)
		return
	}

	// 解析目标地址
	host, portStr, err := net.SplitHostPort(targetAddr)
	if err != nil {
		log.Printf("解析目标地址失败: %v", err)
		return
	}
	port, _ := strconv.Atoi(portStr)

	// 通过 yamux 会话打开一个新的流
	stream, err := session.Open()
	if err != nil {
		log.Printf("打开流失败: %v", err)
		return
	}
	defer stream.Close()

	// 构建地址数据
	var addrData []byte
	switch atyp {
	case 0x01: // IPv4
		addrData = append(addrData, 0x01)
		ip := net.ParseIP(host).To4()
		addrData = append(addrData, ip...)
	case 0x03: // 域名
		addrData = append(addrData, 0x03)
		addrData = append(addrData, byte(len(host)))
		addrData = append(addrData, []byte(host)...)
	case 0x04: // IPv6
		log.Printf("IPv6 不支持")
		return
	default:
		log.Printf("不支持的地址类型")
		return
	}

	// 添加端口
	addrData = append(addrData, byte(port>>8), byte(port&0xFF))

	// 发送目标地址到客户端
	_, err = stream.Write(addrData)
	if err != nil {
		log.Printf("发送目标地址到客户端失败: %v", err)
		return
	}

	// 等待客户端确认连接建立
	ack := make([]byte, 1)
	_, err = stream.Read(ack)
	if err != nil || ack[0] != 0x01 {
		log.Printf("客户端连接确认失败: %v", err)
		return
	}

	// 在 SOCKS5 客户端和流之间转发数据
	go io.Copy(socksConn, stream)
	io.Copy(stream, socksConn)
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

	// 读取 SOCKS5 请求
	_, err := io.ReadFull(conn, buf[:4])
	if err != nil {
		return "", 0, err
	}

	// 读取目标地址
	var addr string
	atyp := buf[3]
	switch atyp {
	case 0x01: // IPv4
		_, err = io.ReadFull(conn, buf[:4])
		if err != nil {
			return "", 0, err
		}
		addr = fmt.Sprintf("%d.%d.%d.%d", buf[0], buf[1], buf[2], buf[3])
	case 0x03: // 域名
		_, err = io.ReadFull(conn, buf[:1])
		if err != nil {
			return "", 0, err
		}
		addrLen := buf[0]
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

	// 构建正确的 SOCKS5 响应
	response := []byte{0x05, 0x00, 0x00} // VER, REP, RSV

	// 根据请求的 ATYP 构建响应
	switch atyp {
	case 0x01: // IPv4
		response = append(response, 0x01)
		response = append(response, 0x00, 0x00, 0x00, 0x00) // BND.ADDR (4 bytes)
	case 0x03: // 域名
		response = append(response, 0x03)
		response = append(response, byte(len(addr)))
		response = append(response, []byte(addr)...)
	case 0x04: // IPv6
		return "", 0, fmt.Errorf("IPv6 不支持")
	default:
		return "", 0, fmt.Errorf("不支持的地址类型")
	}

	// 添加端口 (2 bytes)
	response = append(response, 0x00, 0x00)

	// 响应客户端
	_, err = conn.Write(response)
	if err != nil {
		return "", 0, err
	}

	return fmt.Sprintf("%s:%d", addr, port), atyp, nil
}
