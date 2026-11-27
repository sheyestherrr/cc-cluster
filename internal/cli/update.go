package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"

	"github.com/sheyestherrr/cc-cluster/internal/store"
	"github.com/sheyestherrr/cc-cluster/internal/update"
	"github.com/sheyestherrr/cc-cluster/internal/web"
)

// 测试时换成指向假发布页的客户端和临时目录里的文件。
var (
	newUpdateClient = func() *update.Client { return update.NewClient(Version) }
	updateTarget    = update.Target
)

// cmdUpdate 把 cc-cluster 自己换成发布的新版本。
//
// 它替换的只有二进制这一个文件。槽位、节点、订阅、身份和设置都不动，容器不停也不重建。
// 镜像的设置只有明说了 --images 才换：换了之后运行中的槽位会变成“配置待应用”，
// 要等网关重启、worker 重建，所以留给使用者挑时间。
func (a *app) cmdUpdate(ctx context.Context, args []string) error {
	fs := a.newFlags("update", "update [--check] [--version 版本] [--images]")
	check := fs.Bool("check", false, "只看有没有新版本，不更新")
	want := fs.String("version", "", "换成这个版本（如 v0.0.3），而不是最新的正式版")
	images := fs.Bool("images", false, "顺带把数据目录里设置的发布镜像换成同一个版本的（先拉取到本机）；运行中的容器不受影响，重建后才用上")
	if err := parse(fs, args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		fs.Usage()
		return errUsage
	}
	if *want != "" && !update.IsRelease(*want) {
		return fmt.Errorf("版本号 %q 不对：应当是 v0.0.3 这样的写法", *want)
	}
	cl := newUpdateClient()
	fromSource := !update.IsRelease(Version)

	target := *want
	if target == "" {
		cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		res, err := cl.Check(cctx, Version)
		cancel()
		if err != nil {
			return fmt.Errorf("查不到最新的版本：%w", err)
		}
		target = res.Latest.Version
		switch {
		case fromSource:
			fmt.Fprintf(a.out, "这个 cc-cluster 是从源码构建的（%s），最新发布的是 %s。\n", Version, target)
		case !res.Newer:
			fmt.Fprintf(a.out, "已经是最新的版本 %s。\n", Version)
			target = Version
		default:
			fmt.Fprintf(a.out, "当前版本 %s，最新的是 %s。\n", Version, target)
			for _, r := range res.Releases {
				a.printRelease(r)
			}
		}
	}
	if *check {
		if !fromSource && target != Version {
			fmt.Fprintln(a.out, "要更新：cc-cluster update")
		}
		return nil
	}
	if fromSource {
		return errors.New("从源码构建的不自动更新：在源码目录里 git pull 之后重新 make release；想换成发布版就运行安装脚本")
	}

	if target != Version {
		exe, err := updateTarget()
		if err != nil {
			return fmt.Errorf("找不到正在运行的 cc-cluster 的位置：%w", err)
		}
		fmt.Fprintf(a.out, "下载 %s.tar.gz 并核对校验和 …\n", update.AssetName(target, runtime.GOOS, runtime.GOARCH))
		staged, err := cl.Stage(ctx, target, exe)
		if err != nil {
			return fmt.Errorf("%w（什么都没有改动）", err)
		}
		if err := staged.Commit(); err != nil {
			return fmt.Errorf("%w（原来的 cc-cluster 没有改动）", err)
		}
		fmt.Fprintf(a.out, "已更新：%s 现在是 %s。槽位、节点、订阅和设置都没有改动，运行中的容器不受影响。\n", exe, target)
		a.restartWebAfterUpdate(ctx, exe, target)
	} else if *want != "" {
		fmt.Fprintf(a.out, "现在就是 %s。\n", Version)
	}
	return a.updateImages(ctx, target, *images)
}

// printRelease 打印一个版本的更新说明（发布页上写的那一段的中文部分）。
func (a *app) printRelease(r update.Release) {
	title := r.Version
	if !r.PublishedAt.IsZero() {
		title += "（" + r.PublishedAt.Local().Format("2006-01-02") + " 发布）"
	}
	fmt.Fprintf(a.out, "\n── %s ──\n", title)
	if notes := update.LocalNotes(r.Notes); notes != "" {
		fmt.Fprintln(a.out, notes)
	} else {
		fmt.Fprintf(a.out, "更新说明见 %s\n", r.URL)
	}
	fmt.Fprintln(a.out)
}

// restartWebAfterUpdate 在后台的网页界面还是旧版本时，让新的二进制把它换掉（端口和令牌不变）。
// 做不到只提醒：二进制已经换好了。
func (a *app) restartWebAfterUpdate(ctx context.Context, exe, v string) {
	if a.dataDir == "" {
		return
	}
	r, ok := runningWeb(a.dataDir)
	if !ok || r.Version == v {
		return
	}
	dir, err := filepath.Abs(a.dataDir)
	if err == nil {
		cmd := exec.CommandContext(ctx, exe, "--data-dir", dir, "web")
		cmd.Stdout, cmd.Stderr = a.out, a.errw
		fmt.Fprintln(a.out)
		err = cmd.Run()
	}
	if err != nil {
		fmt.Fprintf(a.errw, "后台的网页界面还是旧版本（%v）：执行 cc-cluster web 换成新的。\n", err)
		return
	}
	fmt.Fprintln(a.out, "浏览器里开着的页面刷新一下就是新版本。")
}

// updateImages 处理镜像：没有要求时只在设置落后于 v 时提一句；要求了就拉取并改设置。
func (a *app) updateImages(ctx context.Context, v string, change bool) error {
	if a.dataDir == "" {
		return nil
	}
	st, err := store.Open(a.dataDir)
	if err != nil {
		if change {
			return err
		}
		return nil // 数据目录还没有初始化：没有镜像设置可言
	}
	c, err := st.Cluster()
	if err != nil {
		if change {
			return err
		}
		return nil
	}
	from, behind := update.ImagesBehind(c.GatewayImage, c.WorkerImage, v)
	if !behind {
		if change {
			fmt.Fprintf(a.out, "镜像的设置不用换：网关 %s，worker %s。\n", c.GatewayImage, c.WorkerImage)
		}
		return nil
	}
	if !change {
		fmt.Fprintf(a.out, "\n镜像的设置仍是 %s 的，照常使用不受影响。想换成 %s 的，挑一个槽位里没在干活的时候执行：\n  %s\n", from, v, a.selfCommand("update --images"))
		return nil
	}
	eng, _, err := a.open(ctx, true)
	if err != nil {
		return fmt.Errorf("镜像没有换：%w", err)
	}
	fmt.Fprintf(a.out, "\n拉取 %s 的镜像（worker 镜像比较大，要等一会儿）…\n", v)
	if _, err := eng.UseReleaseImages(ctx, v); err != nil {
		return fmt.Errorf("镜像没有换，设置保持原样：%w", err)
	}
	c = eng.Cluster()
	fmt.Fprintf(a.out, "网关镜像：%s\nworker 镜像：%s\n", c.GatewayImage, c.WorkerImage)
	fmt.Fprintln(a.out, "已经在运行的容器仍在用原来的镜像，槽位会显示“配置待应用”：网关停止后再启动、槽位重建 worker 之后换成新的（网页里各自的页面上有这两个操作；HOME 和身份都保留）。")
	return nil
}

// selfCommand 是让使用者照着执行的命令；数据目录不是默认的那个、环境变量里也没有指定时带上 --data-dir。
func (a *app) selfCommand(rest string) string {
	dir, err := filepath.Abs(a.dataDir)
	if err != nil || os.Getenv(EnvDataDir) != "" {
		return "cc-cluster " + rest
	}
	return web.SelfCommand(dir, rest)
}
