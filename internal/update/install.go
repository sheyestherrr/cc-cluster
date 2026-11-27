package update

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const (
	maxSumsSize    = 1 << 20
	maxArchiveSize = 256 << 20
	maxBinarySize  = 512 << 20
)

// Target 返回正在运行的二进制的真实路径（跟过符号链接），更新时替换的就是它。
func Target() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(exe)
}

// AssetName 是一个平台的发布包去掉 .tar.gz 之后的名字，也是包里那一层目录的名字。
func AssetName(v, goos, goarch string) string {
	return fmt.Sprintf("cc-cluster_%s_%s_%s", v, goos, goarch)
}

// Staged 是已经下载、核对过、放在目标旁边但还没有替换上去的新二进制。
// 走到这一步什么都还没变：Commit 才替换，Discard 把它删掉。
type Staged struct {
	Version string
	// Target 是将要被替换的文件。
	Target string
	tmp    string
}

// Stage 下载 v 这个版本在本平台的发布包，按同一个发布里的 SHA256SUMS 核对，
// 取出其中的 cc-cluster 放到 target 所在的目录（同一个文件系统，之后的替换才是原子的），并运行一次确认它报告的就是这个版本。
// 任何一步失败都不留下文件，target 不变。
func (c *Client) Stage(ctx context.Context, v, target string) (*Staged, error) {
	return c.stage(ctx, v, target, runtime.GOOS, runtime.GOARCH)
}

func (c *Client) stage(ctx context.Context, v, target, goos, goarch string) (_ *Staged, err error) {
	if _, ok := parseVersion(v); !ok {
		return nil, fmt.Errorf("版本号 %q 不对：应当是 v0.0.3 这样的写法", v)
	}
	name := AssetName(v, goos, goarch)
	base := c.Site + "/" + Repo + "/releases/download/" + v + "/"

	sums, err := c.download(ctx, base+"SHA256SUMS", maxSumsSize)
	if err != nil {
		return nil, fmt.Errorf("下载校验和文件：%w", err)
	}
	want := findSum(sums, name+".tar.gz")
	if want == "" {
		return nil, fmt.Errorf("%s 的发布里没有 %s/%s 的包", v, goos, goarch)
	}
	archive, err := c.download(ctx, base+name+".tar.gz", maxArchiveSize)
	if err != nil {
		return nil, fmt.Errorf("下载 %s.tar.gz：%w", name, err)
	}
	sum := sha256.Sum256(archive)
	if got := hex.EncodeToString(sum[:]); got != want {
		return nil, fmt.Errorf("%s.tar.gz 的校验和对不上，下载的文件可能不完整（应为 %s，得到 %s）", name, want, got)
	}

	dir := filepath.Dir(target)
	f, err := os.CreateTemp(dir, ".cc-cluster-*.new")
	if err != nil {
		if errors.Is(err, os.ErrPermission) {
			return nil, fmt.Errorf("没有权限写 %s：用安装它的那个用户执行，或者重新运行安装脚本", dir)
		}
		return nil, err
	}
	tmp := f.Name()
	defer func() {
		if err != nil {
			f.Close()
			os.Remove(tmp)
		}
	}()
	if err = extractBinary(archive, name+"/cc-cluster", f); err != nil {
		return nil, err
	}
	if err = f.Chmod(0o755); err != nil {
		return nil, err
	}
	if err = f.Sync(); err != nil {
		return nil, err
	}
	if err = f.Close(); err != nil {
		return nil, err
	}
	// 新的二进制跑不起来（坏包、架构不对、目录不让执行）就不替换。
	vctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	out, rerr := exec.CommandContext(vctx, tmp, "version").Output()
	if rerr != nil {
		err = fmt.Errorf("下载到的 cc-cluster 运行不了，没有替换：%w", rerr)
		return nil, err
	}
	if got := strings.TrimSpace(string(out)); got != v {
		err = fmt.Errorf("下载到的 cc-cluster 报告的版本是 %q，不是 %s，没有替换", got, v)
		return nil, err
	}
	return &Staged{Version: v, Target: target, tmp: tmp}, nil
}

// Commit 用新的二进制替换目标：同一个目录里的一次改名，要么是旧的要么是新的，不会有写了一半的文件。
// 正在运行的进程继续用它已经打开的旧文件，不受影响。
func (s *Staged) Commit() error {
	if s.tmp == "" {
		return errors.New("已经替换过或放弃了")
	}
	// 沿用原来的权限位（至少可执行）。
	if fi, err := os.Stat(s.Target); err == nil {
		_ = os.Chmod(s.tmp, fi.Mode().Perm()|0o100)
	}
	if err := os.Rename(s.tmp, s.Target); err != nil {
		os.Remove(s.tmp)
		s.tmp = ""
		return fmt.Errorf("替换 %s：%w", s.Target, err)
	}
	s.tmp = ""
	if d, err := os.Open(filepath.Dir(s.Target)); err == nil {
		_ = d.Sync()
		d.Close()
	}
	return nil
}

// Discard 删掉还没替换上去的新二进制；Commit 之后调用什么也不做。
func (s *Staged) Discard() {
	if s != nil && s.tmp != "" {
		os.Remove(s.tmp)
		s.tmp = ""
	}
}

// download 把一个地址的内容读进内存，超过 limit 就报错。
func (c *Client) download(ctx context.Context, url string, limit int64) ([]byte, error) {
	resp, err := c.get(ctx, url, "")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, fmt.Errorf("下载中断：%w", err)
	}
	if int64(len(b)) > limit {
		return nil, fmt.Errorf("内容超过了 %d MB，不像是发布的文件", limit>>20)
	}
	return b, nil
}

// findSum 在 sha256sum 格式的清单里找一个文件的校验和（小写十六进制）；没有时返回空串。
func findSum(sums []byte, file string) string {
	sc := bufio.NewScanner(bytes.NewReader(sums))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) == 2 && strings.TrimPrefix(f[1], "*") == file && len(f[0]) == sha256.Size*2 {
			return strings.ToLower(f[0])
		}
	}
	return ""
}

// extractBinary 从 tar.gz 里取出名为 entry 的那个普通文件写到 w。包里别的内容不落盘。
func extractBinary(archive []byte, entry string, w io.Writer) error {
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return fmt.Errorf("发布包解不开：%w", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return fmt.Errorf("发布包里没有找到 %s", entry)
		}
		if err != nil {
			return fmt.Errorf("发布包解不开：%w", err)
		}
		if h.Typeflag != tar.TypeReg || h.Name != entry {
			continue
		}
		n, err := io.Copy(w, io.LimitReader(tr, maxBinarySize+1))
		if err != nil {
			return fmt.Errorf("发布包解不开：%w", err)
		}
		if n > maxBinarySize {
			return errors.New("发布包里的 cc-cluster 大得不正常")
		}
		return nil
	}
}
