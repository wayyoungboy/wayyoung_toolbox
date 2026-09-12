package integrity

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const (
	kindGeneric    = "generic"
	kindPDF        = "pdf"
	kindArchive    = "archive"
	kindMedia      = "media"
	kindImage      = "image"
	kindSQLite     = "sqlite"
	kindXML        = "xml"
	kindIncomplete = "incomplete"
	kindSystem     = "system"
)

var (
	mediaExtensions   = extensionSet(".mkv .mp4 .m4v .mov .avi .wmv .flv .webm .mpg .mpeg .m2ts .mts .vob .rm .rmvb .mp3 .flac .wav .aac .m4a .ogg .opus .ape .wma")
	imageExtensions   = extensionSet(".jpg .jpeg .png .gif .bmp .webp .tif .tiff .heic .heif .avif")
	archiveExtensions = extensionSet(".zip .rar .7z .tar .gz .tgz .bz2 .tbz2 .xz .txz .apk .jar .epub .docx .xlsx .pptx")
)

func extensionSet(value string) map[string]bool {
	result := make(map[string]bool)
	for _, extension := range strings.Fields(value) {
		result[extension] = true
	}
	return result
}

func detectKind(path string) string {
	name := strings.ToLower(filepath.Base(path))
	extension := strings.ToLower(filepath.Ext(name))
	if strings.HasPrefix(name, "._") || strings.HasPrefix(name, "~$") || name == "thumbs.db" || name == ".ds_store" {
		return kindSystem
	}
	for _, suffix := range []string{".part", ".downloading", ".crdownload", ".aria2", ".download"} {
		if strings.HasSuffix(name, suffix) {
			return kindIncomplete
		}
	}
	if extension == ".pdf" {
		return kindPDF
	}
	if extension == ".ts" {
		if isMPEGTransportStream(path) {
			return kindMedia
		}
		return kindGeneric
	}
	if mediaExtensions[extension] {
		return kindMedia
	}
	if imageExtensions[extension] {
		return kindImage
	}
	if archiveExtensions[extension] {
		return kindArchive
	}
	if extension == ".sqlite" || extension == ".sqlite3" || (extension == ".db" && hasPrefix(path, []byte("SQLite format 3\x00"))) {
		return kindSQLite
	}
	if extension == ".xml" || (extension == ".nfo" && firstNonSpaceIs(path, '<')) {
		return kindXML
	}
	return kindGeneric
}

func isMPEGTransportStream(path string) bool {
	file, err := os.Open(path)
	if err != nil {
		return false
	}
	defer file.Close()
	buffer := make([]byte, 188*4)
	count, err := file.Read(buffer)
	if err != nil && err != io.EOF {
		return false
	}
	buffer = buffer[:count]
	for offset := 0; offset < 188 && offset+376 < len(buffer); offset++ {
		if buffer[offset] == 0x47 && buffer[offset+188] == 0x47 && buffer[offset+376] == 0x47 {
			return true
		}
	}
	return false
}

func hasPrefix(path string, prefix []byte) bool {
	file, err := os.Open(path)
	if err != nil {
		return false
	}
	defer file.Close()
	buffer := make([]byte, len(prefix))
	_, err = io.ReadFull(file, buffer)
	return err == nil && bytes.Equal(buffer, prefix)
}

func firstNonSpaceIs(path string, expected byte) bool {
	file, err := os.Open(path)
	if err != nil {
		return false
	}
	defer file.Close()
	buffer := make([]byte, 4096)
	count, _ := file.Read(buffer)
	return len(bytes.TrimSpace(buffer[:count])) > 0 && bytes.TrimSpace(buffer[:count])[0] == expected
}

func checkFile(ctx context.Context, path, mode string) (result Result) {
	started := time.Now()
	result = Result{Path: path, Kind: detectKind(path), Status: statusHealthy}
	defer func() { result.DurationMS = time.Since(started).Milliseconds() }()
	info, err := os.Stat(path)
	if err != nil {
		result.Status, result.Message, result.Method = statusDamaged, "文件已消失或不可访问："+err.Error(), "文件状态"
		return result
	}
	result.Size = info.Size()
	if !info.Mode().IsRegular() {
		result.Status, result.Message, result.Method = statusSkipped, "不是普通文件", "文件类型检查"
		return result
	}
	if result.Kind == kindSystem {
		result.Status, result.Message, result.Method = statusSkipped, "系统元数据或临时文件，未按同扩展名格式检查", "文件名规则"
		return result
	}
	if info.Size() == 0 {
		result.Status, result.Message, result.Method = statusWarning, "空文件，无法确认内容完整性", "文件大小"
		return result
	}
	checksum, err := readFile(ctx, path, mode == "deep")
	if err != nil {
		if errors.Is(err, context.Canceled) {
			result.Status, result.Message = statusSkipped, "检查已取消"
		} else {
			result.Status, result.Message = statusDamaged, "读取失败："+err.Error()
		}
		result.Method = readMethod(mode)
		return result
	}
	result.Checksum = checksum
	if result.Kind == kindIncomplete {
		result.Status, result.Message, result.Method = statusWarning, "文件名表示下载尚未完成", readMethod(mode)+" + 文件名规则"
		return result
	}

	var status, message, method string
	switch result.Kind {
	case kindPDF:
		status, message = checkPDFMarkers(path)
		method = readMethod(mode) + " + PDF 头尾结构"
		if status == statusHealthy && mode == "deep" {
			status, message, method = checkPDFDeep(ctx, path, method)
		}
	case kindArchive:
		status, message, method = checkArchive(ctx, path, mode)
	case kindMedia:
		status, message, method = checkMedia(ctx, path, mode)
	case kindImage:
		status, message, method = checkImage(ctx, path, mode)
	case kindSQLite:
		status, message, method = checkSQLite(ctx, path)
	case kindXML:
		status, message, method = checkXML(path)
	default:
		status, message, method = statusHealthy, "文件可正常读取", readMethod(mode)
	}
	result.Status, result.Message, result.Method = status, message, method
	return result
}

func readMethod(mode string) string {
	if mode == "deep" {
		return "完整读取 + SHA-256"
	}
	return "头尾抽样读取"
}

func readFile(ctx context.Context, path string, deep bool) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	if deep {
		hash := sha256.New()
		buffer := make([]byte, 1<<20)
		for {
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			default:
			}
			count, readErr := file.Read(buffer)
			if count > 0 {
				_, _ = hash.Write(buffer[:count])
			}
			if readErr == io.EOF {
				break
			}
			if readErr != nil {
				return "", readErr
			}
		}
		return hex.EncodeToString(hash.Sum(nil)), nil
	}
	info, err := file.Stat()
	if err != nil {
		return "", err
	}
	buffer := make([]byte, min(int64(1<<20), info.Size()))
	if _, err := io.ReadFull(file, buffer); err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
		return "", err
	}
	if info.Size() > int64(len(buffer)) {
		if _, err := file.Seek(max(0, info.Size()-(1<<20)), io.SeekStart); err != nil {
			return "", err
		}
		if _, err := io.CopyN(io.Discard, file, min(int64(1<<20), info.Size())); err != nil && err != io.EOF {
			return "", err
		}
	}
	return "", nil
}

func checkPDFMarkers(path string) (string, string) {
	file, err := os.Open(path)
	if err != nil {
		return statusDamaged, "PDF 无法读取：" + err.Error()
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return statusDamaged, "PDF 状态无法读取：" + err.Error()
	}
	head := make([]byte, min(int64(1024), info.Size()))
	_, _ = io.ReadFull(file, head)
	if !bytes.HasPrefix(bytes.TrimPrefix(head, []byte{0xef, 0xbb, 0xbf}), []byte("%PDF-")) {
		return statusDamaged, "缺少 PDF 文件头"
	}
	start := max(int64(0), info.Size()-(1<<20))
	if _, err := file.Seek(start, io.SeekStart); err != nil {
		return statusDamaged, "PDF 尾部无法读取：" + err.Error()
	}
	tail, err := io.ReadAll(io.LimitReader(file, 1<<20))
	if err != nil {
		return statusDamaged, "PDF 尾部无法读取：" + err.Error()
	}
	if !bytes.Contains(tail, []byte("%%EOF")) {
		return statusDamaged, "缺少 PDF 结束标记，文件可能被截断"
	}
	return statusHealthy, "PDF 头尾结构正常"
}

func checkPDFDeep(ctx context.Context, path, fallbackMethod string) (string, string, string) {
	if tool, err := exec.LookPath("gs"); err == nil {
		return runValidator(ctx, 30*time.Minute, tool, []string{"-q", "-dNOPAUSE", "-dBATCH", "-sDEVICE=nullpage", path}, "Ghostscript 全页渲染", false)
	}
	if tool, err := exec.LookPath("qpdf"); err == nil {
		return runValidator(ctx, 2*time.Minute, tool, []string{"--check", path}, "qpdf 完整结构检查", false)
	}
	if tool, err := exec.LookPath("pdfinfo"); err == nil {
		return runValidator(ctx, 2*time.Minute, tool, []string{path}, "pdfinfo 结构检查", false)
	}
	return statusWarning, "文件已完整读取且头尾正常，但系统未安装 Ghostscript/qpdf/pdfinfo，内部对象需人工复核", fallbackMethod
}

func checkArchive(ctx context.Context, path, mode string) (string, string, string) {
	extension := strings.ToLower(filepath.Ext(path))
	if extension == ".gz" || extension == ".tgz" {
		file, err := os.Open(path)
		if err != nil {
			return statusDamaged, err.Error(), "gzip 检查"
		}
		defer file.Close()
		reader, err := gzip.NewReader(file)
		if err != nil {
			return statusDamaged, "gzip 头部损坏：" + err.Error(), "gzip 检查"
		}
		defer reader.Close()
		if mode == "deep" {
			if _, err := io.Copy(io.Discard, reader); err != nil {
				return statusDamaged, "gzip 数据或校验和损坏：" + err.Error(), "gzip CRC 检查"
			}
		}
		return statusHealthy, "gzip 结构正常", "gzip 检查"
	}
	if tool, err := exec.LookPath("7z"); err == nil {
		status, message, method := runValidator(ctx, timeoutFor(mode), tool, []string{"t", "-bd", "-y", path}, "7z CRC 检查", true)
		return normalizeArchiveResult(path, status, message, method)
	}
	if tool, err := exec.LookPath("7zz"); err == nil {
		status, message, method := runValidator(ctx, timeoutFor(mode), tool, []string{"t", "-bd", "-y", path}, "7-Zip CRC 检查", true)
		return normalizeArchiveResult(path, status, message, method)
	}
	if extension == ".zip" || extension == ".apk" || extension == ".jar" || extension == ".epub" || extension == ".docx" || extension == ".xlsx" || extension == ".pptx" {
		if tool, err := exec.LookPath("unzip"); err == nil {
			return runValidator(ctx, timeoutFor(mode), tool, []string{"-t", path}, "ZIP CRC 检查", true)
		}
	}
	return statusWarning, "文件可读取，但系统缺少对应压缩包校验器", readMethod(mode)
}

func normalizeArchiveResult(path, status, message, method string) (string, string, string) {
	if status != statusDamaged || strings.ToLower(filepath.Ext(path)) != ".apk" {
		return status, message, method
	}
	lower := strings.ToLower(message)
	compatibilityWarning := strings.Contains(lower, "warning") || strings.Contains(lower, "data after the end of archive")
	realDamage := strings.Contains(lower, "data error") || strings.Contains(lower, "crc failed") || strings.Contains(lower, "unexpected end") || strings.Contains(lower, "cannot open") || strings.Contains(lower, "headers error")
	if compatibilityWarning && !realDamage {
		return statusWarning, "APK 签名块可能不被当前 7-Zip 完全识别，属于兼容性提示：" + message, method
	}
	return status, message, method
}

func checkMedia(ctx context.Context, path, mode string) (string, string, string) {
	if mode == "deep" {
		if tool, err := exec.LookPath("ffmpeg"); err == nil {
			return runValidator(ctx, 30*time.Minute, tool, []string{"-nostdin", "-v", "error", "-xerror", "-i", path, "-map", "0:v?", "-map", "0:a?", "-f", "null", "-"}, "ffmpeg 音视频流完整解码", false)
		}
	}
	if tool, err := exec.LookPath("ffprobe"); err == nil {
		return runValidator(ctx, 2*time.Minute, tool, []string{"-v", "error", "-show_entries", "format=duration", "-of", "default=nw=1:nk=1", path}, "ffprobe 容器检查", false)
	}
	return statusWarning, "文件可读取，但系统缺少 ffprobe/ffmpeg，无法验证媒体流", readMethod(mode)
}

func checkImage(ctx context.Context, path, mode string) (string, string, string) {
	file, err := os.Open(path)
	if err != nil {
		return statusDamaged, err.Error(), "图片解码"
	}
	_, format, decodeErr := image.DecodeConfig(bufio.NewReader(file))
	_ = file.Close()
	if decodeErr == nil {
		if mode == "quick" {
			return statusHealthy, "图片头部与尺寸信息正常（" + format + "）", "图片头部解码"
		}
		info, _ := os.Stat(path)
		if info != nil && info.Size() <= 512<<20 {
			full, openErr := os.Open(path)
			if openErr == nil {
				_, _, decodeErr = image.Decode(full)
				_ = full.Close()
			}
			if decodeErr == nil {
				return statusHealthy, "图片可完整解码（" + format + "）", "图片完整解码"
			}
		}
	}
	if tool, err := exec.LookPath("ffmpeg"); err == nil {
		status, message, method := runValidator(ctx, 5*time.Minute, tool, []string{"-nostdin", "-v", "error", "-xerror", "-i", path, "-frames:v", "1", "-f", "null", "-"}, "ffmpeg 图片解码", false)
		if status == statusDamaged && imageMayContainAppendedPayload(path) {
			return statusWarning, "图片带有追加数据，可能是封面加资源包；解码结果需复核：" + message, method
		}
		return status, message, method
	}
	if decodeErr != nil {
		return statusWarning, "图片格式不受内置解码器支持，且系统缺少 ffmpeg：" + decodeErr.Error(), readMethod(mode)
	}
	return statusWarning, "大图片头部正常，已完整读取但未在内存中完全解码", readMethod(mode) + " + 图片头部解码"
}

func imageMayContainAppendedPayload(path string) bool {
	extension := strings.ToLower(filepath.Ext(path))
	if extension != ".jpg" && extension != ".jpeg" {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && info.Size() > 512<<20
}

func checkSQLite(ctx context.Context, path string) (string, string, string) {
	if tool, err := exec.LookPath("sqlite3"); err == nil {
		status, message, method := runValidator(ctx, 5*time.Minute, tool, []string{path, "PRAGMA quick_check;"}, "SQLite quick_check", false)
		if status == statusHealthy && !strings.Contains(strings.ToLower(message), "ok") {
			return statusDamaged, "SQLite quick_check 未返回 ok：" + message, method
		}
		return status, message, method
	}
	file, err := os.Open(path)
	if err != nil {
		return statusDamaged, err.Error(), "SQLite 文件头"
	}
	defer file.Close()
	header := make([]byte, 16)
	_, err = io.ReadFull(file, header)
	if err != nil || string(header) != "SQLite format 3\x00" {
		return statusDamaged, "SQLite 文件头无效", "SQLite 文件头"
	}
	return statusWarning, "SQLite 文件头正常，但系统缺少 sqlite3，页结构需复核", "SQLite 文件头"
}

func checkXML(path string) (string, string, string) {
	file, err := os.Open(path)
	if err != nil {
		return statusDamaged, err.Error(), "XML 解析"
	}
	defer file.Close()
	decoder := xml.NewDecoder(file)
	for {
		_, err := decoder.Token()
		if err == io.EOF {
			return statusHealthy, "XML 结构正常", "XML 流式解析"
		}
		if err != nil {
			return statusDamaged, "XML 结构错误：" + err.Error(), "XML 流式解析"
		}
	}
}

func timeoutFor(mode string) time.Duration {
	if mode == "deep" {
		return 30 * time.Minute
	}
	return 5 * time.Minute
}

func runValidator(parent context.Context, timeout time.Duration, command string, args []string, method string, protectedIsWarning bool) (string, string, string) {
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, command, args...)
	output, err := cmd.CombinedOutput()
	message := strings.TrimSpace(string(output))
	if len(message) > 2000 {
		message = message[len(message)-2000:]
	}
	if err == nil {
		if message == "" {
			message = "格式校验通过"
		}
		return statusHealthy, message, method
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return statusWarning, "校验超时，结果不确定", method
	}
	if errors.Is(ctx.Err(), context.Canceled) {
		return statusSkipped, "检查已取消", method
	}
	lower := strings.ToLower(message)
	if protectedIsWarning && (strings.Contains(lower, "password") || strings.Contains(lower, "encrypted") || strings.Contains(lower, "wrong password")) {
		return statusWarning, "文件受密码保护，无法验证内部数据", method
	}
	if message == "" {
		message = fmt.Sprintf("校验器退出：%v", err)
	}
	return statusDamaged, message, method
}
