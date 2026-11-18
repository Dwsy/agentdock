package skill

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/uvwt/agentdock/internal/skillspec"

	"gopkg.in/yaml.v3"
)

type skillFrontmatter struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
}

func LoadSkillDocument(packageDir string) (SkillDocument, error) {
	data, err := os.ReadFile(filepath.Join(packageDir, "SKILL.md"))
	if err != nil {
		return SkillDocument{}, packageError(ErrDocumentInvalid, "document.read", err)
	}
	doc, err := ParseSkillDocument(data)
	if err != nil {
		return SkillDocument{}, packageError(ErrDocumentInvalid, "document.parse", err)
	}
	return doc, nil
}

func ParseSkillMetadata(data []byte) (SkillMetadata, error) {
	frontmatter, body, err := splitSkillFrontmatter(data)
	if err != nil {
		return SkillMetadata{}, err
	}
	name, description, err := decodeAndValidateSkillFrontmatter(frontmatter, len(bytes.TrimSpace(body)) > 0)
	if err != nil {
		return SkillMetadata{}, err
	}
	return SkillMetadata{Name: name, Description: description}, nil
}

func ParseSkillDocument(data []byte) (SkillDocument, error) {
	frontmatter, rawBody, err := splitSkillFrontmatter(data)
	if err != nil {
		return SkillDocument{}, err
	}
	if bytes.IndexByte(rawBody, '\r') >= 0 {
		rawBody = bytes.ReplaceAll(rawBody, []byte("\r\n"), []byte("\n"))
	}
	body := strings.TrimSpace(string(rawBody))
	name, description, err := decodeAndValidateSkillFrontmatter(frontmatter, body != "")
	if err != nil {
		return SkillDocument{}, err
	}
	return SkillDocument{
		Name:        name,
		Description: description,
		Body:        body,
	}, nil
}

func decodeAndValidateSkillFrontmatter(frontmatter []byte, hasBody bool) (string, string, error) {
	var fields skillFrontmatter
	decoder := yaml.NewDecoder(bytes.NewReader(frontmatter))
	if err := decoder.Decode(&fields); err != nil {
		return "", "", fmt.Errorf("decode SKILL.md frontmatter: %w", err)
	}

	name := strings.TrimSpace(fields.Name)
	description := strings.TrimSpace(fields.Description)
	var issues []string
	if err := skillspec.ValidateName(name); err != nil {
		issues = append(issues, err.Error())
	}
	if err := skillspec.ValidateDescription(description); err != nil {
		issues = append(issues, err.Error())
	}
	if !hasBody {
		issues = append(issues, "markdown body is required")
	}
	if len(issues) > 0 {
		return "", "", errors.New(strings.Join(issues, "; "))
	}
	return name, description, nil
}

// splitSkillFrontmatter slices YAML frontmatter and markdown body directly from data
// so metadata extraction skips allocating/normalizing multi-KB markdown bodies.
func splitSkillFrontmatter(data []byte) ([]byte, []byte, error) {
	nl0 := bytes.IndexByte(data, '\n')
	if nl0 < 0 || string(bytes.TrimSpace(data[:nl0])) != "---" {
		return nil, nil, errors.New("SKILL.md must start with YAML frontmatter")
	}
	// Require at least 3 lines (opening '---', content/empty line, closing line).
	if bytes.IndexByte(data[nl0+1:], '\n') < 0 {
		return nil, nil, errors.New("SKILL.md must start with YAML frontmatter")
	}

	start := nl0 + 1
	for pos := start; pos <= len(data); {
		end := len(data)
		hasNewline := false
		if rel := bytes.IndexByte(data[pos:], '\n'); rel >= 0 {
			end = pos + rel
			hasNewline = true
		}
		if string(bytes.TrimSpace(data[pos:end])) == "---" {
			var frontmatter []byte
			if pos > start {
				frontmatter = data[start : pos-1]
				if len(frontmatter) > 0 && frontmatter[len(frontmatter)-1] == '\r' {
					frontmatter = frontmatter[:len(frontmatter)-1]
				}
				if bytes.IndexByte(frontmatter, '\r') >= 0 {
					frontmatter = bytes.ReplaceAll(frontmatter, []byte("\r\n"), []byte("\n"))
				}
			}
			var body []byte
			if hasNewline {
				body = data[end+1:]
			}
			return frontmatter, body, nil
		}
		if !hasNewline {
			break
		}
		pos = end + 1
	}
	return nil, nil, errors.New("SKILL.md frontmatter must be closed by ---")
}

func ValidatePackage(packageDir string) error {
	if _, err := os.Stat(filepath.Join(packageDir, "agentdock.yaml")); err == nil {
		return packageError(ErrInvalidPackage, "package.legacy_manifest", errors.New("agentdock.yaml is not supported; Skill packages are document-only"))
	} else if !os.IsNotExist(err) {
		return packageError(ErrInvalidPackage, "package.legacy_manifest", err)
	}
	return filepath.WalkDir(packageDir, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return packageError(ErrInvalidPackage, "package.symlink", errors.New("symlinks are not allowed in Skill packages"))
		}
		if !entry.IsDir() {
			info, err := entry.Info()
			if err != nil {
				return packageError(ErrInvalidPackage, "package.file_type", err)
			}
			if !info.Mode().IsRegular() {
				return packageError(ErrInvalidPackage, "package.file_type", errors.New("special files are not allowed in Skill packages"))
			}
		}
		if !entry.IsDir() && entry.Name() == ".agentdock-install.json" {
			return packageError(ErrInvalidPackage, "package.reserved_file", errors.New(".agentdock-install.json is reserved legacy AgentDock metadata and is not valid Skill package content"))
		}
		return nil
	})
}
