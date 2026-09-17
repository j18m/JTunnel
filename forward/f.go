package forward

import (
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Service struct {
	Scheme        string
	ListenAddress string
	TargetAddress string
}

func ParseServiceURL(value string) (Service, error) {
	parsed, err := url.Parse(value)
	if err != nil {
		return Service{}, fmt.Errorf("服务地址格式无效 %q: %w", value, err)
	}
	if !strings.EqualFold(parsed.Scheme, "tcp") {
		if parsed.Scheme == "" {
			return Service{}, fmt.Errorf("服务地址 %q 缺少协议，当前格式应为 tcp://监听地址/目标地址", value)
		}
		return Service{}, fmt.Errorf("暂不支持 %q 协议，当前仅支持 tcp://", parsed.Scheme)
	}
	if parsed.User != nil {
		return Service{}, fmt.Errorf("tcp:// 服务不支持用户名或密码")
	}
	if parsed.RawQuery != "" {
		return Service{}, fmt.Errorf("tcp:// 服务暂不支持查询参数")
	}
	if parsed.Fragment != "" {
		return Service{}, fmt.Errorf("tcp:// 服务不支持片段参数")
	}

	listenAddress, err := NormalizeListenAddress(parsed.Host)
	if err != nil {
		return Service{}, err
	}
	targetAddress := strings.TrimPrefix(parsed.Path, "/")
	if targetAddress == "" {
		return Service{}, fmt.Errorf("tcp:// 服务缺少转发目标，格式应为 tcp://监听地址/目标地址")
	}
	if strings.Contains(targetAddress, "/") {
		return Service{}, fmt.Errorf("转发目标格式无效 %q", targetAddress)
	}
	if err := ValidateTargetAddress(targetAddress); err != nil {
		return Service{}, err
	}

	return Service{
		Scheme:        "tcp",
		ListenAddress: listenAddress,
		TargetAddress: targetAddress,
	}, nil
}

func NormalizeListenAddress(value string) (string, error) {
	if value == "" {
		return "", fmt.Errorf("监听端口不能为空")
	}
	if !strings.Contains(value, ":") {
		value = net.JoinHostPort("127.0.0.1", value)
	}
	if err := validateAddress(value, true); err != nil {
		return "", fmt.Errorf("监听地址无效 %q: %w", value, err)
	}
	return value, nil
}

func ValidateTargetAddress(value string) error {
	if err := validateAddress(value, false); err != nil {
		return fmt.Errorf("目标地址无效 %q: %w", value, err)
	}
	return nil
}

func validateAddress(value string, allowEmptyHost bool) error {
	host, portString, err := net.SplitHostPort(value)
	if err != nil {
		return err
	}
	if !allowEmptyHost && host == "" {
		return fmt.Errorf("主机不能为空")
	}
	port, err := strconv.Atoi(portString)
	if err != nil || port < 1 || port > 65535 {
		return fmt.Errorf("端口必须是 1 到 65535 之间的数字")
	}
	return nil
}

func Start(listenAddress, targetAddress string, debugMode bool, timeout int) error {
	listenAddress, err := NormalizeListenAddress(listenAddress)
	if err != nil {
		return err
	}
	if err := ValidateTargetAddress(targetAddress); err != nil {
		return err
	}

	return StartServices([]Service{{
		Scheme:        "tcp",
		ListenAddress: listenAddress,
		TargetAddress: targetAddress,
	}}, debugMode, timeout)
}

type runningService struct {
	service  Service
	listener net.Listener
}

func StartServices(services []Service, debugMode bool, timeout int) error {
	if len(services) == 0 {
		return fmt.Errorf("至少需要指定一个监听服务")
	}
	if timeout < 0 {
		return fmt.Errorf("连接超时时间不能小于 0")
	}

	running := make([]runningService, 0, len(services))
	closeListeners := func() {
		for _, item := range running {
			_ = item.listener.Close()
		}
	}
	defer closeListeners()

	// 先完成全部监听，任意一个失败就关闭已创建的监听，避免部分启动。
	for index, service := range services {
		if service.Scheme != "tcp" {
			return fmt.Errorf("服务 %d 的协议 %q 不受支持", index+1, service.Scheme)
		}
		listenAddress, err := NormalizeListenAddress(service.ListenAddress)
		if err != nil {
			return fmt.Errorf("服务 %d: %w", index+1, err)
		}
		if err := ValidateTargetAddress(service.TargetAddress); err != nil {
			return fmt.Errorf("服务 %d: %w", index+1, err)
		}

		listener, err := net.Listen("tcp", listenAddress)
		if err != nil {
			return fmt.Errorf("服务 %d 监听 %s 失败: %w", index+1, listenAddress, err)
		}
		service.ListenAddress = listenAddress
		running = append(running, runningService{service: service, listener: listener})
	}

	errChannel := make(chan error, len(running))
	for index, item := range running {
		log.Printf("TCP转发服务 %d 已启动，监听地址: %s，目标地址: %s", index+1, item.service.ListenAddress, item.service.TargetAddress)
		item := item
		go func() {
			errChannel <- serve(item.listener, item.service.TargetAddress, debugMode, timeout)
		}()
	}

	return <-errChannel
}

func serve(listener net.Listener, targetAddress string, debugMode bool, timeout int) error {
	for {
		localConnection, err := listener.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return nil
			}
			return fmt.Errorf("接受本地连接失败: %w", err)
		}
		go handleConnection(localConnection, targetAddress, debugMode, timeout)
	}
}

func handleConnection(localConnection net.Conn, targetAddress string, debugMode bool, timeout int) {
	defer localConnection.Close()
	if debugMode {
		log.Printf("[DEBUG] 接受转发连接: %s -> %s", localConnection.RemoteAddr(), targetAddress)
	}

	targetConnection, err := net.DialTimeout("tcp", targetAddress, time.Duration(timeout)*time.Second)
	if err != nil {
		log.Printf("连接转发目标失败: %s: %v", targetAddress, err)
		return
	}
	defer targetConnection.Close()

	proxyConnections(localConnection, targetConnection, debugMode)
}

func proxyConnections(localConnection, targetConnection net.Conn, debugMode bool) {
	var waitGroup sync.WaitGroup
	waitGroup.Add(2)

	copyConnection := func(destination, source net.Conn, direction string) {
		defer waitGroup.Done()
		bytesCopied, err := io.CopyBuffer(destination, source, make([]byte, 32*1024))
		if err != nil && err != io.EOF && debugMode {
			log.Printf("[DEBUG] %s 数据转发失败: %v", direction, err)
		}
		if tcpConnection, ok := destination.(*net.TCPConn); ok {
			_ = tcpConnection.CloseWrite()
		}
		if debugMode {
			log.Printf("[DEBUG] %s 数据转发完成，共 %d 字节", direction, bytesCopied)
		}
	}

	go copyConnection(targetConnection, localConnection, "客户端到目标")
	go copyConnection(localConnection, targetConnection, "目标到客户端")
	waitGroup.Wait()
}
