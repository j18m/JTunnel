package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:          "jtunnel",
	Short:        "A small mTLS tunnel with local SOCKS5 proxy support",
	SilenceUsage: true,
	Long: `JTunnel provides a local SOCKS5 proxy over an mTLS-protected yamux tunnel.

Common usage:
  jtunnel server -l :23336
  jtunnel client example.com:23336 -l 127.0.0.1:1080
  jtunnel relay -l :23337 -n example.com:23336`,
}

func RunJtunnel() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}

func init() {
	// 添加服务器命令
	rootCmd.AddCommand(ServerCmd)

	// 添加客户端命令
	rootCmd.AddCommand(ClientCmd)

	// 添加端点命令
	rootCmd.AddCommand(RelayCmd)
}
