package plugininfra

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/yaoice/goclaude/pkg/domain/plugin"
)

// Installer 实现 plugin.Installer：支持 local / git / http 三种来源。
type Installer struct {
	// HTTPClient 可注入用于测试；nil 时使用带超时的默认客户端。
	HTTPClient *http.Client
}

// NewInstaller 创建安装器。
func NewInstaller() *Installer {
	return &Installer{
		HTTPClient: &http.Client{Timeout: 60 * time.Second},
	}
}

// Install 把 source 安装到 destDir，返回插件根目录绝对路径。
//
// destDir 应为该插件的目标根目录（调用方已确定，例如 ~/.goclaude/plugins/<name>）。
func (i *Installer) Install(ctx context.Context, source, destDir string) (string, error) {
	// 展开 "owner/repo" 的 GitHub 简写为完整 git URL（其余原样）。
	src := plugin.NormalizeSource(strings.TrimSpace(source))
	if src == "" {
		return "", fmt.Errorf("空的安装来源")
	}
	if err := os.MkdirAll(filepath.Dir(destDir), 0o755); err != nil {
		return "", fmt.Errorf("创建插件目录失败: %w", err)
	}
	// 目标已存在则先清理，保证幂等
	if err := os.RemoveAll(destDir); err != nil {
		return "", fmt.Errorf("清理旧插件目录失败: %w", err)
	}

	switch plugin.ClassifySource(src) {
	case plugin.SourceLocal:
		if err := installLocal(src, destDir); err != nil {
			return "", err
		}
	case plugin.SourceGit:
		if err := i.installGit(ctx, src, destDir); err != nil {
			_ = os.RemoveAll(destDir)
			return "", err
		}
	case plugin.SourceHTTP:
		if err := i.installHTTP(ctx, src, destDir); err != nil {
			_ = os.RemoveAll(destDir)
			return "", err
		}
	default:
		return "", fmt.Errorf("不支持的来源类型: %q", src)
	}
	return destDir, nil
}

// Uninstall 删除已安装的插件根目录。
func (i *Installer) Uninstall(_ context.Context, rootPath string) error {
	if rootPath == "" {
		return fmt.Errorf("空的插件路径")
	}
	if err := os.RemoveAll(rootPath); err != nil {
		return fmt.Errorf("删除插件目录失败: %w", err)
	}
	return nil
}

// ---- local ----

func installLocal(src, dest string) error {
	abs, err := filepath.Abs(src)
	if err != nil {
		return fmt.Errorf("解析本地来源路径失败: %w", err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		return fmt.Errorf("本地来源不存在: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("本地来源不是目录: %s", abs)
	}
	return copyTree(abs, dest)
}

// copyTree 递归复制目录树，跳过 .git 目录。
func copyTree(src, dest string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return os.MkdirAll(dest, 0o755)
		}
		// 跳过版本控制目录
		if info.IsDir() && info.Name() == ".git" {
			return filepath.SkipDir
		}
		target := filepath.Join(dest, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			// 不复制符号链接，避免越界引用
			return nil
		}
		return copyFile(path, target, info.Mode())
	})
}

func copyFile(src, dest string, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode|0o600)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}

// ---- git ----

func (i *Installer) installGit(ctx context.Context, src, dest string) error {
	// 解析可选的 "#ref"（分支/标签），其余部分为克隆 URL。
	url := strings.TrimPrefix(strings.TrimSpace(src), "git+")
	var ref string
	if idx := strings.LastIndex(url, "#"); idx >= 0 {
		ref = strings.TrimSpace(url[idx+1:])
		url = strings.TrimSpace(url[:idx])
	}
	if err := validateRemoteURL(url); err != nil {
		return fmt.Errorf("git 来源被拒绝: %w", err)
	}
	// 通过参数数组调用 git，禁止 shell 解释；--depth 1 浅克隆。
	args := []string{"clone", "--depth", "1"}
	if ref != "" {
		// --branch 接受分支或标签名；指定后仅克隆该 ref。
		args = append(args, "--branch", ref)
	}
	args = append(args, "--", url, dest)
	cmd := exec.CommandContext(ctx, "git", args...)
	// 禁止交互式凭据提示卡住进程
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_ASKPASS=true")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git clone 失败: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	// 移除 .git 目录，避免把远端历史带入本地插件目录
	_ = os.RemoveAll(filepath.Join(dest, ".git"))
	return nil
}

// ---- http archive ----

func (i *Installer) installHTTP(ctx context.Context, src, dest string) error {
	if err := validateRemoteURL(src); err != nil {
		return fmt.Errorf("http 来源被拒绝: %w", err)
	}
	tmp, err := os.CreateTemp("", "goclaude-plugin-*.archive")
	if err != nil {
		return fmt.Errorf("创建临时文件失败: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	if err := i.download(ctx, src, tmp); err != nil {
		tmp.Close()
		return err
	}
	tmp.Close()

	if err := os.MkdirAll(dest, 0o755); err != nil {
		return err
	}

	lower := strings.ToLower(src)
	switch {
	case strings.HasSuffix(lower, ".zip"):
		if err := extractZip(tmpPath, dest); err != nil {
			return err
		}
	case strings.HasSuffix(lower, ".tar.gz"), strings.HasSuffix(lower, ".tgz"):
		if err := extractTarGz(tmpPath, dest, true); err != nil {
			return err
		}
	case strings.HasSuffix(lower, ".tar"):
		if err := extractTarGz(tmpPath, dest, false); err != nil {
			return err
		}
	default:
		return fmt.Errorf("不支持的压缩包格式: %s", src)
	}
	// 若解压后只有单个顶层目录，则将其作为插件根（常见的 GitHub 压缩包形态）
	flattenSingleDir(dest)
	return nil
}

func (i *Installer) download(ctx context.Context, url string, dst io.Writer) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("构造下载请求失败: %w", err)
	}
	client := i.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 60 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("下载失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("下载返回状态 %d", resp.StatusCode)
	}
	limited := io.LimitReader(resp.Body, maxDownloadBytes+1)
	n, err := io.Copy(dst, limited)
	if err != nil {
		return fmt.Errorf("写入下载内容失败: %w", err)
	}
	if n > maxDownloadBytes {
		return fmt.Errorf("下载内容超过大小上限 %d 字节", maxDownloadBytes)
	}
	return nil
}

// safeJoin 防 zip-slip：确保解压目标在 dest 之内，含 ".." 穿越的条目直接拒绝。
func safeJoin(dest, name string) (string, error) {
	normalized := strings.ReplaceAll(name, "\\", "/")
	target := filepath.Join(dest, normalized)
	cleanDest := filepath.Clean(dest)
	rel, err := filepath.Rel(cleanDest, target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("非法的压缩包条目路径: %q", name)
	}
	return target, nil
}

func extractZip(archivePath, dest string) error {
	zr, err := zip.OpenReader(archivePath)
	if err != nil {
		return fmt.Errorf("打开 zip 失败: %w", err)
	}
	defer zr.Close()

	var total int64
	count := 0
	for _, f := range zr.File {
		count++
		if count > maxArchiveFiles {
			return fmt.Errorf("压缩包条目数超过上限 %d", maxArchiveFiles)
		}
		target, err := safeJoin(dest, f.Name)
		if err != nil {
			return err
		}
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
			continue
		}
		// 跳过符号链接等非常规文件
		if !f.Mode().IsRegular() {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		written, err := writeLimited(target, rc, f.Mode(), maxArchiveBytes-total)
		rc.Close()
		if err != nil {
			return err
		}
		total += written
		if total > maxArchiveBytes {
			return fmt.Errorf("解压总大小超过上限 %d 字节", maxArchiveBytes)
		}
	}
	return nil
}

func extractTarGz(archivePath, dest string, gzipped bool) error {
	f, err := os.Open(archivePath)
	if err != nil {
		return fmt.Errorf("打开压缩包失败: %w", err)
	}
	defer f.Close()

	var r io.Reader = f
	if gzipped {
		gz, err := gzip.NewReader(f)
		if err != nil {
			return fmt.Errorf("解压 gzip 失败: %w", err)
		}
		defer gz.Close()
		r = gz
	}

	tr := tar.NewReader(r)
	var total int64
	count := 0
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("读取 tar 失败: %w", err)
		}
		count++
		if count > maxArchiveFiles {
			return fmt.Errorf("压缩包条目数超过上限 %d", maxArchiveFiles)
		}
		target, err := safeJoin(dest, hdr.Name)
		if err != nil {
			return err
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			written, err := writeLimited(target, tr, os.FileMode(hdr.Mode), maxArchiveBytes-total)
			if err != nil {
				return err
			}
			total += written
			if total > maxArchiveBytes {
				return fmt.Errorf("解压总大小超过上限 %d 字节", maxArchiveBytes)
			}
		default:
			// 跳过符号链接、设备等非常规条目，防止越界
			continue
		}
	}
	return nil
}

func writeLimited(target string, r io.Reader, mode os.FileMode, remaining int64) (int64, error) {
	if remaining <= 0 {
		return 0, fmt.Errorf("解压总大小超过上限")
	}
	out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode.Perm()|0o600)
	if err != nil {
		return 0, err
	}
	defer out.Close()
	n, err := io.Copy(out, io.LimitReader(r, remaining))
	if err != nil {
		return n, err
	}
	return n, nil
}

// flattenSingleDir 若 dest 下仅有一个子目录（无其它文件），将其内容上提到 dest。
func flattenSingleDir(dest string) {
	entries, err := os.ReadDir(dest)
	if err != nil || len(entries) != 1 || !entries[0].IsDir() {
		return
	}
	inner := filepath.Join(dest, entries[0].Name())
	innerEntries, err := os.ReadDir(inner)
	if err != nil {
		return
	}
	for _, e := range innerEntries {
		_ = os.Rename(filepath.Join(inner, e.Name()), filepath.Join(dest, e.Name()))
	}
	_ = os.Remove(inner)
}
