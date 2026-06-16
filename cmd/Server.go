package cmd

import (
	"JTunnel/server"

	"github.com/spf13/cobra"
)

// 定义命令行参数变量
var (
	bindAddr        string // 服务器绑定地址
	serverDebugMode bool   // 调试模式
	serverTimeout   int    // 超时时间（秒）
)

// ServerCmd 表示服务器命令
var ServerCmd = &cobra.Command{
	Use:   "server [listen-address]",
	Short: "启动JTunnel服务器，接受客户端连接并转发流量",
	Long: `JTunnel服务器命令

示例:
  jtunnel server
  jtunnel server :23336
  jtunnel server -l 0.0.0.0:23336
	`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if len(args) > 0 {
			bindAddr = args[0]
		}

		if err := server.StartServer(bindAddr, serverDebugMode, serverTimeout); err != nil {
			return err
		}
		return nil
	},
}

func init() {
	ServerCmd.Flags().StringVarP(&bindAddr, "listen", "l", "0.0.0.0:23336", "监听地址 (格式: host:port)")
	ServerCmd.Flags().StringVarP(&bindAddr, "bind", "b", "0.0.0.0:23336", "监听地址 (兼容旧参数，建议使用 --listen)")
	ServerCmd.Flags().BoolVarP(&serverDebugMode, "debug", "d", false, "开启调试模式")
	ServerCmd.Flags().IntVarP(&serverTimeout, "timeout", "t", 10, "超时时间（秒）")
	ServerCmd.Flags().MarkHidden("bind")
}
