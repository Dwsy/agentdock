package resourcelibrary

import (
	"archive/zip"
	"io"
	"os"
	"path"
	"strings"
)

const (
	KindSkill  = "skill"
	KindPlugin = "plugin"

	// 与现有安装器相同的上限。云端资源库不能另定一套更大的包。
	SkillArchiveLimit  int64 = 128 << 20
	PluginArchiveLimit int64 = 64 << 20
	SkillExtractLimit  int64 = 128 << 20
	PluginExtractLimit int64 = 256 << 20
	MaxPackageFiles          = 10000
	maxControlBody           = 64 * 1024
	maxSecretScanBytes int64 = 1 << 20
)

func archiveLimit(kind string) int64 {
	if kind == KindPlugin {
		return PluginArchiveLimit
	}
	return SkillArchiveLimit
}

func extractLimit(kind string) int64 {
	if kind == KindPlugin {
		return PluginExtractLimit
	}
	return SkillExtractLimit
}

// rejectedPrivatePath 拒绝私人运行时数据和密钥文件名。
// 这是路径规则，不是内容扫描；内容里的疑似凭证只产生警告。
func rejectedPrivatePath(relative string) string {
	relative = strings.ToLower(path.Clean(relative))
	if relative == "." {
		return ""
	}
	for _, segment := range strings.Split(relative, "/") {
		if segment == "" || segment == "." {
			continue
		}
		extension := path.Ext(segment)
		stem := strings.TrimSuffix(segment, extension)
		switch extension {
		case ".pem", ".key", ".p12", ".pfx", ".keystore", ".jks":
			return "key material"
		case ".sqlite", ".db", ".bak", ".session":
			return "private runtime data"
		}
		if segment == ".env" || (strings.HasPrefix(segment, ".env.") &&
			segment != ".env.example" && segment != ".env.sample" && segment != ".env.template") || extension == ".env" {
			return "environment file"
		}
		// 以普通源码命名的 session.ts、key.go 不属于凭证文件。
		// credentials/、secrets/ 可能包含正常资源文件，目录名不作为整目录拒绝的依据；
		// 其中明确的密钥、Token、凭证文件名仍按下方规则过滤。
		switch segment {
		case "cookie", "cookies", "session", "sessions", "secret",
			"credential", "id_rsa", "id_dsa", "id_ecdsa", "id_ed25519",
			"key", "privatekey", "private_key", "skill-data", ".skill-data",
			".agentdock", ".ssh", ".aws", ".kube":
			return "private runtime data"
		}
		switch extension {
		case ".json", ".yaml", ".yml", ".toml", ".ini", ".txt", ".csv", ".xml", ".conf", ".cfg", ".properties":
			switch stem {
			case "cookie", "cookies", "session", "sessions", "secret", "secrets",
				"credential", "credentials", "privatekey", "private_key", "key",
				"token", "tokens", "access_token", "api_key", "password", "passwords":
				return "private runtime data"
			}
			if strings.HasSuffix(stem, "-key") || strings.HasSuffix(stem, "_key") ||
				strings.HasSuffix(stem, "-secret") || strings.HasSuffix(stem, "_secret") ||
				strings.HasSuffix(stem, "-cookie") || strings.HasSuffix(stem, "_cookie") ||
				strings.HasSuffix(stem, "-session") || strings.HasSuffix(stem, "_session") {
				return "private runtime data"
			}
		}
	}
	return ""
}

// ignoredExportPath 是与源码分离的缓存、安装来源元数据和环境示例文件。
// 它们不属于可恢复的包内容，不应因本机产生的缓存而阻断整份备份。
// 不忽略用户脚本、任意目录或普通点文件；敏感数据由 rejectedPrivatePath 明确拒绝。
func ignoredExportPath(relative string) bool {
	for _, segment := range strings.Split(strings.ToLower(relative), "/") {
		switch segment {
		case ".git", ".hg", ".svn", ".clawhub", "__pycache__", "node_modules",
			".pytest_cache", ".mypy_cache", ".ruff_cache", ".tox", ".venv", "venv":
			return true
		}
	}
	return false
}

// InspectArchive 在把候选 ZIP 交给原生安装器之前拒绝明显危险的包。
// 不对入站包施加源码扩展名白名单；Skill/Plugin 能否安装仍由原生安装器决定，
// 这里负责路径穿越、符号链接、特殊文件、私钥文件名、文件数和解压大小。
func InspectArchive(archivePath, kind string) error {
	reader, err := zip.OpenReader(archivePath)
	if err != nil {
		return failed("PACKAGE_INVALID", "validation", "candidate archive is not a readable ZIP")
	}
	defer reader.Close()

	var files int
	var total int64
	limit := extractLimit(kind)
	for _, file := range reader.File {
		if file.Flags&0x1 != 0 {
			return failed("PACKAGE_INVALID", "validation", "encrypted ZIP entries are not allowed")
		}
		if file.Mode()&os.ModeSymlink != 0 {
			return failedf("PACKAGE_INVALID", "validation", "ZIP symlink is not allowed: %s", file.Name)
		}
		name := strings.TrimSuffix(file.Name, "/")
		if err := validateArchiveName(name); err != nil {
			return err
		}
		if file.FileInfo().IsDir() {
			if reason := rejectedPrivatePath(name); reason != "" {
				return failedf("PACKAGE_PRIVATE_DATA", "validation", "ZIP directory contains %s: %s", reason, name)
			}
			continue
		}
		mode := file.Mode()
		if !mode.IsRegular() {
			return failedf("PACKAGE_INVALID", "validation", "ZIP special file is not allowed: %s", file.Name)
		}
		if reason := rejectedPrivatePath(name); reason != "" {
			return failedf("PACKAGE_PRIVATE_DATA", "validation", "ZIP entry contains %s: %s", reason, name)
		}
		files++
		if files > MaxPackageFiles {
			return failedf("PACKAGE_INVALID", "validation", "package exceeds %d files", MaxPackageFiles)
		}
		body, err := file.Open()
		if err != nil {
			return failed("PACKAGE_INVALID", "validation", "ZIP entry cannot be read")
		}
		copied, copyErr := io.Copy(io.Discard, io.LimitReader(body, limit-total+1))
		closeErr := body.Close()
		if copyErr != nil {
			return failed("PACKAGE_INVALID", "validation", "ZIP entry cannot be read")
		}
		if closeErr != nil {
			return failed("PACKAGE_INVALID", "validation", "ZIP entry cannot be read")
		}
		total += copied
		if total > limit {
			return failedf("PACKAGE_INVALID", "validation", "uncompressed package exceeds %d bytes", limit)
		}
	}
	if files == 0 {
		return failed("PACKAGE_INVALID", "validation", "candidate archive has no files")
	}
	return nil
}

func validateArchiveName(name string) error {
	if name == "" {
		return failed("PACKAGE_INVALID", "validation", "ZIP entry path is empty")
	}
	if strings.HasPrefix(name, "/") || strings.Contains(name, `\`) || strings.Contains(name, ":") {
		return failedf("PACKAGE_INVALID", "validation", "ZIP path escapes package root: %s", name)
	}
	for _, segment := range strings.Split(name, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return failedf("PACKAGE_INVALID", "validation", "ZIP path escapes package root: %s", name)
		}
	}
	return nil
}
