package engine

import (
	"errors"
	"strings"
)

// Guided 是带处理办法的报错。同一种情况在命令行与网页界面上的处理入口不同：
// Error 给出命令行的写法，WebText 给出网页界面上的入口。
type Guided struct {
	// Msg 说明对象、状态与原因。
	Msg string
	// CLI 是命令行上的处理办法，接在 Msg 之后，含开头的标点。
	CLI string
	// Web 是网页界面上的处理办法，写法同 CLI。
	Web string
}

func (g *Guided) Error() string { return g.Msg + g.CLI }

// WebText 是显示在网页界面上的说明。
func (g *Guided) WebText() string { return g.Msg + g.Web }

// WebErrorText 返回显示在网页界面上的报错：err 中带有 Guided 时，把其中命令行的处理办法换成界面上的入口，
// 外层添加的上下文保持不变；其他报错原样返回。
func WebErrorText(err error) string {
	var g *Guided
	if errors.As(err, &g) {
		return strings.Replace(err.Error(), g.Error(), g.WebText(), 1)
	}
	return err.Error()
}
