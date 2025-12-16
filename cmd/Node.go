package cmd

import (
	"JTunnel/client"
	"JTunnel/relay"
	"fmt"
	"log"

	"github.com/spf13/cobra"
)

// 定义命令行参数变量
var (
	nodeListenAddr string // 节点监听地址（仅用于relay类型）
	nodeNextAddr   string // 下一个节点地址
	nodeSocksPort  int    // SOCKS5端口（仅用于client类型）
	nodeUsername   string // 认证用户名
	nodePassword   string // 认证密码
	nodeDebugMode  bool   // 调试模式
	nodeTimeout    int    // 超时时间（秒）
)

// NodeCmd 表示节点命令
var NodeCmd = &cobra.Command{
	Use:   "node [type]",
	Short: "启动JTunnel节点，支持client和relay类型",
	Long: `JTunnel节点命令，用于启动不同类型的隧道节点

类型说明:
  client - 客户端节点，作为隧道的最后一个节点，负责发起实际网络请求
  relay  - 中继节点，负责转发流量到下一个节点

示例:
  # 启动客户端节点
  jtunnel node client --next 127.0.0.1:23338 --socks-port 2337
  
  # 启动中继节点
  jtunnel node relay --listen 0.0.0.0:23337 --next 127.0.0.1:23338
	`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		nodeType := args[0]
		
		if nodeType != "client" && nodeType != "relay" {
			log.Fatal("节点类型必须是 'client' 或 'relay'")
		}
		
		if nodeNextAddr == "" {
			log.Fatal("必须指定下一个节点地址")
		}
		
		if nodeType == "client" {
			if nodeSocksPort <= 0 || nodeSocksPort > 65535 {
				log.Fatal("SOCKS5端口必须在1-65535范围内")
			}
			
			fmt.Printf("启动客户端节点，连接到: %s，SOCKS5端口: %d\n", nodeNextAddr, nodeSocksPort)
			client.StartClient(nodeNextAddr, nodeSocksPort, nodeUsername, nodePassword, nodeDebugMode, nodeTimeout)
		} else if nodeType == "relay" {
			if nodeListenAddr == "" {
				log.Fatal("中继节点必须指定监听地址")
			}
			
			fmt.Printf("启动中继节点，监听地址: %s，转发到: %s\n", nodeListenAddr, nodeNextAddr)
			relay.StartRelay(nodeListenAddr, nodeNextAddr, nodeDebugMode, nodeTimeout)
		}
	},
}

func init() {
	// 添加命令行参数
	NodeCmd.Flags().StringVarP(&nodeListenAddr, "listen", "l", "", "节点监听地址 (格式: host:port，仅用于relay类型)")
	NodeCmd.Flags().StringVarP(&nodeNextAddr, "next", "n", "", "下一个节点地址 (格式: host:port)")
	NodeCmd.Flags().IntVarP(&nodeSocksPort, "socks-port", "s", 0, "SOCKS5端口 (仅用于client类型)")
	NodeCmd.Flags().StringVarP(&nodeUsername, "username", "u", "", "认证用户名")
	NodeCmd.Flags().StringVarP(&nodePassword, "password", "p", "", "认证密码")
	NodeCmd.Flags().BoolVarP(&nodeDebugMode, "debug", "d", false, "开启调试模式")
	NodeCmd.Flags().IntVarP(&nodeTimeout, "timeout", "t", 10, "超时时间（秒）")
}