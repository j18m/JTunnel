package cmd

import (
	"JTunnel/client"
	"fmt"
	"net"
	"strconv"

	"github.com/spf13/cobra"
)

// 定义命令行参数变量
var (
	serverAddr      string // 服务器地址
	socksListenAddr string // 本地SOCKS5监听地址
	socksPort       int    // 兼容旧参数：本地SOCKS5代理端口
	username        string // 认证用户名
	password        string // 认证密码
	debugMode       bool   // 调试模式
	timeout         int    // 超时时间（秒）
)

// ClientCmd 表示客户端命令
var ClientCmd = &cobra.Command{
	Use:   "client <server-address>",
	Short: "启动JTunnel客户端，建立到远程服务器的隧道连接",
	Long: `JTunnel客户端命令，用于建立到远程JTunnel服务器的安全隧道连接。
	
示例:
  jtunnel client 10.100.100.76:23336
  jtunnel client 10.100.100.76:23336 -l 127.0.0.1:1080
  jtunnel client 10.100.100.76:23336 -l :1080 --username user --password pass
	`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if len(args) > 0 {
			serverAddr = args[0]
		}
		if serverAddr == "" {
			return fmt.Errorf("服务器地址不能为空")
		}
		if socksPort > 0 {
			socksListenAddr = net.JoinHostPort("127.0.0.1", strconv.Itoa(socksPort))
		}
		if _, _, err := net.SplitHostPort(socksListenAddr); err != nil {
			return fmt.Errorf("本地SOCKS5监听地址无效: %w", err)
		}

		fmt.Printf("启动客户端，服务器: %s，本地SOCKS5: %s\n", serverAddr, socksListenAddr)

		if err := client.StartClient(serverAddr, socksListenAddr, username, password, debugMode, timeout); err != nil {
			return err
		}
		return nil
	},
}

func init() {
	ClientCmd.Flags().StringVarP(&serverAddr, "server", "s", "", "JTunnel服务器地址 (兼容旧参数，建议使用位置参数)")
	ClientCmd.Flags().StringVarP(&socksListenAddr, "listen", "l", "127.0.0.1:1080", "本地SOCKS5监听地址 (格式: host:port)")
	ClientCmd.Flags().IntVar(&socksPort, "socks-port", 0, "本地SOCKS5代理端口 (兼容旧参数，建议使用 --listen)")
	ClientCmd.Flags().StringVarP(&username, "username", "u", "", "认证用户名")
	ClientCmd.Flags().StringVarP(&password, "password", "p", "", "认证密码")
	ClientCmd.Flags().BoolVarP(&debugMode, "debug", "d", false, "开启调试模式")
	ClientCmd.Flags().IntVarP(&timeout, "timeout", "t", 10, "超时时间（秒）")
	ClientCmd.Flags().MarkHidden("server")
}
