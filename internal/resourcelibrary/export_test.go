package resourcelibrary

import (
	"archive/zip"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExportDirectory_自动排除隐私文件而不修改本地Skill(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "SKILL.md"), "---\nname: demo-skill\ndescription: Export demo.\n---\n\n# Demo\n\npassword: \"supersecretvalue\"\n")
	writeFile(t, filepath.Join(root, "references", "guide.md"), "Use a relative path.\n")
	writeFile(t, filepath.Join(root, ".env"), "TOKEN=real\n")
	writeFile(t, filepath.Join(root, "credentials", "token.json"), `{"token":"real"}`)

	archive := filepath.Join(t.TempDir(), "skill.zip")
	exported, err := ExportDirectory(root, KindSkill, archive)
	if err != nil {
		t.Fatal(err)
	}
	if exported.Size == 0 || exported.ArchiveDigest == "" || exported.ContentDigest == "" {
		t.Fatalf("export = %#v", exported)
	}
	if !hasFile(exported.Files, "SKILL.md") || !hasFile(exported.Files, "references/guide.md") {
		t.Fatalf("files = %#v", exported.Files)
	}
	if hasFile(exported.Files, ".env") || hasFile(exported.Files, "credentials/token.json") {
		t.Fatalf("private data appeared in export manifest: %#v", exported.Files)
	}
	if len(exported.Warnings) == 0 {
		t.Fatal("export must explain that private data was omitted")
	}
	warnings := strings.Join(exported.Warnings, "\n")
	if !strings.Contains(warnings, ".env") || !strings.Contains(warnings, "credentials") ||
		strings.Contains(warnings, "TOKEN=real") || strings.Contains(warnings, `"token":"real"`) {
		t.Fatalf("warnings should name excluded paths, never expose values: %s", warnings)
	}
	for _, name := range zipNames(t, archive) {
		if name == ".env" || strings.HasPrefix(name, "credentials/") {
			t.Fatalf("exported ZIP contains private data: %s", name)
		}
	}
	if actual, err := os.ReadFile(filepath.Join(root, ".env")); err != nil || string(actual) != "TOKEN=real\n" {
		t.Fatalf("local .env was modified: %q %v", actual, err)
	}
	if err := InspectArchive(archive, KindSkill); err != nil {
		t.Fatalf("exported ZIP must pass device safety inspection: %v", err)
	}
	preview, err := validateSkillArchive(t, archive, exported.ArchiveDigest, "demo-skill", exported.ContentDigest)
	if err != nil || !preview.Valid {
		t.Fatalf("exported ZIP cannot be restored: %#v %v", preview, err)
	}
}

func TestExportDirectory_拒绝符号链接但允许自定义目录(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "SKILL.md"), "---\nname: demo-skill\ndescription: Export demo.\n---\n\n# Demo\n")
	if err := os.Symlink(filepath.Join(root, "SKILL.md"), filepath.Join(root, "references")); err != nil {
		t.Fatal(err)
	}
	if _, err := ExportDirectory(root, KindSkill, filepath.Join(t.TempDir(), "link.zip")); err == nil {
		t.Fatal("export accepted a symlink")
	}
	os.Remove(filepath.Join(root, "references"))

	writeFile(t, filepath.Join(root, "bin", "tool"), "#!/bin/sh\n")
	writeFile(t, filepath.Join(root, "test_run.py"), "print('ok')\n")
	writeFile(t, filepath.Join(root, "package.json"), "{}\n")
	writeFile(t, filepath.Join(root, "docs", "使用说明.md"), "# 正常文档\n")
	if err := os.Chmod(filepath.Join(root, "bin", "tool"), 0o755); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(t.TempDir(), "skill.zip")
	exported, err := ExportDirectory(root, KindSkill, archive)
	if err != nil {
		t.Fatal(err)
	}
	if err := InspectArchive(archive, KindSkill); err != nil {
		t.Fatal(err)
	}
	if !hasFile(exported.Files, "bin/tool") || !hasFile(exported.Files, "test_run.py") ||
		!hasFile(exported.Files, "package.json") || !hasFile(exported.Files, "docs/使用说明.md") {
		t.Fatalf("expected installed package files in backup, got %#v", exported.Files)
	}
	zipReader, err := zip.OpenReader(archive)
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range zipReader.File {
		if file.Name == "bin/tool" && file.Mode().Perm()&0o111 == 0 {
			t.Errorf("export lost executable bit: %s %v", file.Name, file.Mode())
		}
	}
	if err := zipReader.Close(); err != nil {
		t.Fatal(err)
	}
	preview, err := validateSkillArchive(t, archive, exported.ArchiveDigest, "demo-skill", exported.ContentDigest)
	if err != nil || !preview.Valid {
		t.Fatalf("native validator rejected exported Skill: preview=%#v err=%v", preview, err)
	}
}

func TestExportDirectory_忽略本地缓存但保留普通元数据并保持摘要一致(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "SKILL.md"), "---\nname: demo-skill\ndescription: Backup.\n---\n\n# Demo\n\nBody.\n")
	writeFile(t, filepath.Join(root, "run.py"), "print('hello')\n")
	writeFile(t, filepath.Join(root, "_meta.json"), "{}\n")
	for _, relative := range []string{
		".clawhub/origin.json", ".git/config", "node_modules/lib/package.json",
		"__pycache__/run.pyc", "scripts/__pycache__/cache.pyc", ".env.example",
	} {
		writeFile(t, filepath.Join(root, filepath.FromSlash(relative)), "generated\n")
	}

	archive := filepath.Join(t.TempDir(), "skill.zip")
	exported, err := ExportDirectory(root, KindSkill, archive)
	if err != nil {
		t.Fatal(err)
	}
	if len(exported.Files) != 3 || !hasFile(exported.Files, "_meta.json") || len(exported.Warnings) == 0 {
		t.Fatalf("unexpected filtered files: %#v", exported.Files)
	}
	for _, name := range zipNames(t, archive) {
		if strings.Contains(name, "__pycache__") || strings.Contains(name, "node_modules") ||
			strings.Contains(name, ".clawhub") || strings.Contains(name, ".git/") || name == ".env.example" {
			t.Fatalf("backup included local-only data: %s", name)
		}
	}
	preview, err := validateSkillArchive(t, archive, exported.ArchiveDigest, "demo-skill", exported.ContentDigest)
	if err != nil || !preview.Valid {
		t.Fatalf("snapshot digest differs from native installer: preview=%#v err=%v", preview, err)
	}
}

func TestExportDirectory_敏感命名目录仍可保存正常资源文件(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "SKILL.md"), "---\nname: demo-skill\ndescription: demo\n---\n\n# Demo\n")
	writeFile(t, filepath.Join(root, "credentials", "README.md"), "Document non-secret configuration.\n")
	writeFile(t, filepath.Join(root, "secrets", "examples", "mock.json"), "{}\n")
	writeFile(t, filepath.Join(root, "credentials", "token.json"), "DO_NOT_BACK_UP")
	writeFile(t, filepath.Join(root, "secrets", "private.key"), "DO_NOT_BACK_UP")
	archive := filepath.Join(t.TempDir(), "backup.zip")
	exported, err := ExportDirectory(root, KindSkill, archive)
	if err != nil {
		t.Fatal(err)
	}
	if !hasFile(exported.Files, "credentials/README.md") || !hasFile(exported.Files, "secrets/examples/mock.json") {
		t.Fatalf("non-secret files were omitted: %#v", exported.Files)
	}
	if hasFile(exported.Files, "credentials/token.json") || hasFile(exported.Files, "secrets/private.key") {
		t.Fatalf("private files were included: %#v", exported.Files)
	}
	if err := InspectArchive(archive, KindSkill); err != nil {
		t.Fatal(err)
	}
	preview, err := validateSkillArchive(t, archive, exported.ArchiveDigest, "demo-skill", exported.ContentDigest)
	if err != nil || !preview.Valid {
		t.Fatalf("filtered Skill cannot be restored: %#v %v", preview, err)
	}
}

func TestExportDirectory_不同私有路径自动排除且不影响普通源码(t *testing.T) {
	for _, secret := range []string{
		".env", ".env.production", "credentials/token.json", "secret.json", "private_key.yaml",
		".ssh/config", "state.sqlite", "auth-session.json", "token.json", "credentials.csv",
		"secrets/key.pem", "data/cookies.json", "data/.env", "data/skill-data/state.json",
	} {
		t.Run(secret, func(t *testing.T) {
			root := t.TempDir()
			writeFile(t, filepath.Join(root, "SKILL.md"), "---\nname: demo-skill\ndescription: Export.\n---\n\n# Demo\n")
			writeFile(t, filepath.Join(root, "session.ts"), "export const start = true;\n")
			source := filepath.Join(root, filepath.FromSlash(secret))
			writeFile(t, source, "private content")
			archive := filepath.Join(t.TempDir(), "skill.zip")
			exported, err := ExportDirectory(root, KindSkill, archive)
			if err != nil {
				t.Fatalf("backup should skip private data: %v", err)
			}
			if hasFile(exported.Files, secret) || !hasFile(exported.Files, "session.ts") {
				t.Fatalf("wrong export manifest: %#v", exported.Files)
			}
			for _, name := range zipNames(t, archive) {
				if name == secret {
					t.Fatalf("private data was uploaded: %s", name)
				}
			}
			if len(exported.Warnings) == 0 {
				t.Fatal("omitted private files need a visible warning")
			}
			if actual, err := os.ReadFile(source); err != nil || string(actual) != "private content" {
				t.Fatalf("original file was modified: %q %v", actual, err)
			}
			if err := InspectArchive(archive, KindSkill); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestExportDirectory_Skill只允许根目录Manifest(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "SKILL.md"), "---\nname: demo-skill\ndescription: Export demo.\n---\n")
	writeFile(t, filepath.Join(root, "docs", "SKILL.md"), "---\nname: stale-copy\ndescription: Old manifest.\n---\n")

	_, err := ExportDirectory(root, KindSkill, filepath.Join(t.TempDir(), "skill.zip"))
	if err == nil || !errorCode(err, "PACKAGE_INVALID") {
		t.Fatalf("nested SKILL.md error = %v", err)
	}
}

func TestExportDirectory_Plugin也自动排除隐私文件(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "plugin.json"), `{"name":"demo-plugin","version":"1.0.0","description":"Demo"}`)
	writeFile(t, filepath.Join(root, "skills", "demo-skill", "SKILL.md"), "---\nname: demo-skill\ndescription: Plugin skill.\n---\n\n# Demo\n\nBody.\n")
	writeFile(t, filepath.Join(root, "notes.pem"), "-----BEGIN PRIVATE KEY-----\n")
	writeFile(t, filepath.Join(root, "skills", "demo-skill", ".env"), "API_KEY=secret\n")
	archive := filepath.Join(t.TempDir(), "plugin.zip")
	exported, err := ExportDirectory(root, KindPlugin, archive)
	if err != nil {
		t.Fatal(err)
	}
	if hasFile(exported.Files, "notes.pem") || hasFile(exported.Files, "skills/demo-skill/.env") {
		t.Fatalf("private Plugin files were included: %#v", exported.Files)
	}
	if len(exported.Warnings) < 2 {
		t.Fatalf("expected omitted-file warnings, got: %#v", exported.Warnings)
	}
	if err := InspectArchive(archive, KindPlugin); err != nil {
		t.Fatal(err)
	}
	manager := newPluginManager(t, filepath.Join(t.TempDir(), "agentdock-home"))
	review := manager.ValidateSource(context.Background(), archive)
	if !review.Valid || review.PackageDigest != exported.ContentDigest {
		t.Fatalf("filtered Plugin must still pass native validation: %#v, exported=%s", review, exported.ContentDigest)
	}
}

func TestExportDirectory_Plugin保留原生安装器支持的文件(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "plugin.json"), `{"name":"demo-plugin","version":"1.0.0","description":"Backup"}`)
	writeFile(t, filepath.Join(root, "package.json"), `{"name":"demo-plugin"}`)
	writeFile(t, filepath.Join(root, "skills", "demo-skill", "SKILL.md"), "---\nname: demo-skill\ndescription: Backup.\n---\n\n# Demo\n\nBody.\n")
	writeFile(t, filepath.Join(root, "skills", "demo-skill", "test_run.py"), "print('ok')\n")
	writeFile(t, filepath.Join(root, "assets", "说明.md"), "# 文档\n")
	archive := filepath.Join(t.TempDir(), "plugin.zip")
	exported, err := ExportDirectory(root, KindPlugin, archive)
	if err != nil {
		t.Fatal(err)
	}
	if err := InspectArchive(archive, KindPlugin); err != nil {
		t.Fatal(err)
	}
	if !hasFile(exported.Files, "package.json") || !hasFile(exported.Files, "skills/demo-skill/test_run.py") {
		t.Fatalf("plugin files lost: %#v", exported.Files)
	}
	manager := newPluginManager(t, filepath.Join(t.TempDir(), "agentdock-home"))
	review := manager.ValidateSource(context.Background(), archive)
	if !review.Valid || review.PackageDigest != exported.ContentDigest {
		t.Fatalf("native Plugin validator disagrees with backup digest: %#v, exported=%s", review, exported.ContentDigest)
	}
}

func TestInspectArchive_拒绝路径穿越符号链接和秘密文件(t *testing.T) {
	traversal := filepath.Join(t.TempDir(), "traversal.zip")
	writeRawZip(t, traversal, map[string]string{"../evil.txt": "owned"})
	if err := InspectArchive(traversal, KindSkill); err == nil {
		t.Fatal("inspect accepted a traversal entry")
	}

	secret := filepath.Join(t.TempDir(), "secret.zip")
	writeRawZip(t, secret, map[string]string{".env": "TOKEN=real", "SKILL.md": "---\nname: demo-skill\n"})
	if err := InspectArchive(secret, KindSkill); err == nil {
		t.Fatal("inspect accepted .env")
	}

	link := filepath.Join(t.TempDir(), "link.zip")
	writeSymlinkZip(t, link, "SKILL.md", "outside")
	if err := InspectArchive(link, KindSkill); err == nil {
		t.Fatal("inspect accepted a symlink")
	}

	valid := filepath.Join(t.TempDir(), "valid.zip")
	writeRawZip(t, valid, map[string]string{"SKILL.md": "---\nname: demo-skill\ndescription: Demo.\n---\n\n# Demo\n"})
	if err := InspectArchive(valid, KindSkill); err != nil {
		t.Fatal(err)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func writeRawZip(t *testing.T, path string, files map[string]string) {
	t.Helper()
	output, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	writer := zip.NewWriter(output)
	for name, content := range files {
		entry, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := output.Close(); err != nil {
		t.Fatal(err)
	}
}

func writeSymlinkZip(t *testing.T, path, name, target string) {
	t.Helper()
	output, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	writer := zip.NewWriter(output)
	header := &zip.FileHeader{Name: name}
	header.SetMode(os.ModeSymlink | 0o777)
	entry, err := writer.CreateHeader(header)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := entry.Write([]byte(target)); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := output.Close(); err != nil {
		t.Fatal(err)
	}
}

func zipNames(t *testing.T, path string) []string {
	t.Helper()
	reader, err := zip.OpenReader(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	names := make([]string, 0, len(reader.File))
	for _, file := range reader.File {
		names = append(names, file.Name)
	}
	return names
}
