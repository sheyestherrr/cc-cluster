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
	if err := ValidVersion(v); err != nil {
		return nil, err
	}
	// 先确认能写，再去下载。
	dir := filepath.Dir(target)
	if err := checkWritable(dir); err != nil {
		return nil, err
	}
	name := AssetName(v, goos, goarch)
	base := c.Site + "/" + Repo + "/releases/download/" + v + "/"

	sums, err := c.download(ctx, base+"SHA256SUMS", maxSumsSize)
	if err != nil {
		if errors.Is(err, errNotFound) {
			return nil, fmt.Errorf("未找到版本 %s", v)
		}
		return nil, fmt.Errorf("下载校验和文件失败：%w", err)
	}
	want := findSum(sums, name+".tar.gz")
	if want == "" {
		return nil, fmt.Errorf("版本 %s 未提供 %s/%s 的安装包", v, goos, goarch)
	}
	archive, err := c.download(ctx, base+name+".tar.gz", maxArchiveSize)
	if err != nil {
		return nil, fmt.Errorf("下载 %s.tar.gz 失败：%w", name, err)
	}
	sum := sha256.Sum256(archive)
	if got := hex.EncodeToString(sum[:]); got != want {
		return nil, fmt.Errorf("%s.tar.gz 校验和不匹配（应为 %s，实际为 %s）", name, want, got)
	}

	f, err := os.CreateTemp(dir, ".cc-cluster-*.new")
	if err != nil {
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
	// 新的二进制无法运行（文件损坏、架构不符、目录禁止执行）就不替换。
	vctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	out, rerr := exec.CommandContext(vctx, tmp, "version").Output()
	if rerr != nil {
		err = fmt.Errorf("新版本无法运行：%w", rerr)
		return nil, err
	}
	if got := strings.TrimSpace(string(out)); got != v {
		err = fmt.Errorf("安装包内的程序版本为 %q，与 %s 不符", got, v)
		return nil, err
	}
	return &Staged{Version: v, Target: target, tmp: tmp}, nil
}

// checkWritable 确认能在 dir 里创建文件：建一个空文件再删掉。
func checkWritable(dir string) error {
	f, err := os.CreateTemp(dir, ".cc-cluster-*.new")
	if err != nil {
		if errors.Is(err, os.ErrPermission) {
			return fmt.Errorf("无权写入 %s，请以安装 cc-cluster 的用户执行", dir)
		}
		return err
	}
	f.Close()
	return os.Remove(f.Name())
}

// Commit 用新的二进制替换目标：同一个目录里的一次改名，要么是旧的要么是新的，不会有写了一半的文件。
// 正在运行的进程继续用它已经打开的旧文件，不受影响。
func (s *Staged) Commit() error {
	if s.tmp == "" {
		return errors.New("更新已完成或已取消")
	}
	// 沿用原来的权限位（至少可执行）。
	if fi, err := os.Stat(s.Target); err == nil {
		_ = os.Chmod(s.tmp, fi.Mode().Perm()|0o100)
	}
	if err := os.Rename(s.tmp, s.Target); err != nil {
		os.Remove(s.tmp)
		s.tmp = ""
		return fmt.Errorf("替换 %s 失败：%w", s.Target, err)
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
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, errors.New("下载超时")
		}
		return nil, fmt.Errorf("下载中断：%w", err)
	}
	if int64(len(b)) > limit {
		return nil, fmt.Errorf("文件大小超过上限（%d MB）", limit>>20)
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
		return fmt.Errorf("安装包解压失败：%w", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return fmt.Errorf("安装包中缺少 %s", entry)
		}
		if err != nil {
			return fmt.Errorf("安装包解压失败：%w", err)
		}
		if h.Typeflag != tar.TypeReg || h.Name != entry {
			continue
		}
		n, err := io.Copy(w, io.LimitReader(tr, maxBinarySize+1))
		if err != nil {
			return fmt.Errorf("安装包解压失败：%w", err)
		}
		if n > maxBinarySize {
			return fmt.Errorf("安装包中的 cc-cluster 大小超过上限（%d MB）", maxBinarySize>>20)
		}
		return nil
	}
}
