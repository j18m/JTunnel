package cmd

import (
	"JTunnel/relay"
	"fmt"

	"github.com/spf13/cobra"
)

// 定义命令行参数变量
var (
	listenAddr     string // 端点监听地址
	nextAddr       string // 下一个端点地址
	relayDebugMode bool   // 调试模式
	relayTimeout   int    // 超时时间（秒）
)

// RelayCmd 表示端点命令
var RelayCmd = &cobra.Command{
	Use:   "relay <next-address>",
	Short: "启动JTunnel中继，转发流量到下一个节点",
	Long: `JTunnel中继命令

示例:
  jtunnel relay 127.0.0.1:23336
  jtunnel relay -l :23337 127.0.0.1:23336
  jtunnel relay -l 0.0.0.0:23337 -n 127.0.0.1:23336
	`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if len(args) > 0 {
			nextAddr = args[0]
		}
		if listenAddr == "" {
			return fmt.Errorf("中继监听地址不能为空")
		}
		if nextAddr == "" {
			return fmt.Errorf("下一个节点地址不能为空")
		}

		fmt.Printf("启动中继，监听地址: %s，转发到: %s\n", listenAddr, nextAddr)

		if err := relay.StartRelay(listenAddr, nextAddr, relayDebugMode, relayTimeout); err != nil {
			return err
		}
		return nil
	},
}

func init() {
	RelayCmd.Flags().StringVarP(&listenAddr, "listen", "l", "0.0.0.0:23337", "中继监听地址 (格式: host:port)")
	RelayCmd.Flags().StringVarP(&nextAddr, "next", "n", "", "下一个节点地址 (格式: host:port)")
	RelayCmd.Flags().BoolVarP(&relayDebugMode, "debug", "d", false, "开启调试模式")
	RelayCmd.Flags().IntVarP(&relayTimeout, "timeout", "t", 10, "超时时间（秒）")
}
