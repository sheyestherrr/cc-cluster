package cli

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/sheyestherrr/cc-cluster/internal/model"
	"github.com/sheyestherrr/cc-cluster/internal/update"
	"github.com/sheyestherrr/cc-cluster/internal/update/updatetest"
)

// updateEnv 是一次更新测试的现场：假的发布页、一个“装好的旧二进制”和一个有内容的数据目录。
type updateEnv struct {
	t      *testing.T
	hub    *updatetest.Hub
	target string
	data   string
}

// newUpdateEnv 把本程序的版本设成 current，并准备好现场；测试结束时恢复。
func newUpdateEnv(t *testing.T, current string) *updateEnv {
	t.Helper()
	e := &updateEnv{t: t, hub: updatetest.New(t, update.Repo), target: filepath.Join(t.TempDir(), "cc-cluster"), data: filepath.Join(t.TempDir(), "data")}
	oldVersion, oldClient, oldTarget := Version, newUpdateClient, updateTarget
	t.Cleanup(func() { Version, newUpdateClient, updateTarget = oldVersion, oldClient, oldTarget })
	Version = current
	newUpdateClient = func() *update.Client {
		c := update.NewClient(Version)
		c.API, c.Site = e.hub.URL, e.hub.URL
		return c
	}
	updateTarget = func() (string, error) { return e.target, nil }
	if err := os.WriteFile(e.target, updatetest.Script(current), 0o755); err != nil {
		t.Fatal(err)
	}
	// 数据目录：按这个版本初始化，再放进几样使用者的东西。
	if out, code := e.run("init", "--name", "t"); code != 0 {
		t.Fatalf("init 失败: %s", out)
	}
	for name, body := range map[string]string{
		"slots/a/home/.claude/credentials.json": `{"k":"v"}`,
		"slots/a/home/work/notes.txt":           "使用者的文件",
		"web.token":                             strings.Repeat("ab", 16) + "\n",
		"nodes.d/home.json":                     `{"id":"home"}`,
	} {
		p := filepath.Join(e.data, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return e
}

func (e *updateEnv) run(args ...string) (string, int) {
	var out, errOut strings.Builder
	code := Run(append([]string{"--data-dir", e.data}, args...), strings.NewReader(""), &out, &errOut)
	return out.String() + errOut.String(), code
}

// publish 发布一个版本：登记到接口里，并放上本平台的包。
func (e *updateEnv) publish(v string, day int, notes string) {
	e.hub.Add(v, day, notes)
	e.hub.Publish(e.t, v, runtime.GOOS, runtime.GOARCH, updatetest.Script(v))
}

// snapshot 记下数据目录里每个文件的路径、类型与权限、修改时间和内容摘要。
func (e *updateEnv) snapshot() string {
	e.t.Helper()
	var b strings.Builder
	err := filepath.WalkDir(e.data, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		fi, err := d.Info()
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(e.data, p)
		fmt.Fprintf(&b, "%s %v", rel, fi.Mode())
		if fi.Mode().IsRegular() {
			body, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			fmt.Fprintf(&b, " %d %x", fi.ModTime().UnixNano(), sha256.Sum256(body))
		}
		b.WriteByte('\n')
		return nil
	})
	if err != nil {
		e.t.Fatal(err)
	}
	return b.String()
}

func (e *updateEnv) binary() string {
	b, err := os.ReadFile(e.target)
	if err != nil {
		e.t.Fatal(err)
	}
	return string(b)
}

func (e *updateEnv) cluster() model.Cluster {
	var c model.Cluster
	b, err := os.ReadFile(filepath.Join(e.data, "cluster.json"))
	if err != nil || json.Unmarshal(b, &c) != nil {
		e.t.Fatalf("读不了集群设置: %v", err)
	}
	return c
}

func TestUpdateReplacesOnlyTheBinary(t *testing.T) {
	e := newUpdateEnv(t, "v0.0.2")
	e.publish("v0.0.4", 28, "第四版\n- 修了一处\n\n---\n\nFourth")
	e.publish("v0.0.3", 27, "第三版")
	e.hub.Add("v0.0.2", 25, "第二版")
	before := e.snapshot()

	// 只查看：说明要经过的两个版本，什么都不换。
	out, code := e.run("update", "--check")
	if code != 0 || !strings.Contains(out, "当前版本 v0.0.2，最新版本 v0.0.4") || !strings.Contains(out, "第三版") || !strings.Contains(out, "修了一处") {
		t.Fatalf("--check 的输出不对: %d %s", code, out)
	}
	if strings.Contains(out, "Fourth") || strings.Contains(out, "第二版") {
		t.Fatalf("只应显示新版本说明的中文部分: %s", out)
	}
	// 数据目录不是默认的那个：给出的命令带着它。
	if !strings.Contains(out, "cc-cluster --data-dir "+e.data+" update\n") {
		t.Fatalf("应给出能照着执行的更新命令: %s", out)
	}
	if e.binary() != string(updatetest.Script("v0.0.2")) || e.snapshot() != before {
		t.Fatal("--check 不应改动任何东西")
	}

	out, code = e.run("update")
	if code != 0 || !strings.Contains(out, "已更新至 v0.0.4") {
		t.Fatalf("更新失败: %d %s", code, out)
	}
	if e.binary() != string(updatetest.Script("v0.0.4")) {
		t.Fatalf("二进制应换成新版本: %q", e.binary())
	}
	if entries, _ := os.ReadDir(filepath.Dir(e.target)); len(entries) != 1 {
		t.Fatalf("二进制旁边不应留下别的文件: %v", entries)
	}
	// 最要紧的一条：数据目录里的每个文件，内容、权限、修改时间都和更新之前一样。
	if after := e.snapshot(); after != before {
		t.Fatalf("更新不应碰数据目录:\n之前:\n%s之后:\n%s", before, after)
	}
	// 镜像的设置没有动，只提醒有这回事。
	if c := e.cluster(); !strings.HasSuffix(c.GatewayImage, ":v0.0.2") || !strings.HasSuffix(c.WorkerImage, ":v0.0.2") {
		t.Fatalf("镜像的设置不应自动更换: %+v", c)
	}
	if !strings.Contains(out, "镜像设置仍为 v0.0.2") || !strings.Contains(out, "update --images") {
		t.Fatalf("应提醒镜像可以另外更换: %s", out)
	}
}

func TestUpdateWhenNothingToDo(t *testing.T) {
	e := newUpdateEnv(t, "v0.0.3")
	e.publish("v0.0.3", 27, "第三版")
	before := e.snapshot()
	out, code := e.run("update")
	if code != 0 || !strings.Contains(out, "已是最新版本（v0.0.3）") || strings.Contains(out, "镜像") {
		t.Fatalf("已是最新时: %d %s", code, out)
	}
	if e.binary() != string(updatetest.Script("v0.0.3")) || e.snapshot() != before {
		t.Fatal("已是最新时不应改动任何东西")
	}
}

func TestUpdateFailureChangesNothing(t *testing.T) {
	e := newUpdateEnv(t, "v0.0.2")
	e.publish("v0.0.3", 27, "第三版")
	pkg := "v0.0.3/" + updatetest.PackageName("v0.0.3", runtime.GOOS, runtime.GOARCH) + ".tar.gz"
	e.hub.Files[pkg] = append([]byte{}, e.hub.Files[pkg][:len(e.hub.Files[pkg])-1]...)
	before := e.snapshot()

	out, code := e.run("update")
	if code == 0 || !strings.Contains(out, "校验和不匹配") || !strings.Contains(out, "未做任何更改") {
		t.Fatalf("下载坏了应当失败并说明: %d %s", code, out)
	}
	if e.binary() != string(updatetest.Script("v0.0.2")) || e.snapshot() != before {
		t.Fatal("失败时二进制和数据目录都应保持原样")
	}
	if entries, _ := os.ReadDir(filepath.Dir(e.target)); len(entries) != 1 {
		t.Fatalf("失败时不应留下临时文件: %v", entries)
	}

	// 发布页连不上。
	e.hub.Close()
	if out, code := e.run("update"); code == 0 || !strings.Contains(out, "检查更新失败") {
		t.Fatalf("连不上时应说明: %d %s", code, out)
	}
	if e.binary() != string(updatetest.Script("v0.0.2")) || e.snapshot() != before {
		t.Fatal("连不上时什么都不应改动")
	}
}

func TestUpdateRefusesSourceBuilds(t *testing.T) {
	e := newUpdateEnv(t, "v0.0.2-3-g768d650")
	e.publish("v0.0.3", 27, "第三版")
	if out, code := e.run("update", "--check"); code != 0 || !strings.Contains(out, "源码构建") || !strings.Contains(out, "v0.0.3") || strings.Contains(out, "执行以下命令") {
		t.Fatalf("源码构建也应能查看最新版本，但不提示更新命令: %d %s", code, out)
	}
	out, code := e.run("update")
	if code == 0 || !strings.Contains(out, "无法自动更新") {
		t.Fatalf("源码构建不应被自动换掉: %d %s", code, out)
	}
	if e.binary() != string(updatetest.Script("v0.0.2-3-g768d650")) {
		t.Fatal("源码构建的二进制不应被改动")
	}
}

func TestUpdateToGivenVersion(t *testing.T) {
	e := newUpdateEnv(t, "v0.0.3")
	e.publish("v0.0.3", 27, "第三版")
	e.publish("v0.0.2", 25, "第二版")
	for _, bad := range []string{"latest", "0.0.2", "v0.0.2/../x"} {
		if out, code := e.run("update", "--version", bad); code == 0 || !strings.Contains(out, "无效") {
			t.Fatalf("版本号 %q 应被拒绝: %d %s", bad, code, out)
		}
	}
	// --check 查的是最新版本，和指定版本一起用没有意义。
	if out, code := e.run("update", "--check", "--version", "v0.0.2"); code == 0 || !strings.Contains(out, "不能同时使用") {
		t.Fatalf("--check 与 --version 不应同时接受: %d %s", code, out)
	}
	// 已经是这个版本。
	if out, code := e.run("update", "--version", "v0.0.3"); code != 0 || !strings.Contains(out, "无需更新") || e.binary() != string(updatetest.Script("v0.0.3")) {
		t.Fatalf("指定的就是当前版本时不应有动作: %d %s", code, out)
	}
	// 明确指定时可以换回旧版本。镜像的设置比它新，不提示“更新镜像”。
	out, code := e.run("update", "--version", "v0.0.2")
	if code != 0 || e.binary() != string(updatetest.Script("v0.0.2")) {
		t.Fatalf("应换成指定的版本: %d %s", code, out)
	}
	if strings.Contains(out, "镜像") {
		t.Fatalf("退回旧版本时不应提示更新镜像: %s", out)
	}
	// 指定的版本没有发布过。
	if out, code := e.run("update", "--version", "v0.9.9"); code == 0 || !strings.Contains(out, "未找到版本 v0.9.9") || !strings.Contains(out, "未做任何更改") {
		t.Fatalf("不存在的版本应当失败: %d %s", code, out)
	}
}

// webRecord 在数据目录里放一份“网页界面正在运行”的记录：进程用父进程冒充，端口真的有人在听。
func webRecord(t *testing.T, dataDir string, r webRun) {
	t.Helper()
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	r.PID, r.Port, r.Token = os.Getppid(), ln.Addr().(*net.TCPAddr).Port, "t"
	b, _ := json.Marshal(r)
	if err := os.WriteFile(webRunPath(dataDir), b, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestUpdateRestartsStaleWeb(t *testing.T) {
	e := newUpdateEnv(t, "v0.0.3")
	e.publish("v0.0.3", 27, "第三版")
	// 冒充二进制的脚本把收到的参数记下来，再照常打印版本号。
	args := filepath.Join(t.TempDir(), "args")
	script := func(tail string) {
		body := "#!/bin/sh\necho \"$@\" >> " + args + "\n" + tail
		if err := os.WriteFile(e.target, []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	called := func() string {
		b, _ := os.ReadFile(args)
		_ = os.Remove(args)
		return strings.TrimSpace(string(b))
	}

	// 二进制已经是最新的，后台的网页界面还是旧版本（用安装脚本升级过，或上次没能重启）：照样重启它。
	script("echo v0.0.3\n")
	webRecord(t, e.data, webRun{Version: "v0.0.2"})
	out, code := e.run("update")
	if code != 0 || !strings.Contains(out, "已是最新版本") || !strings.Contains(out, "网页界面已重启") {
		t.Fatalf("旧版本的网页界面应被重启: %d %s", code, out)
	}
	if got := called(); got != "--data-dir "+e.data+" web" {
		t.Fatalf("应由二进制自己重启网页界面: %q", got)
	}

	// 版本相同：不动。
	webRecord(t, e.data, webRun{Version: "v0.0.3"})
	if out, _ := e.run("update"); strings.Contains(out, "网页界面") || called() != "" {
		t.Fatalf("版本相同时不应重启: %s", out)
	}

	// 以前台方式运行的（由别的程序管着）：只提示，不替它重启。
	webRecord(t, e.data, webRun{Version: "v0.0.2", Foreground: true})
	if out, _ := e.run("update"); !strings.Contains(out, "请手动重启") || called() != "" {
		t.Fatalf("前台运行的网页界面不应被重启: %s", out)
	}

	// 重启失败：说明原因和要执行的命令，更新本身不算失败；带令牌的地址不出现在输出里。
	script("echo 'http://127.0.0.1:1/#token=abc'\necho '错误：端口被占用' >&2\nexit 1\n")
	webRecord(t, e.data, webRun{Version: "v0.0.2"})
	out, code = e.run("update")
	if code != 0 || !strings.Contains(out, "网页界面重启失败：端口被占用") || !strings.Contains(out, "cc-cluster --data-dir "+e.data+" web") {
		t.Fatalf("重启失败时应说明: %d %s", code, out)
	}
	if strings.Contains(out, "token=") {
		t.Fatalf("不应转述带令牌的地址: %s", out)
	}
}

// fakeDocker 起一个只会应答版本和拉取镜像的假 Docker，并让本进程连到它；返回被拉取过的镜像。
func fakeDocker(t *testing.T, pullError string) *[]string {
	t.Helper()
	// Unix socket 的路径有长度上限，不放进带着测试名的 t.TempDir()。
	dir, err := os.MkdirTemp("", "dk")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	sock := filepath.Join(dir, "d.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	pulled := &[]string{}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/version"):
			_, _ = w.Write([]byte(`{"Version":"test","ApiVersion":"1.44","MinAPIVersion":"1.24"}`))
		case strings.HasSuffix(r.URL.Path, "/images/create") && r.Method == http.MethodPost:
			*pulled = append(*pulled, r.URL.Query().Get("fromImage")+":"+r.URL.Query().Get("tag"))
			if pullError != "" {
				_, _ = w.Write([]byte(`{"error":"` + pullError + `"}` + "\n"))
				return
			}
			_, _ = w.Write([]byte(`{"status":"Status: Image is up to date"}` + "\n"))
		default:
			http.NotFound(w, r)
		}
	})}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	t.Setenv("DOCKER_HOST", "unix://"+sock)
	return pulled
}

func TestUpdateImagesOnlyWhenAsked(t *testing.T) {
	e := newUpdateEnv(t, "v0.0.2")
	e.publish("v0.0.3", 27, "第三版")
	pulled := fakeDocker(t, "")

	out, code := e.run("update", "--images")
	if code != 0 {
		t.Fatalf("update --images 失败: %s", out)
	}
	gw, wk := update.Images("v0.0.3")
	if c := e.cluster(); c.GatewayImage != gw || c.WorkerImage != wk || c.Name != "t" {
		t.Fatalf("镜像的设置应换成新版本的，别的不变: %+v", c)
	}
	if strings.Join(*pulled, " ") != gw+" "+wk {
		t.Fatalf("应先把两个镜像拉到本机: %v", *pulled)
	}
	if !strings.Contains(out, "配置待应用") || !strings.Contains(out, "持续检测暂停") {
		t.Fatalf("应说明运行中的容器要重建后才用上，期间不做持续检测: %s", out)
	}
	// 使用者的文件仍然原样。
	if b, _ := os.ReadFile(filepath.Join(e.data, "slots/a/home/work/notes.txt")); string(b) != "使用者的文件" {
		t.Fatalf("槽位里的文件被动了: %q", b)
	}

	// 再来一次：已经是这个版本的，不拉取也不改。
	*pulled = nil
	before := e.snapshot()
	Version = "v0.0.3"
	if out, code := e.run("update", "--images"); code != 0 || !strings.Contains(out, "无需更新") || len(*pulled) != 0 || e.snapshot() != before {
		t.Fatalf("没有可换的时候不应有动作: %d %s %v", code, out, *pulled)
	}
}

func TestUpdateImagesLeavesCustomImagesAndFailuresAlone(t *testing.T) {
	e := newUpdateEnv(t, "v0.0.3")
	e.publish("v0.0.3", 27, "第三版")
	// 网关用的是自己的镜像，worker 是发布的旧版本。
	oldWorker := update.ImageRepo + "worker:v0.0.2"
	if out, code := e.run("images", "--gateway-image", "my/gateway:1", "--worker-image", oldWorker); code != 0 {
		t.Fatal(out)
	}

	// 拉不下来：设置保持原样。
	fakeDocker(t, "manifest unknown")
	out, code := e.run("update", "--images")
	if code == 0 || !strings.Contains(out, "manifest unknown") || !strings.Contains(out, "镜像设置未更改") {
		t.Fatalf("拉取失败应当报出来: %d %s", code, out)
	}
	if c := e.cluster(); c.GatewayImage != "my/gateway:1" || c.WorkerImage != oldWorker {
		t.Fatalf("拉取失败时设置不应改动: %+v", c)
	}

	// 拉得下来：只换发布的那个，自己指定的不碰、也不去拉。
	pulled := fakeDocker(t, "")
	if out, code := e.run("update", "--images"); code != 0 {
		t.Fatal(out)
	}
	_, wk := update.Images("v0.0.3")
	if c := e.cluster(); c.GatewayImage != "my/gateway:1" || c.WorkerImage != wk {
		t.Fatalf("自己指定的镜像不应被换掉: %+v", c)
	}
	if strings.Join(*pulled, " ") != wk {
		t.Fatalf("只应拉取要换成的那个: %v", *pulled)
	}
}

func TestSelfCommand(t *testing.T) {
	t.Setenv(EnvDataDir, "")
	a := &app{dataDir: "/srv/cc data", out: &bytes.Buffer{}}
	if got := a.selfCommand("update"); got != "cc-cluster --data-dir '/srv/cc data' update" {
		t.Fatalf("数据目录不是默认的那个时应带上它: %s", got)
	}
	t.Setenv(EnvDataDir, "/srv/cc data")
	if got := a.selfCommand("update"); got != "cc-cluster update" {
		t.Fatalf("环境变量指定的就是它时不用带: %s", got)
	}
	// 环境变量指的是另一个目录，这次用 --data-dir 换了：不带的话照着执行会改到另一个目录。
	t.Setenv(EnvDataDir, "/srv/other")
	if got := a.selfCommand("update --images"); got != "cc-cluster --data-dir '/srv/cc data' update --images" {
		t.Fatalf("与环境变量指定的不是同一个目录时应带上它: %s", got)
	}
}
