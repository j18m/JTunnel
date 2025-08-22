package cmd

import (
	"JTunnel/client"
	"fmt"
	"log"

	"github.com/spf13/cobra"
)

// 定义命令行参数变量
var (
	serverAddr string // 服务器地址
	localPort  int    // 本地监听端口
	username   string // 认证用户名
	password   string // 认证密码
)

// ClientCmd 表示客户端命令
var ClientCmd = &cobra.Command{
	Use:   "client",
	Short: "启动JTunnel客户端，建立到远程服务器的隧道连接",
	Long: `JTunnel客户端命令，用于建立到远程JTunnel服务器的安全隧道连接。
	
示例:
  jtunnel client --server 10.100.100.76:23336 --local-port 2337 --username user --password pass
	`,
	Run: func(cmd *cobra.Command, args []string) {
		// 验证必要参数
		if serverAddr == "" {
			log.Fatal("服务器地址不能为空")
		}
		if localPort <= 0 || localPort > 65535 {
			log.Fatal("本地端口必须在1-65535范围内")
		}

		fmt.Printf("启动客户端连接到服务器: %s\n", serverAddr)

		// 启动客户端
		client.StartClient(serverAddr, localPort, username, password)
	},
}

func init() {
	// 添加命令行参数
	ClientCmd.Flags().StringVarP(&serverAddr, "server", "s", "", "JTunnel服务器地址 (格式: host:port)")
	ClientCmd.Flags().IntVarP(&localPort, "local-port", "l", 0, "本地监听端口")
	ClientCmd.Flags().StringVarP(&username, "username", "u", "", "认证用户名")
	ClientCmd.Flags().StringVarP(&password, "password", "p", "", "认证密码")

	// 标记必需参数
	ClientCmd.MarkFlagRequired("server")
	ClientCmd.MarkFlagRequired("local-port")

	// 添加到根命令
	rootCmd.AddCommand(ClientCmd)
}
