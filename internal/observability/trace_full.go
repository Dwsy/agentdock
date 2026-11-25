package observability

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

const (
	// TraceFullBytes 是全文旁路的单侧上限。预览契约（8 KiB）不能动：
	// 内存环常驻 2000 条记录，正文必须留在旁路文件里按需读取。
	// 256 KiB 是“够排障”和“脱敏成本”的折中：脱敏正则约 65 ms/MiB，
	// 单侧截到 256 KiB 后 Record 路径的同步脱敏被钳在几十毫秒内。
	TraceFullBytes = 256 << 10
	// Flush the pending full records before their payloads exceed this budget.
	traceFullPendingBytes = 8 << 20
	// A JSON line can have seven capped text fields, with up to six bytes
	// of JSON escaping per input byte, plus the envelope and truncation markers.
	traceFullScanBytes = 7*6*(TraceFullBytes+len(traceTruncatedMarker)) + 1024
)

// TraceFullCommand 是命令类工具全文副本的三个自由文本字段。
// 退出码、字节数等元数据已在预览记录里，不重复保存。
type TraceFullCommand struct {
	Command string `json:"command,omitempty"`
	Stdout  string `json:"stdout,omitempty"`
	Stderr  string `json:"stderr,omitempty"`
}

// TraceFullRecord 是单条调用的全文副本（脱敏后，单侧上限 TraceFullBytes）。
// 只有预览会被截断的记录才写全文：小 payload 的预览本身就是全文，
// 重复落盘只浪费磁盘。与预览同为按天 JSONL，存于 <root>/full/。
type TraceFullRecord struct {
	ID                uint64            `json:"id"`
	Arguments         string            `json:"arguments,omitempty"`
	Result            string            `json:"result,omitempty"`
	Error             string            `json:"error,omitempty"`
	Command           *TraceFullCommand `json:"command,omitempty"`
	FullTruncated     bool              `json:"full_truncated,omitempty"`
	FileDiff          string            `json:"file_diff,omitempty"`
	FileDiffTruncated bool              `json:"file_diff_truncated,omitempty"`
}

// fullDir 返回全文旁路目录；与预览文件同级，清理逻辑各自按天删除。
func (s *TraceStore) fullDir() string { return filepath.Join(s.root, "full") }

// traceNeedsFullCopy 判断预览是否会被截断。只有命中时才值得付全文脱敏与磁盘成本。
func traceNeedsFullCopy(record TraceRecord) bool {
	if len(record.Arguments) > TracePreviewBytes || len(record.Result) > TracePreviewBytes {
		return true
	}
	if record.Command != nil {
		return len(record.Command.Command) > TraceCommandTextBytes ||
			len(record.Command.Stdout) > TraceCommandTextBytes ||
			len(record.Command.Stderr) > TraceCommandTextBytes
	}
	return false
}

// buildTraceFull 在预览截断之前捕获全文副本。脱敏与截断语义复用预览路径
// （tracePreviewWith），只是上限更大：全文同样不允许出现未脱敏字节落盘，
// 截断处同样补 “…(truncated)” 标记。
func buildTraceFull(record TraceRecord, fileDiff string) TraceFullRecord {
	full := TraceFullRecord{ID: record.ID}

	arguments, argsTruncated := tracePreviewWith(record.Arguments, TraceFullBytes, redactTracePayload)
	result, resultTruncated := tracePreviewWith(record.Result, TraceFullBytes, redactTracePayload)
	errorText, errorTruncated := tracePreviewWith(record.Error, TraceFullBytes, redactCommandPayload)
	full.Arguments, full.Result, full.Error = arguments, result, errorText
	truncated := argsTruncated || resultTruncated || errorTruncated

	if record.Command != nil {
		command := TraceFullCommand{}
		var commandTruncated, stdoutTruncated, stderrTruncated bool
		command.Command, commandTruncated = traceCommandPreview(record.Command.Command, TraceFullBytes)
		command.Stdout, stdoutTruncated = traceCommandPreview(record.Command.Stdout, TraceFullBytes)
		command.Stderr, stderrTruncated = traceCommandPreview(record.Command.Stderr, TraceFullBytes)
		full.Command = &command
		truncated = truncated || commandTruncated || stdoutTruncated || stderrTruncated
	}
	full.FileDiff, full.FileDiffTruncated = FileDiffPreview(fileDiff, TraceFullBytes)
	full.FullTruncated = truncated || full.FileDiffTruncated
	return full
}

// FileDiffPreview applies the existing trace redaction to a bounded diff. The
// caller stores truncation separately so the diff retains its unified format.
func FileDiffPreview(diff string, limit int) (string, bool) {
	text, truncated := tracePreviewWith(diff, limit, redactCommandPayload)
	if truncated {
		text = strings.TrimSuffix(text, traceTruncatedMarker)
	}
	return strings.Clone(text), truncated
}

func (full TraceFullRecord) payloadBytes() int {
	size := len(full.Arguments) + len(full.Result) + len(full.Error) + len(full.FileDiff)
	if full.Command != nil {
		size += len(full.Command.Command) + len(full.Command.Stdout) + len(full.Command.Stderr)
	}
	return size
}

// FullRecord serves detail from the pending disk-write buffer or the sidecar
// files. It checks pending under the same lock that flushes those files.
func (s *TraceStore) FullRecord(id uint64) (TraceFullRecord, bool) {
	if s == nil || s.mode != TraceModeDisk || s.root == "" || id == 0 {
		return TraceFullRecord{}, false
	}
	s.mu.RLock()
	for i := len(s.pendingFull) - 1; i >= 0; i-- {
		record := s.pendingFull[i]
		if record.ID == id {
			s.mu.RUnlock()
			if record.Command != nil {
				command := *record.Command
				record.Command = &command
			}
			return record, true
		}
	}
	s.mu.RUnlock()

	entries, err := os.ReadDir(s.fullDir())
	if err != nil {
		return TraceFullRecord{}, false
	}
	days := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".jsonl") {
			continue
		}
		days = append(days, entry.Name())
	}
	// 目录按天命名，字典序倒序即新到旧。
	sort.Sort(sort.Reverse(sort.StringSlice(days)))
	for _, name := range days {
		record, ok := scanFullFile(filepath.Join(s.fullDir(), name), id)
		if ok {
			return record, true
		}
	}
	return TraceFullRecord{}, false
}

// scanFullFile 逐行扫描单个全文文件。先用 id 子串做廉价过滤，
// 避免对每行都跑 JSON 解码：一天的全文文件可能有几百 MB。
func scanFullFile(path string, id uint64) (TraceFullRecord, bool) {
	file, err := os.Open(path)
	if err != nil {
		return TraceFullRecord{}, false
	}
	defer file.Close()
	needle := []byte(`"id":` + strconv.FormatUint(id, 10))
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64<<10), traceFullScanBytes)
	for scanner.Scan() {
		line := scanner.Bytes()
		if !bytes.Contains(line, needle) {
			continue
		}
		var record TraceFullRecord
		if err := json.Unmarshal(line, &record); err != nil {
			continue
		}
		if record.ID == id {
			return record, true
		}
	}
	return TraceFullRecord{}, false
}
