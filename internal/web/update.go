package web

import (
	"context"
	"path/filepath"
	"sync"
	"time"

	"github.com/sheyestherrr/cc-cluster/internal/update"
)

const (
	// 查到的结果留这么久：页面开着时每隔一阵来问一次，多数时候答的是记着的那份。
	updateFresh = 6 * time.Hour
	// 没查成（连不上、被限流）时过这么久才自己再试。
	updateRetry = 15 * time.Minute
	// 使用者点“重新检查”也不比这更频繁：匿名的接口每小时只有 60 次。
	updateMinGap = time.Minute
	// 查一次最多等这么久。
	updateTimeout = 20 * time.Second
)

// updater 是版本检查在内存里的状态。结果不落盘：数据目录里不为它留任何文件，重启后重新查。
type updater struct {
	mu sync.Mutex
	// checked 是最近一次检查结束的时间。
	checked time.Time
	// running 在检查进行中时非 nil，结束时关闭；同时来的请求等同一次检查，不各查各的。
	running chan struct{}
	res     update.Check
	ok      bool
	err     string
}

type updateView struct {
	Current string `json:"current"`
	// IsRelease 为假表示这是从源码构建的：没法与发布的版本比较，也不自动更新。
	IsRelease bool `json:"is_release"`
	// AutoCheck 为假表示关掉了自动检查，只在使用者点的时候查。
	AutoCheck bool `json:"auto_check"`
	// CheckedAt 是最近一次检查的时间；还没查过时没有。
	CheckedAt *time.Time      `json:"checked_at,omitempty"`
	Latest    *update.Release `json:"latest,omitempty"`
	Newer     bool            `json:"newer"`
	// Releases 是要给使用者看的更新说明，新的在前。
	Releases []update.Release `json:"releases"`
	// Error 是最近一次检查失败的原因。
	Error string `json:"error,omitempty"`
	// Command 是在终端里更新的命令。网页只负责告诉使用者有新版本，更新在终端里做。
	Command     string `json:"command"`
	ReleasesURL string `json:"releases_url"`
	// ImagesFrom 在镜像的设置比这个二进制旧时，是它们现在的版本；ImagesCommand 是更新到同版本的命令。
	ImagesFrom    string `json:"images_from,omitempty"`
	ImagesCommand string `json:"images_command,omitempty"`
}

func (s *Server) updateClient() *update.Client {
	if s.opt.UpdateClient != nil {
		return s.opt.UpdateClient
	}
	return update.NewClient(s.opt.Version)
}

// runUpdateCheck 查一次并记下结果。它不随发起它的那个请求取消：页面刷新或关掉了，
// 结果照样记下来给之后的请求用，也不会把“请求被取消”记成检查失败。
func (s *Server) runUpdateCheck(done chan struct{}) {
	ctx, cancel := context.WithTimeout(context.Background(), updateTimeout)
	res, err := s.updateClient().Check(ctx, s.opt.Version)
	cancel()
	u := &s.upd
	u.mu.Lock()
	defer u.mu.Unlock()
	if err != nil {
		u.err = err.Error() // 之前查到过的结果留着，照样显示
	} else {
		for i := range res.Releases {
			res.Releases[i].Notes = update.LocalNotes(res.Releases[i].Notes)
		}
		res.Latest.Notes = update.LocalNotes(res.Latest.Notes)
		u.res, u.ok, u.err = res, true, ""
	}
	u.checked, u.running = time.Now(), nil
	close(done)
}

// updateInfo 返回版本检查的结果。带 refresh=1 是使用者点了“检查更新”；
// 不带时用记着的那份，过期了（或还没查过）才去查，设置了不自动检查时不查。
// 这个接口只读：它不下载也不替换任何东西。
func (s *Server) updateInfo(c *call) (any, error) {
	force := c.r.URL.Query().Get("refresh") == "1"
	u := &s.upd
	u.mu.Lock()
	wait := u.running
	if wait == nil {
		age := time.Since(u.checked)
		var due bool
		switch {
		case u.checked.IsZero():
			due = force || !s.opt.NoUpdateCheck
		case force:
			due = age >= updateMinGap
		case s.opt.NoUpdateCheck:
			due = false
		case u.err != "":
			due = age >= updateRetry
		default:
			due = age >= updateFresh
		}
		if due {
			wait = make(chan struct{})
			u.running = wait
			go s.runUpdateCheck(wait)
		}
	}
	u.mu.Unlock()
	if wait != nil {
		select {
		case <-wait:
		case <-c.ctx.Done():
		}
	}

	dataDir, err := filepath.Abs(s.opt.DataDir)
	if err != nil {
		dataDir = s.opt.DataDir
	}
	out := updateView{
		Current: s.opt.Version, IsRelease: update.IsRelease(s.opt.Version), AutoCheck: !s.opt.NoUpdateCheck, Releases: []update.Release{},
		Command: SelfCommand(dataDir, "update"), ReleasesURL: update.ReleasesURL,
	}
	if out.IsRelease {
		cl := c.eng.Cluster()
		if from, behind := update.ImagesBehind(cl.GatewayImage, cl.WorkerImage, s.opt.Version); behind {
			out.ImagesFrom, out.ImagesCommand = from, SelfCommand(dataDir, "update --images")
		}
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	if !u.checked.IsZero() {
		t := u.checked
		out.CheckedAt = &t
	}
	out.Error = u.err
	if u.ok {
		latest := u.res.Latest
		out.Latest, out.Newer, out.Releases = &latest, u.res.Newer, u.res.Releases
	}
	return out, nil
}
