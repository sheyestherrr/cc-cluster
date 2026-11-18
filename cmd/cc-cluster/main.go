// Command cc-cluster 是 cc-cluster 的管理器入口。
package main

import (
	"os"

	"github.com/sheyestherrr/cc-cluster/internal/cli"
)

func main() {
	os.Exit(cli.Run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}
