package dockerapi

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"time"
)

// demux 拆开 Docker 的多路复用流（非 TTY 的 attach / logs / exec 输出）。
// 每一帧是 8 字节头（第 0 字节为流编号，后 4 字节为大端长度）加载荷。
func demux(r io.Reader, stdout, stderr io.Writer) error {
	var hdr [8]byte
	for {
		if _, err := io.ReadFull(r, hdr[:]); err != nil {
			if err == io.EOF {
				return nil
			}
			return fmt.Errorf("读取输出流帧头: %w", err)
		}
		w := stdout
		if hdr[0] == 2 {
			w = stderr
		}
		if _, err := io.CopyN(w, r, int64(binary.BigEndian.Uint32(hdr[4:]))); err != nil {
			return fmt.Errorf("读取输出流载荷: %w", err)
		}
	}
}

// ExecOptions 是 exec 的可选项。
type ExecOptions struct {
	User string
	Env  []string
}

// Exec 在运行中的容器里执行命令并等它结束。命令不接标准输入。
func (c *Client) Exec(ctx context.Context, container string, cmd []string, opt ExecOptions) (RunResult, error) {
	var created struct {
		ID string `json:"Id"`
	}
	body := map[string]any{"AttachStdout": true, "AttachStderr": true, "Cmd": cmd}
	if opt.User != "" {
		body["User"] = opt.User
	}
	if len(opt.Env) > 0 {
		body["Env"] = opt.Env
	}
	if err := c.do(ctx, http.MethodPost, "/containers/"+container+"/exec", nil, body, &created); err != nil {
		return RunResult{}, err
	}

	// 不请求连接升级：守护进程直接在应答体里写多路复用流，命令结束后关闭连接。
	resp, err := c.request(ctx, http.MethodPost, "/exec/"+created.ID+"/start", nil, "application/json",
		bytes.NewReader([]byte(`{"Detach":false,"Tty":false}`)))
	if err != nil {
		return RunResult{}, err
	}
	var o, e bytes.Buffer
	err = demux(resp.Body, &o, &e)
	resp.Body.Close()
	if err != nil {
		return RunResult{}, err
	}

	var st struct {
		ExitCode int  `json:"ExitCode"`
		Running  bool `json:"Running"`
	}
	if err := c.do(ctx, http.MethodGet, "/exec/"+created.ID+"/json", nil, nil, &st); err != nil {
		return RunResult{}, err
	}
	if st.Running {
		return RunResult{}, fmt.Errorf("docker: exec 的输出流已结束但进程仍在运行")
	}
	return RunResult{ExitCode: st.ExitCode, Stdout: o.String(), Stderr: e.String()}, nil
}

// File 是要放进容器的一个文件。
type File struct {
	Data []byte
	Mode int64
}

// CopyTo 把一组文件写进容器的 dir 目录，属主为容器内的 root。
// 文件内容经请求体传输，不出现在命令行和容器元数据里，适合带凭据的配置。
func (c *Client) CopyTo(ctx context.Context, container, dir string, files map[string]File) error {
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)

	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	now := time.Now()
	for _, n := range names {
		f := files[n]
		if err := tw.WriteHeader(&tar.Header{Name: n, Mode: f.Mode, Size: int64(len(f.Data)), ModTime: now, Typeflag: tar.TypeReg}); err != nil {
			return err
		}
		if _, err := tw.Write(f.Data); err != nil {
			return err
		}
	}
	if err := tw.Close(); err != nil {
		return err
	}
	resp, err := c.request(ctx, http.MethodPut, "/containers/"+container+"/archive", url.Values{"path": {dir}}, "application/x-tar", &buf)
	if err != nil {
		return err
	}
	return resp.Body.Close()
}
