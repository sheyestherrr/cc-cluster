package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
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

// 查询最新版本、下载安装包、拉取镜像各自的时限。
const (
	updateCheckTimeout    = 30 * time.Second
	updateDownloadTimeout = 10 * time.Minute
	updatePullTimeout     = time.Hour
)

// cmdUpdate 把 cc-cluster 自己换成发布的新版本。
//
// 它替换的只有二进制这一个文件。槽位、节点、订阅、身份和设置都不动，容器不停也不重建；
// 数据目录里只有网页服务自己的运行记录和日志会随它重启而重写。
// 镜像的设置只有明说了 --images 才换：换了之后运行中的槽位会变成“配置待应用”，
// 持续检测跳过它们，要等网关重启、worker 重建，所以留给使用者挑时间。
func (a *app) cmdUpdate(ctx context.Context, args []string) error {
	fs := a.newFlags("update", "update [--check] [--version 版本] [--images]")
	check := fs.Bool("check", false, "仅检查更新，不安装")
	want := fs.String("version", "", "安装指定版本（如 v0.0.3），默认为最新正式版")
	images := fs.Bool("images", false, "同时将镜像设置更新至该版本（需拉取镜像）；运行中的容器重建后生效")
	if err := parse(fs, args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		fs.Usage()
		return errUsage
	}
	if *want != "" {
		if *check {
			return errors.New("--check 与 --version 不能同时使用")
		}
		if err := update.ValidVersion(*want); err != nil {
			return err
		}
	}
	cl := newUpdateClient()
	fromSource := !update.IsRelease(Version)

	target := *want
	if target == "" {
		cctx, cancel := context.WithTimeout(ctx, updateCheckTimeout)
		res, err := cl.Check(cctx, Version)
		cancel()
		if err != nil {
			return fmt.Errorf("检查更新失败：%w", err)
		}
		target = res.Latest.Version
		switch {
		case fromSource:
			fmt.Fprintf(a.out, "当前为源码构建（%s），最新正式版为 %s。\n", Version, target)
		case !res.Newer:
			fmt.Fprintf(a.out, "已是最新版本（%s）。\n", Version)
			target = Version
		default:
			fmt.Fprintf(a.out, "当前版本 %s，最新版本 %s。\n", Version, target)
			for _, r := range res.Releases {
				a.printRelease(r)
			}
		}
	}
	if *check {
		if !fromSource && target != Version {
			fmt.Fprintf(a.out, "执行以下命令更新：\n  %s\n", a.selfCommand("update"))
		}
		return nil
	}
	if fromSource {
		return errors.New("源码构建的版本无法自动更新，请更新源码后重新构建")
	}

	exe, exeErr := updateTarget()
	if target != Version {
		if exeErr != nil {
			return fmt.Errorf("无法确定 cc-cluster 的安装路径：%w", exeErr)
		}
		fmt.Fprintf(a.out, "正在下载 %s.tar.gz …\n", update.AssetName(target, runtime.GOOS, runtime.GOARCH))
		dctx, cancel := context.WithTimeout(ctx, updateDownloadTimeout)
		staged, err := cl.Stage(dctx, target, exe)
		cancel()
		if err == nil {
			err = staged.Commit()
		}
		if err != nil {
			return fmt.Errorf("更新失败，未做任何更改：%w", err)
		}
		fmt.Fprintf(a.out, "已更新至 %s（%s）。\n槽位、节点、订阅与设置未改动，运行中的容器不受影响。\n", target, exe)
	} else if *want != "" {
		fmt.Fprintf(a.out, "当前版本已是 %s，无需更新。\n", Version)
	}
	if exeErr == nil {
		a.restartWeb(ctx, exe, target)
	}
	return a.updateImages(ctx, target, *images)
}

// printRelease 打印一个版本的更新说明（发布页上写的那一段的中文部分）。
func (a *app) printRelease(r update.Release) {
	title := r.Version
	if !r.PublishedAt.IsZero() {
		title += "（" + r.PublishedAt.Local().Format("2006-01-02") + "）"
	}
	fmt.Fprintf(a.out, "\n── %s ──\n", title)
	if notes := update.LocalNotes(r.Notes); notes != "" {
		fmt.Fprintln(a.out, notes)
	} else {
		fmt.Fprintf(a.out, "更新说明：%s\n", r.URL)
	}
	fmt.Fprintln(a.out)
}

// restartWeb 在后台的网页界面与 exe（版本 v）不是同一个版本时，用 exe 把它重新启动；
// 端口、令牌和启动时的选项由 web 命令从运行记录里沿用。
// 这次没有替换二进制时也检查：用别的方式升级过，或者上一次没能重启，旧的那一份还在运行。
// 以前台方式运行的（交给 systemd 之类管理的）不动，只提示。做不到也只提示：二进制已经换好了。
func (a *app) restartWeb(ctx context.Context, exe, v string) {
	if a.dataDir == "" {
		return
	}
	r, ok := runningWeb(a.dataDir)
	if !ok || r.Version == v {
		return
	}
	if r.Foreground {
		fmt.Fprintf(a.out, "网页界面以前台方式运行，请手动重启以使用 %s。\n", v)
		return
	}
	dir, err := filepath.Abs(a.dataDir)
	if err == nil {
		// 它会打印带令牌的访问地址，这里用不着；出错时取它的说明。
		var msg bytes.Buffer
		cmd := exec.CommandContext(ctx, exe, "--data-dir", dir, "web")
		cmd.Stdout, cmd.Stderr = io.Discard, &msg
		if err = cmd.Run(); err != nil && msg.Len() > 0 {
			err = errors.New(strings.TrimPrefix(strings.TrimSpace(msg.String()), "错误："))
		}
	}
	if err != nil {
		fmt.Fprintf(a.errw, "网页界面重启失败：%v\n请手动执行：%s\n", err, a.selfCommand("web"))
		return
	}
	fmt.Fprintln(a.out, "网页界面已重启，请刷新已打开的页面。")
}

// updateImages 处理镜像：没有要求时只在设置比 v 旧时提示一句；要求了就拉取并改设置。
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
	if !change {
		if from, behind := update.ImagesBehind(c.GatewayImage, c.WorkerImage, v); behind {
			fmt.Fprintf(a.out, "\n镜像设置仍为 %s，可继续使用。\n如需更新至 %s，请在槽位空闲时执行（之后需重启网关、重建 worker）：\n  %s\n", from, v, a.selfCommand("update --images"))
		}
		return nil
	}
	if _, differs := update.ImagesVersion(c.GatewayImage, c.WorkerImage, v); !differs {
		fmt.Fprintf(a.out, "镜像设置无需更新：网关 %s，worker %s。\n", c.GatewayImage, c.WorkerImage)
		return nil
	}
	const failed = "镜像更新失败，镜像设置未更改：%w"
	eng, _, err := a.open(ctx, true)
	if err != nil {
		return fmt.Errorf(failed, err)
	}
	fmt.Fprintf(a.out, "\n正在拉取 %s 镜像，可能需要几分钟 …\n", v)
	pctx, cancel := context.WithTimeout(ctx, updatePullTimeout)
	_, err = eng.UseReleaseImages(pctx, v)
	timedOut := pctx.Err() == context.DeadlineExceeded
	cancel()
	if err != nil {
		if timedOut {
			err = errors.New("拉取镜像超时")
		}
		return fmt.Errorf(failed, err)
	}
	c = eng.Cluster()
	fmt.Fprintf(a.out, "镜像设置已更新：\n  网关    %s\n  worker  %s\n", c.GatewayImage, c.WorkerImage)
	fmt.Fprintln(a.out, "运行中的容器仍使用原镜像，所在槽位显示“配置待应用”，持续检测暂停。重启网关、重建 worker 后生效（HOME 与身份保留）。")
	return nil
}

// selfCommand 是让使用者照着执行的命令。数据目录不是这个终端里不带 --data-dir 时会用的那个
// （环境变量指定的，没有指定就是默认的）时，带上 --data-dir。
func (a *app) selfCommand(rest string) string {
	dir, err := filepath.Abs(a.dataDir)
	if err != nil {
		dir = a.dataDir
	}
	implicit := store.DefaultDir()
	if env := os.Getenv(EnvDataDir); env != "" {
		implicit = env
		if abs, err := filepath.Abs(env); err == nil {
			implicit = abs
		}
	}
	return web.CommandFor(dir, implicit, rest)
}
