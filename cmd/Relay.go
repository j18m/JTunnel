package cmd

import (
	"JTunnel/relay"
	"fmt"
	"log"

	"github.com/spf13/cobra"
)

// 定义命令行参数变量
var (
	listenAddr      string // 端点监听地址
	nextAddr        string // 下一个端点地址
	relayDebugMode  bool   // 调试模式
	relayTimeout    int    // 超时时间（秒）
)

// RelayCmd 表示端点命令
var RelayCmd = &cobra.Command{
	Use:   "relay",
	Short: "启动JTunnel端点，转发流量到下一个端点",
	Long: `JTunnel端点命令

示例:
  # 启动端点，监听指定地址并转发到下一个端点
  jtunnel relay --listen 0.0.0.0:23337 --next 127.0.0.1:23338
	`,
	Run: func(cmd *cobra.Command, args []string) {
		// 验证必要参数
		if listenAddr == "" {
			log.Fatal("端点监听地址不能为空")
		}
		if nextAddr == "" {
			log.Fatal("下一个端点地址不能为空")
		}

		fmt.Printf("启动端点，监听地址: %s，转发到: %s\n", listenAddr, nextAddr)

		// 启动端点
	relay.StartRelay(listenAddr, nextAddr, relayDebugMode, relayTimeout)
	},
}

func init() {
	// 添加命令行参数
	RelayCmd.Flags().StringVarP(&listenAddr, "listen", "l", "", "端点监听地址 (格式: host:port)")
	RelayCmd.Flags().StringVarP(&nextAddr, "next", "n", "", "下一个端点地址 (格式: host:port)")
	RelayCmd.Flags().BoolVarP(&relayDebugMode, "debug", "d", false, "开启调试模式")
	RelayCmd.Flags().IntVarP(&relayTimeout, "timeout", "t", 10, "超时时间（秒）")

	// 标记必需参数
	RelayCmd.MarkFlagRequired("listen")
	RelayCmd.MarkFlagRequired("next")
}