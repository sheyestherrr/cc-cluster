package engine

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestGuidedErrorGivesCLIAndWebText(t *testing.T) {
	g := &Guided{Msg: "网关 main 的容器仍存在", CLI: "，请先执行 gateway down main", Web: "，请先停止该网关"}
	if got, want := g.Error(), "网关 main 的容器仍存在，请先执行 gateway down main"; got != want {
		t.Fatalf("Error() = %q，应为 %q", got, want)
	}
	if got, want := WebErrorText(g), "网关 main 的容器仍存在，请先停止该网关"; got != want {
		t.Fatalf("WebErrorText() = %q，应为 %q", got, want)
	}

	// 外层添加的上下文保持不变，只替换处理办法。
	wrapped := fmt.Errorf("删除网关失败：%w", g)
	if got, want := WebErrorText(wrapped), "删除网关失败：网关 main 的容器仍存在，请先停止该网关"; got != want {
		t.Fatalf("WebErrorText(wrapped) = %q，应为 %q", got, want)
	}
	if strings.Contains(WebErrorText(wrapped), "gateway down") {
		t.Fatal("网页界面的报错不应包含命令行的写法")
	}

	// 其他报错原样返回。
	plain := errors.New("本机未找到网关镜像")
	if got := WebErrorText(plain); got != plain.Error() {
		t.Fatalf("WebErrorText(plain) = %q，应原样返回", got)
	}
}
