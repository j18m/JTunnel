package cmd

import (
	"JTunnel/server"
	"github.com/spf13/cobra"
)

// 定义命令行参数变量
var (
	bindAddr      string // 服务器绑定地址
	serverDebugMode bool   // 调试模式
	serverTimeout   int    // 超时时间（秒）
)

// ServerCmd 表示服务器命令
var ServerCmd = &cobra.Command{
	Use:   "server",
	Short: "启动JTunnel服务器，接受客户端连接并转发流量",
	Long: `JTunnel服务器命令

示例:
  # 启动服务器，监听指定地址
  jtunnel server --bind 0.0.0.0:23336
	`,
	Run: func(cmd *cobra.Command, args []string) {
		// 验证必要参数
		if bindAddr == "" {
			bindAddr = "0.0.0.0:23336"
		}

		// 启动服务器，传递所有参数
	server.StartServer(bindAddr, serverDebugMode, serverTimeout)
	},
}

func init() {
	// 添加命令行参数
	ServerCmd.Flags().StringVarP(&bindAddr, "bind", "b", "0.0.0.0:23336", "服务器绑定地址 (格式: host:port)")
	ServerCmd.Flags().BoolVarP(&serverDebugMode, "debug", "d", false, "开启调试模式")
	ServerCmd.Flags().IntVarP(&serverTimeout, "timeout", "t", 10, "超时时间（秒）")
}
