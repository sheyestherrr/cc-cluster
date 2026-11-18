// Command fakenet 提供 e2e 测试用的假服务与客户端，全部只在本机 internal 网络内使用。
//
// 服务端：
//
//	fakenet socks5 <端口> [域名一律解析到的地址]   假中转 / 假落地：最小 SOCKS5，无认证，仅 CONNECT
//	fakenet target <应答地址>                     假目标：80/8080 回报看到的来源地址，8081 流式输出，53 是 TCP DNS
//
// 客户端（输出一行 "OK …" 或 "ERR …"，供测试断言）：
//
//	fakenet get <主机> <端口> [Host 头]
//	fakenet resolve <域名>
//	fakenet dnsq <服务器> <域名>
//	fakenet udp <主机> <端口>
//	fakenet stream <主机> <端口>
package main

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "用法见源码头部注释")
		os.Exit(2)
	}
	a := os.Args[2:]
	arg := func(i int) string {
		if i < len(a) {
			return a[i]
		}
		return ""
	}
	switch os.Args[1] {
	case "socks5":
		socks5(arg(0), arg(1))
	case "target":
		target(arg(0))
	case "get":
		host := arg(2)
		if host == "" {
			host = arg(0)
		}
		get(arg(0), arg(1), host)
	case "resolve":
		resolve(arg(0))
	case "dnsq":
		dnsq(arg(0), arg(1))
	case "udp":
		udp(arg(0), arg(1))
	case "stream":
		stream(arg(0), arg(1))
	default:
		fmt.Fprintln(os.Stderr, "未知子命令", os.Args[1])
		os.Exit(2)
	}
}

// errName 把网络错误压成一个稳定的短名字。
func errName(err error) string {
	var errno syscall.Errno
	if errors.As(err, &errno) {
		switch errno {
		case syscall.ENETUNREACH:
			return "ENETUNREACH"
		case syscall.EHOSTUNREACH:
			return "EHOSTUNREACH"
		case syscall.ECONNREFUSED:
			return "ECONNREFUSED"
		case syscall.ECONNRESET:
			return "ECONNRESET"
		case syscall.ETIMEDOUT:
			return "ETIMEDOUT"
		}
		return "ERRNO" + strconv.Itoa(int(errno))
	}
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return "TIMEOUT"
	}
	if errors.Is(err, io.EOF) {
		return "EOF"
	}
	var de *net.DNSError
	if errors.As(err, &de) {
		return "DNSFAIL"
	}
	return "OTHER " + err.Error()
}

func serve(port string, handler func(net.Conn)) {
	ln, err := net.Listen("tcp4", ":"+port)
	if err != nil {
		fmt.Fprintln(os.Stderr, "listen:", err)
		os.Exit(1)
	}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go handler(c)
		}
	}()
}

func peerIP(c net.Conn) string {
	host, _, _ := net.SplitHostPort(c.RemoteAddr().String())
	return host
}

// ---- 假中转 / 假落地 ----

func socks5(port, resolveAll string) {
	serve(port, func(c net.Conn) {
		defer c.Close()
		peer := peerIP(c)
		fail := func(err error) {
			fmt.Printf("PEER %s FAIL %s\n", peer, errName(err))
			_, _ = c.Write([]byte{5, 5, 0, 1, 0, 0, 0, 0, 0, 0})
		}
		hdr := make([]byte, 2)
		if _, err := io.ReadFull(c, hdr); err != nil {
			return
		}
		if _, err := io.ReadFull(c, make([]byte, hdr[1])); err != nil {
			return
		}
		_, _ = c.Write([]byte{5, 0})
		req := make([]byte, 4)
		if _, err := io.ReadFull(c, req); err != nil {
			return
		}
		var host, kind string
		switch req[3] {
		case 1:
			b := make([]byte, 4)
			if _, err := io.ReadFull(c, b); err != nil {
				return
			}
			host, kind = net.IP(b).String(), "ip"
		case 3:
			l := make([]byte, 1)
			if _, err := io.ReadFull(c, l); err != nil {
				return
			}
			b := make([]byte, l[0])
			if _, err := io.ReadFull(c, b); err != nil {
				return
			}
			host, kind = string(b), "domain"
		default:
			fail(errors.New("atyp"))
			return
		}
		pb := make([]byte, 2)
		if _, err := io.ReadFull(c, pb); err != nil {
			return
		}
		dport := strconv.Itoa(int(binary.BigEndian.Uint16(pb)))
		fmt.Printf("PEER %s REQ %s %s:%s\n", peer, kind, host, dport)
		dst := host
		if kind == "domain" && resolveAll != "" {
			dst = resolveAll
		}
		u, err := net.DialTimeout("tcp4", net.JoinHostPort(dst, dport), 5*time.Second)
		if err != nil {
			fail(err)
			return
		}
		defer u.Close()
		_, _ = c.Write([]byte{5, 0, 0, 1, 0, 0, 0, 0, 0, 0})
		done := make(chan struct{}, 2)
		go func() { _, _ = io.Copy(u, c); done <- struct{}{} }()
		go func() { _, _ = io.Copy(c, u); done <- struct{}{} }()
		<-done
	})
	select {}
}

// ---- 假目标 ----

func target(answerIP string) {
	seen := func(c net.Conn) {
		defer c.Close()
		_ = c.SetReadDeadline(time.Now().Add(1500 * time.Millisecond))
		_, _ = c.Read(make([]byte, 4096))
		fmt.Fprintf(c, "HTTP/1.0 200 OK\r\nContent-Type: text/plain\r\n\r\nSEEN %s\n", peerIP(c))
	}
	serve("80", seen)
	serve("8080", seen)
	serve("8081", func(c net.Conn) {
		defer c.Close()
		for i := 0; i < 12; i++ {
			if _, err := fmt.Fprintf(c, "tick %d\n", i); err != nil {
				return
			}
			time.Sleep(500 * time.Millisecond)
		}
	})
	serve("53", func(c net.Conn) {
		defer c.Close()
		lb := make([]byte, 2)
		if _, err := io.ReadFull(c, lb); err != nil {
			return
		}
		q := make([]byte, binary.BigEndian.Uint16(lb))
		if _, err := io.ReadFull(c, q); err != nil || len(q) < 17 {
			return
		}
		i := 12
		for i < len(q) && q[i] != 0 {
			i += int(q[i]) + 1
		}
		if i+5 > len(q) {
			return
		}
		qtype := binary.BigEndian.Uint16(q[i+1 : i+3])
		fmt.Printf("DNSQ from %s qtype %d\n", peerIP(c), qtype)
		var an []byte
		if qtype == 1 {
			an = append([]byte{0xc0, 0x0c, 0, 1, 0, 1, 0, 0, 0, 60, 0, 4}, net.ParseIP(answerIP).To4()...)
		}
		r := append([]byte{}, q[:2]...)
		r = append(r, 0x81, 0x80, 0, 1, 0, byte(len(an)/16), 0, 0, 0, 0)
		r = append(r, q[12:i+5]...)
		r = append(r, an...)
		out := make([]byte, 2, 2+len(r))
		binary.BigEndian.PutUint16(out, uint16(len(r)))
		_, _ = c.Write(append(out, r...))
	})
	select {}
}

// ---- 客户端 ----

func get(host, port, hostHeader string) {
	c, err := net.DialTimeout("tcp4", net.JoinHostPort(host, port), 6*time.Second)
	if err != nil {
		fmt.Println("ERR", errName(err))
		return
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(6 * time.Second))
	fmt.Fprintf(c, "GET / HTTP/1.0\r\nHost: %s\r\n\r\n", hostHeader)
	data, err := io.ReadAll(c)
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if last := lines[len(lines)-1]; strings.Contains(last, "SEEN") {
		fmt.Println("OK", strings.TrimSpace(last))
		return
	}
	if err != nil {
		fmt.Println("ERR", errName(err))
		return
	}
	fmt.Println("ERR nobody")
}

func resolve(name string) {
	addrs, err := net.LookupHost(name)
	if err != nil || len(addrs) == 0 {
		fmt.Println("ERR", errName(err))
		return
	}
	fmt.Println("OK", addrs[0])
}

// dnsq 直接向指定服务器发一个 UDP 的 A 记录查询，绕过系统解析器。
func dnsq(server, name string) {
	q := []byte{0x12, 0x34, 1, 0, 0, 1, 0, 0, 0, 0, 0, 0}
	for _, lab := range strings.Split(name, ".") {
		q = append(q, byte(len(lab)))
		q = append(q, lab...)
	}
	q = append(q, 0, 0, 1, 0, 1)
	c, err := net.DialTimeout("udp4", net.JoinHostPort(server, "53"), 4*time.Second)
	if err != nil {
		fmt.Println("ERR", errName(err))
		return
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(4 * time.Second))
	if _, err := c.Write(q); err != nil {
		fmt.Println("ERR", errName(err))
		return
	}
	r := make([]byte, 1500)
	n, err := c.Read(r)
	if err != nil {
		fmt.Println("ERR", errName(err))
		return
	}
	r = r[:n]
	i := len(q)
	for i+10 <= len(r) {
		if r[i]&0xc0 == 0xc0 {
			i += 2
		} else {
			for i < len(r) && r[i] != 0 {
				i += int(r[i]) + 1
			}
			i++
		}
		if i+10 > len(r) {
			break
		}
		typ := binary.BigEndian.Uint16(r[i : i+2])
		rdlen := int(binary.BigEndian.Uint16(r[i+8 : i+10]))
		i += 10
		if typ == 1 && i+4 <= len(r) {
			fmt.Println("OK", net.IP(r[i:i+4]).String())
			return
		}
		i += rdlen
	}
	fmt.Println("ERR noanswer")
}

func udp(host, port string) {
	c, err := net.DialTimeout("udp4", net.JoinHostPort(host, port), 3*time.Second)
	if err != nil {
		fmt.Println("ERR", errName(err))
		return
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := c.Write([]byte("x")); err != nil {
		fmt.Println("ERR", errName(err))
		return
	}
	b := make([]byte, 100)
	n, err := c.Read(b)
	if err != nil {
		fmt.Println("ERR", errName(err))
		return
	}
	fmt.Printf("OK %q\n", b[:n])
}

// stream 读流式端口直到对端关闭，报告收到了多少拍。
func stream(host, port string) {
	c, err := net.DialTimeout("tcp4", net.JoinHostPort(host, port), 5*time.Second)
	if err != nil {
		fmt.Println("TICKS 0", errName(err))
		return
	}
	defer c.Close()
	n := 0
	rd := bufio.NewReader(c)
	for {
		_ = c.SetReadDeadline(time.Now().Add(3 * time.Second))
		if _, err := rd.ReadString('\n'); err != nil {
			if errors.Is(err, io.EOF) {
				fmt.Printf("TICKS %d closed\n", n)
			} else {
				fmt.Printf("TICKS %d %s\n", n, errName(err))
			}
			return
		}
		n++
	}
}
