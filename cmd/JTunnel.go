package cmd

import (
	"JTunnel/forward"
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var (
	serviceURLs    []string
	serviceDebug   bool
	serviceTimeout int
)

var rootCmd = &cobra.Command{
	Use:           "jtunnel",
	Short:         "A small TCP forwarding and mTLS tunnel tool",
	SilenceUsage:  true,
	SilenceErrors: true,
	Long: `JTunnel provides a local SOCKS5 proxy over an mTLS-protected yamux tunnel,
and can also forward a local TCP port directly without a tunnel server.

Common usage:
  jtunnel -L tcp://:8080/10.0.0.5:80
  jtunnel -L tcp://127.0.0.1:8080/10.0.0.5:80 -L tcp://127.0.0.1:5432/db.internal:5432
  jtunnel server -l :23336
  jtunnel client example.com:23336 -l 127.0.0.1:1080
  jtunnel forward 8080 10.0.0.5:80
  jtunnel relay -l :23337 -n example.com:23336`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		if len(serviceURLs) == 0 {
			return cmd.Help()
		}

		services := make([]forward.Service, 0, len(serviceURLs))
		for index, value := range serviceURLs {
			service, err := forward.ParseServiceURL(value)
			if err != nil {
				return fmt.Errorf("-L #%d: %w", index+1, err)
			}
			services = append(services, service)
		}
		return forward.StartServices(services, serviceDebug, serviceTimeout)
	},
}

func RunJtunnel() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}

func init() {
	rootCmd.Flags().StringArrayVarP(&serviceURLs, "listen", "L", nil, "本地服务URL，可重复指定（例如 tcp://:8080/10.0.0.5:80）")
	rootCmd.Flags().BoolVarP(&serviceDebug, "debug", "D", false, "输出直接转发的调试日志")
	rootCmd.Flags().IntVarP(&serviceTimeout, "timeout", "t", 10, "连接目标的超时时间（秒）")

	// 添加服务器命令
	rootCmd.AddCommand(ServerCmd)

	// 添加客户端命令
	rootCmd.AddCommand(ClientCmd)

	// 添加端点命令
	rootCmd.AddCommand(RelayCmd)

	// 添加直接TCP端口转发命令
	rootCmd.AddCommand(ForwardCmd)
}
