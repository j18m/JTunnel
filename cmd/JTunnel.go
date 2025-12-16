package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "Jtunnel",
	Short: "Jtunnel run",
	Long:  "Jtunnel run test",
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

	// 添加节点命令
	rootCmd.AddCommand(NodeCmd)
}
