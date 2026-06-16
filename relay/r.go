package relay

import (
	"JTunnel/config"
	"bufio"
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

// readResetter 是一个包装类型，用于支持流的重新读取
// 它包装了一个bufio.Reader和原始的net.Conn
// 实现了net.Conn接口

type readResetter struct {
	*bufio.Reader
	conn net.Conn
}

// Read 从bufio.Reader读取数据
func (r *readResetter) Read(p []byte) (n int, err error) {
	return r.Reader.Read(p)
}

// Write 直接写入原始连接
func (r *readResetter) Write(p []byte) (n int, err error) {
	return r.conn.Write(p)
}

// Close 关闭原始连接
func (r *readResetter) Close() error {
	return r.conn.Close()
}

// LocalAddr 返回原始连接的本地地址
func (r *readResetter) LocalAddr() net.Addr {
	return r.conn.LocalAddr()
}

// RemoteAddr 返回原始连接的远程地址
func (r *readResetter) RemoteAddr() net.Addr {
	return r.conn.RemoteAddr()
}

// SetDeadline 设置原始连接的截止时间
func (r *readResetter) SetDeadline(t time.Time) error {
	return r.conn.SetDeadline(t)
}

// SetReadDeadline 设置原始连接的读取截止时间
func (r *readResetter) SetReadDeadline(t time.Time) error {
	return r.conn.SetReadDeadline(t)
}

// SetWriteDeadline 设置原始连接的写入截止时间
func (r *readResetter) SetWriteDeadline(t time.Time) error {
	return r.conn.SetWriteDeadline(t)
}

func StartRelay(listenAddr string, nextAddr string, debugMode bool, timeout int) error {
	// 日志输出函数
	debugLog := func(format string, args ...interface{}) {
		if debugMode {
			log.Printf("[DEBUG] "+format, args...)
		}
	}

	debugLog("开始启动中继节点，监听地址: %s，转发到: %s", listenAddr, nextAddr)

	if listenAddr == "" || nextAddr == "" {
		debugLog("监听地址或下一个端点地址不能为空")
		return fmt.Errorf("必须指定监听地址和下一个端点地址")
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

	// 加载服务器证书
	serverCert, err := tls.X509KeyPair([]byte(config.ServerCertPEM), []byte(config.ServerKeyPEM))
	if err != nil {
		debugLog("加载服务器证书失败: %v", err)
		return fmt.Errorf("加载服务器证书失败: %v", err)
	}
	debugLog("服务器证书加载成功")

	// 服务器端TLS配置（用于接收上一个节点的连接）
	serverTLSConfig := &tls.Config{
		Certificates: []tls.Certificate{serverCert}, // 端点使用服务器证书作为服务端证书
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    certPool, // 信任客户端和服务器证书
	}
	debugLog("服务器端TLS配置完成")

	// 客户端TLS配置（用于连接到下一个节点）
	clientTLSConfig := &tls.Config{
		Certificates:       []tls.Certificate{clientCert},
		RootCAs:            certPool, // 信任客户端和服务器证书
		InsecureSkipVerify: true,
		VerifyPeerCertificate: func(rawCerts [][]byte, verifiedChains [][]*x509.Certificate) error {
			return verifyPeerCertificate(rawCerts, certPool)
		},
	}
	debugLog("客户端TLS配置完成")

	// 启动监听
	debugLog("开始监听地址: %s", listenAddr)
	listener, err := tls.Listen("tcp", listenAddr, serverTLSConfig)
	if err != nil {
		debugLog("监听失败: %v", err)
		return fmt.Errorf("监听失败: %v", err)
	}
	defer listener.Close()
	debugLog("监听成功")

	log.Printf("端点启动，监听地址: %s，转发到: %s", listenAddr, nextAddr)

	for {
		// 接收来自上一个节点的连接
		conn, err := listener.Accept()
		if err != nil {
			debugLog("接受连接失败: %v", err)
			log.Printf("接受连接失败: %v", err)
			continue
		}
		debugLog("接受来自上一个节点的连接: %s", conn.RemoteAddr().String())

		go handleRelayConnection(conn.(*tls.Conn), nextAddr, clientTLSConfig, debugLog, timeout)
	}
}

func verifyPeerCertificate(rawCerts [][]byte, roots *x509.CertPool) error {
	if len(rawCerts) == 0 {
		return fmt.Errorf("peer did not provide a certificate")
	}
	cert, err := x509.ParseCertificate(rawCerts[0])
	if err != nil {
		return err
	}
	_, err = cert.Verify(x509.VerifyOptions{Roots: roots})
	return err
}

func handleRelayConnection(conn *tls.Conn, nextAddr string, tlsConfig *tls.Config, debugLog func(format string, args ...interface{}), timeout int) {
	defer conn.Close()
	debugLog("开始处理中继连接")

	// 创建yamux会话（作为服务器端接收上一个节点的连接）
	debugLog("创建yamux会话（作为服务器端）")
	yamuxServerConfig := yamux.DefaultConfig()
	yamuxServerConfig.ConnectionWriteTimeout = 5 * time.Minute
	yamuxServerConfig.KeepAliveInterval = 30 * time.Second
	session, err := yamux.Server(conn, yamuxServerConfig)
	if err != nil {
		debugLog("创建yamux会话失败: %v", err)
		log.Printf("创建yamux会话失败: %v", err)
		return
	}
	defer session.Close()
	debugLog("yamux会话创建成功")

	// 设置连接超时
	connTimeout := time.Duration(timeout) * time.Second
	debugLog("连接下一个节点的超时时间设置为: %v", connTimeout)

	// 连接到下一个节点
	debugLog("连接到下一个节点: %s", nextAddr)
	// 使用net.DialTimeout和tls.Client的组合替代tls.DialWithTimeout
	tcpConn, err := net.DialTimeout("tcp", nextAddr, connTimeout)
	if err != nil {
		debugLog("创建TCP连接失败: %v", err)
		log.Printf("创建TCP连接失败: %v", err)
		return
	}
	nextConn := tls.Client(tcpConn, tlsConfig)
	defer nextConn.Close()
	debugLog("已连接到下一个节点: %s", nextAddr)

	// 创建yamux会话（作为客户端连接到下一个节点）
	debugLog("创建yamux会话（作为客户端）")
	yamuxClientConfig := yamux.DefaultConfig()
	yamuxClientConfig.ConnectionWriteTimeout = 5 * time.Minute
	yamuxClientConfig.KeepAliveInterval = 30 * time.Second
	nextSession, err := yamux.Client(nextConn, yamuxClientConfig)
	if err != nil {
		debugLog("创建到下一个节点的yamux会话失败: %v", err)
		log.Printf("创建到下一个节点的yamux会话失败: %v", err)
		return
	}
	defer nextSession.Close()
	debugLog("到下一个节点的yamux会话创建成功")

	log.Printf("已建立到下一个节点的连接: %s", nextAddr)

	// 处理双向流转发
	debugLog("开始处理双向流转发")

	// 标记是否已经处理过SOCKS5配置
	configHandled := false

	// 启动goroutine处理从下一个节点到上一个节点的流
	go func() {
		debugLog("开始处理从下一个节点到上一个节点的流转发")
		for {
			// 接受来自下一个节点的流
			debugLog("等待接受来自下一个节点的流")
			nextStream, err := nextSession.Accept()
			if err != nil {
				debugLog("接受下一个节点的流失败: %v", err)
				log.Printf("接受下一个节点的流失败: %v", err)
				break
			}
			debugLog("接受来自下一个节点的流成功")

			// 打开到上一个节点的流
			debugLog("打开到上一个节点的流")
			upperStream, err := session.Open()
			if err != nil {
				debugLog("打开到上一个节点的流失败: %v", err)
				log.Printf("打开到上一个节点的流失败: %v", err)
				nextStream.Close()
				continue
			}
			debugLog("打开到上一个节点的流成功")

			// 转发流
			go func(upperStream net.Conn, lowerStream net.Conn, debugLog func(format string, args ...interface{})) {
				defer upperStream.Close()
				defer lowerStream.Close()

				// 使用缓冲提高性能
				copyBuf := make([]byte, 32*1024)

				// 使用WaitGroup等待两个方向的数据传输完成
				var wg sync.WaitGroup
				wg.Add(2)
				debugLog("启动反向双向数据传输goroutine")

				// 从下一个节点到上一个节点的数据传输
				go func() {
					defer wg.Done()
					debugLog("开始从下一个节点到上一个节点的数据传输")
					bytesCopied, err := io.CopyBuffer(upperStream, lowerStream, copyBuf)
					if err != nil && err != io.EOF {
						debugLog("从下一个节点到上一个节点传输数据失败: %v", err)
						log.Printf("从下一个节点到上一个节点传输数据失败: %v", err)
					} else {
						debugLog("从下一个节点到上一个节点传输数据完成，传输字节数: %d", bytesCopied)
					}
					// 关闭上一个节点流，触发另一端的EOF
					if tcpConn, ok := upperStream.(*yamux.Stream); ok {
						tcpConn.Close()
						debugLog("已关闭到上一个节点流")
					}
				}()

				// 从上一个节点到下一个节点的数据传输
				go func() {
					defer wg.Done()
					debugLog("开始从上一个节点到下一个节点的数据传输")
					bytesCopied, err := io.CopyBuffer(lowerStream, upperStream, copyBuf)
					if err != nil && err != io.EOF {
						debugLog("从上一个节点到下一个节点传输数据失败: %v", err)
						log.Printf("从上一个节点到下一个节点传输数据失败: %v", err)
					} else {
						debugLog("从上一个节点到下一个节点传输数据完成，传输字节数: %d", bytesCopied)
					}
					// 关闭下一个节点流，触发另一端的EOF
					if tcpConn, ok := lowerStream.(*yamux.Stream); ok {
						tcpConn.Close()
						debugLog("已关闭到下一个节点流")
					}
				}()

				wg.Wait()
				debugLog("反向流转发处理完成")
			}(upperStream, nextStream, debugLog)
		}
	}()

	// 主循环处理从上一个节点到下一个节点的流
	for {
		// 接受来自上一个节点的流
		debugLog("等待接受来自上一个节点的流")
		stream, err := session.Accept()
		if err != nil {
			debugLog("接受流失败: %v", err)
			log.Printf("接受流失败: %v", err)
			break
		}
		debugLog("接受来自上一个节点的流成功")

		// 如果还没有处理过SOCKS5配置，尝试解析为配置流
		if !configHandled {
			// 创建一个带缓冲的读取器，以便可以重新读取数据
			bufReader := bufio.NewReader(stream)

			// 尝试解析为SOCKS5配置流
			var socksConfig Socks5Config
			decoder := json.NewDecoder(bufReader)
			err = decoder.Decode(&socksConfig)

			if err == nil {
				// 是SOCKS5配置流
				configHandled = true
				debugLog("解析SOCKS5配置成功: 端口=%d, 用户名=%s", socksConfig.Port, socksConfig.Username)

				// 转发SOCKS5配置到下一个节点
				go func(socksConfig Socks5Config, nextSession *yamux.Session, debugLog func(format string, args ...interface{})) {
					debugLog("开始转发SOCKS5配置到下一个节点")
					nextConfigStream, err := nextSession.Open()
					if err != nil {
						debugLog("打开到下一个节点的配置流失败: %v", err)
						log.Printf("打开到下一个节点的配置流失败: %v", err)
						return
					}
					defer nextConfigStream.Close()
					debugLog("打开到下一个节点的配置流成功")

					encoder := json.NewEncoder(nextConfigStream)
					err = encoder.Encode(socksConfig)
					if err != nil {
						debugLog("转发SOCKS5配置失败: %v", err)
						log.Printf("转发SOCKS5配置失败: %v", err)
						return
					}
					debugLog("转发SOCKS5配置成功")
					log.Printf("已转发SOCKS5配置: 端口=%d, 用户名=%s", socksConfig.Port, socksConfig.Username)
				}(socksConfig, nextSession, debugLog)

				continue
			} else {
				// 不是SOCKS5配置流，重新封装流以便后续处理
				// 使用bufio.NewReader保留已读取的数据
				stream = &readResetter{
					Reader: bufReader,
					conn:   stream,
				}
			}
		}

		// 处理普通数据流
		go forwardStream(stream, nextSession, debugLog)
	}
}

func forwardStream(stream net.Conn, nextSession *yamux.Session, debugLog func(format string, args ...interface{})) {
	defer stream.Close()
	debugLog("开始处理流转发，上一个节点: %s，下一个节点会话创建中", stream.RemoteAddr().String())

	// 打开到下一个节点的流
	debugLog("打开到下一个节点的流")
	nextStream, err := nextSession.Open()
	if err != nil {
		debugLog("打开到下一个节点的流失败: %v", err)
		log.Printf("打开到下一个节点的流失败: %v", err)
		return
	}
	defer nextStream.Close()
	debugLog("打开到下一个节点的流成功")

	// 使用缓冲提高性能
	copyBuf := make([]byte, 32*1024)
	debugLog("数据传输缓冲区大小: %d字节", len(copyBuf))

	// 使用WaitGroup等待两个方向的数据传输完成
	var wg sync.WaitGroup
	wg.Add(2)
	debugLog("启动双向数据传输goroutine")

	// 从上一个节点到下一个节点的数据传输
	go func() {
		defer wg.Done()
		debugLog("开始从上一个节点到下一个节点的数据传输")
		bytesCopied, err := io.CopyBuffer(nextStream, stream, copyBuf)
		if err != nil && err != io.EOF {
			debugLog("从上一个节点到下一个节点传输数据失败: %v", err)
			log.Printf("从上一个节点到下一个节点传输数据失败: %v", err)
		} else {
			debugLog("从上一个节点到下一个节点传输数据完成，传输字节数: %d", bytesCopied)
		}
		// 关闭下一个节点流，触发另一端的EOF
		if tcpConn, ok := nextStream.(*yamux.Stream); ok {
			tcpConn.Close()
			debugLog("已关闭到下一个节点流")
		}
	}()

	// 从下一个节点到上一个节点的数据传输
	go func() {
		defer wg.Done()
		debugLog("开始从下一个节点到上一个节点的数据传输")
		bytesCopied, err := io.CopyBuffer(stream, nextStream, copyBuf)
		if err != nil && err != io.EOF {
			debugLog("从下一个节点到上一个节点传输数据失败: %v", err)
			log.Printf("从下一个节点到上一个节点传输数据失败: %v", err)
		} else {
			debugLog("从下一个节点到上一个节点传输数据完成，传输字节数: %d", bytesCopied)
		}
		// 关闭上一个节点流，触发另一端的EOF
		if tcpConn, ok := stream.(*yamux.Stream); ok {
			tcpConn.Close()
			debugLog("已关闭上一个节点流")
		}
	}()

	// 等待两个方向的数据传输都完成
	wg.Wait()
	debugLog("流转发处理完成，两个方向的数据传输都已结束")
}
