package cmd

import (
	"JTunnel/forward"
	"fmt"

	"github.com/spf13/cobra"
)

var (
	forwardDebugMode bool
	forwardTimeout   int
)

var ForwardCmd = &cobra.Command{
	Use:   "forward <listen-port-or-address> <target-address>",
	Short: "直接监听本机TCP端口并转发到目标地址",
	Long: `直接TCP端口转发，不需要启动JTunnel Server。

示例:
  jtunnel forward 8080 10.0.0.5:80
  jtunnel forward 0.0.0.0:8080 example.com:80
  jtunnel forward "[::1]:8080" "[2001:db8::1]:80"`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		listenAddress, err := forward.NormalizeListenAddress(args[0])
		if err != nil {
			return err
		}
		if err := forward.ValidateTargetAddress(args[1]); err != nil {
			return err
		}

		fmt.Printf("启动直接TCP转发，监听: %s，目标: %s\n", listenAddress, args[1])
		return forward.Start(listenAddress, args[1], forwardDebugMode, forwardTimeout)
	},
}

func init() {
	ForwardCmd.Flags().BoolVarP(&forwardDebugMode, "debug", "d", false, "输出每个连接的调试日志")
	ForwardCmd.Flags().IntVarP(&forwardTimeout, "timeout", "t", 10, "连接目标的超时时间（秒）")
}
