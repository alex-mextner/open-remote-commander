package executor

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"unicode/utf8"

	"github.com/alex-mextner/open-remote-commander/internal/osproc"
	"github.com/alex-mextner/open-remote-commander/internal/pathpolicy"
	"github.com/alex-mextner/open-remote-commander/internal/processmgr"
	"github.com/alex-mextner/open-remote-commander/internal/searchmgr"
)

const (
	maxReadBytes  = 4 << 20
	maxWriteBytes = 4 << 20
	maxListItems  = 2000
)

type Executor struct {
	paths            *pathpolicy.Policy
	procs            *processmgr.Manager
	search           *searchmgr.Manager
	allowKillProcess bool
}

type Options struct{ AllowKillProcess bool }

func New(paths *pathpolicy.Policy, procs *processmgr.Manager) *Executor {
	return NewWithOptions(paths, procs, Options{})
}
func NewWithOptions(paths *pathpolicy.Policy, procs *processmgr.Manager, options Options) *Executor {
	return &Executor{paths: paths, procs: procs, search: searchmgr.New(8), allowKillProcess: options.AllowKillProcess}
}

func (e *Executor) Execute(ctx context.Context, tool string, raw json.RawMessage) (any, error) {
	switch tool {
	case "ping":
		return map[string]any{"ok": true}, nil
	case "read_file":
		return e.readFile(raw)
	case "write_file":
		return e.writeFile(raw)
	case "read_multiple_files":
		return e.readMultipleFiles(raw)
	case "edit_block":
		return e.editBlock(raw)
	case "list_directory":
		return e.listDirectory(raw)
	case "create_directory":
		return e.createDirectory(raw)
	case "move_file":
		return e.moveFile(raw)
	case "get_file_info":
		return e.getFileInfo(raw)
	case "start_process":
		return e.startProcess(raw)
	case "read_process_output":
		return e.readProcess(raw)
	case "interact_with_process":
		return e.writeProcess(raw)
	case "force_terminate":
		return e.terminateProcess(raw)
	case "list_sessions":
		return map[string]any{"sessions": e.procs.List()}, nil
	case "start_search":
		return e.startSearch(raw)
	case "get_more_search_results":
		return e.getMoreSearch(raw)
	case "stop_search":
		return e.stopSearch(raw)
	case "list_searches":
		return map[string]any{"searches": e.search.List()}, nil
	case "list_processes":
		return e.listProcesses(ctx, raw)
	case "kill_process":
		return e.killProcess(raw)
	case "get_config":
		return e.getConfig(), nil
	default:
		return nil, fmt.Errorf("unsupported tool %q", tool)
	}
}

func decodeArgs(raw json.RawMessage, dst any) error {
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return fmt.Errorf("invalid arguments: %w", err)
	}
	return nil
}

func (e *Executor) readFile(raw json.RawMessage) (any, error) {
	var a struct {
		Path     string `json:"path"`
		Offset   int64  `json:"offset,omitempty"`
		MaxBytes int    `json:"max_bytes,omitempty"`
	}
	if err := decodeArgs(raw, &a); err != nil {
		return nil, err
	}
	path, err := e.paths.ResolveExisting(a.Path)
	if err != nil {
		return nil, err
	}
	st, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if st.IsDir() {
		return nil, errors.New("path is a directory")
	}
	max := a.MaxBytes
	if max <= 0 || max > maxReadBytes {
		max = maxReadBytes
	}
	if a.Offset < 0 {
		return nil, errors.New("offset cannot be negative")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if _, err := f.Seek(a.Offset, io.SeekStart); err != nil {
		return nil, err
	}
	b, err := io.ReadAll(io.LimitReader(f, int64(max)+1))
	if err != nil {
		return nil, err
	}
	truncated := len(b) > max
	if truncated {
		b = b[:max]
	}
	if utf8.Valid(b) {
		return map[string]any{"path": path, "encoding": "utf-8", "content": string(b), "offset": a.Offset, "next_offset": a.Offset + int64(len(b)), "truncated": truncated}, nil
	}
	return map[string]any{"path": path, "encoding": "base64", "content": base64.StdEncoding.EncodeToString(b), "offset": a.Offset, "next_offset": a.Offset + int64(len(b)), "truncated": truncated}, nil
}

func (e *Executor) writeFile(raw json.RawMessage) (any, error) {
	var a struct {
		Path, Content, Encoding string
		Mode                    uint32 `json:"mode,omitempty"`
	}
	if err := decodeArgs(raw, &a); err != nil {
		return nil, err
	}
	path, err := e.paths.ResolveForCreate(a.Path)
	if err != nil {
		return nil, err
	}
	var data []byte
	switch a.Encoding {
	case "", "utf-8":
		data = []byte(a.Content)
	case "base64":
		data, err = base64.StdEncoding.DecodeString(a.Content)
		if err != nil {
			return nil, errors.New("invalid base64 content")
		}
	default:
		return nil, errors.New("encoding must be utf-8 or base64")
	}
	if len(data) > maxWriteBytes {
		return nil, errors.New("write exceeds 4MiB")
	}
	parent := filepath.Dir(path)
	if _, err := e.paths.ResolveExisting(parent); err != nil {
		return nil, err
	}
	if st, err := os.Lstat(path); err == nil && st.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("refusing to overwrite a symlink")
	} else if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	mode := os.FileMode(0600)
	if a.Mode != 0 {
		mode = os.FileMode(a.Mode & 0777)
	}
	tmp, err := os.CreateTemp(parent, ".orc-write-*")
	if err != nil {
		return nil, err
	}
	tmpName := tmp.Name()
	ok := false
	defer func() {
		if !ok {
			_ = os.Remove(tmpName)
		}
	}()
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return nil, err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return nil, err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return nil, err
	}
	if err := tmp.Close(); err != nil {
		return nil, err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return nil, err
	}
	ok = true
	return map[string]any{"path": path, "bytes_written": len(data)}, nil
}

func (e *Executor) listDirectory(raw json.RawMessage) (any, error) {
	var a struct {
		Path string `json:"path"`
	}
	if err := decodeArgs(raw, &a); err != nil {
		return nil, err
	}
	path, err := e.paths.ResolveExisting(a.Path)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, err
	}
	truncated := len(entries) > maxListItems
	if truncated {
		entries = entries[:maxListItems]
	}
	out := make([]map[string]any, 0, len(entries))
	for _, de := range entries {
		info, err := de.Info()
		if err != nil {
			continue
		}
		out = append(out, map[string]any{"name": de.Name(), "type": entryType(de), "size": info.Size(), "modified_at": info.ModTime().UTC()})
	}
	return map[string]any{"path": path, "entries": out, "truncated": truncated}, nil
}

func (e *Executor) createDirectory(raw json.RawMessage) (any, error) {
	var a struct {
		Path string `json:"path"`
		Mode uint32 `json:"mode,omitempty"`
	}
	if err := decodeArgs(raw, &a); err != nil {
		return nil, err
	}
	path, err := e.paths.ResolveForCreate(a.Path)
	if err != nil {
		return nil, err
	}
	mode := os.FileMode(0750)
	if a.Mode != 0 {
		mode = os.FileMode(a.Mode & 0777)
	}
	if err := os.MkdirAll(path, mode); err != nil {
		return nil, err
	}
	return map[string]any{"path": path}, nil
}
func (e *Executor) moveFile(raw json.RawMessage) (any, error) {
	var a struct {
		Source      string `json:"source"`
		Destination string `json:"destination"`
		Overwrite   bool   `json:"overwrite,omitempty"`
	}
	if err := decodeArgs(raw, &a); err != nil {
		return nil, err
	}
	src, err := e.paths.ResolveExisting(a.Source)
	if err != nil {
		return nil, err
	}
	dst, err := e.paths.ResolveForCreate(a.Destination)
	if err != nil {
		return nil, err
	}
	if st, err := os.Lstat(dst); err == nil {
		if st.Mode()&os.ModeSymlink != 0 {
			return nil, errors.New("refusing to replace symlink")
		}
		if !a.Overwrite {
			return nil, errors.New("destination exists; set overwrite=true to replace it")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if err := os.Rename(src, dst); err != nil {
		return nil, err
	}
	return map[string]any{"source": src, "destination": dst}, nil
}
func (e *Executor) getFileInfo(raw json.RawMessage) (any, error) {
	var a struct {
		Path string `json:"path"`
	}
	if err := decodeArgs(raw, &a); err != nil {
		return nil, err
	}
	path, err := e.paths.ResolveExisting(a.Path)
	if err != nil {
		return nil, err
	}
	st, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	return map[string]any{"path": path, "name": st.Name(), "size": st.Size(), "mode": st.Mode().String(), "modified_at": st.ModTime().UTC(), "is_dir": st.IsDir()}, nil
}

func (e *Executor) startProcess(raw json.RawMessage) (any, error) {
	var a processmgr.StartRequest
	if err := decodeArgs(raw, &a); err != nil {
		return nil, err
	}
	if a.Cwd != "" {
		cwd, err := e.paths.ResolveExisting(a.Cwd)
		if err != nil {
			return nil, err
		}
		a.Cwd = cwd
	}
	return e.procs.Start(a)
}
func (e *Executor) readProcess(raw json.RawMessage) (any, error) {
	var a struct {
		ProcessID string `json:"process_id"`
		Cursor    int64  `json:"cursor,omitempty"`
		MaxBytes  int    `json:"max_bytes,omitempty"`
	}
	if err := decodeArgs(raw, &a); err != nil {
		return nil, err
	}
	return e.procs.Read(a.ProcessID, a.Cursor, a.MaxBytes)
}
func (e *Executor) writeProcess(raw json.RawMessage) (any, error) {
	var a struct {
		ProcessID string `json:"process_id"`
		Data      string `json:"data"`
		Encoding  string `json:"encoding,omitempty"`
	}
	if err := decodeArgs(raw, &a); err != nil {
		return nil, err
	}
	data := []byte(a.Data)
	if a.Encoding == "base64" {
		var err error
		data, err = base64.StdEncoding.DecodeString(a.Data)
		if err != nil {
			return nil, errors.New("invalid base64 data")
		}
	} else if a.Encoding != "" && a.Encoding != "utf-8" {
		return nil, errors.New("encoding must be utf-8 or base64")
	}
	if err := e.procs.Write(a.ProcessID, data); err != nil {
		return nil, err
	}
	return map[string]any{"written": len(data)}, nil
}
func (e *Executor) terminateProcess(raw json.RawMessage) (any, error) {
	var a struct {
		ProcessID string `json:"process_id"`
	}
	if err := decodeArgs(raw, &a); err != nil {
		return nil, err
	}
	if err := e.procs.Terminate(a.ProcessID); err != nil {
		return nil, err
	}
	return map[string]any{"terminated": true}, nil
}

func entryType(d os.DirEntry) string {
	if d.IsDir() {
		return "directory"
	}
	if d.Type()&os.ModeSymlink != 0 {
		return "symlink"
	}
	if d.Type().IsRegular() {
		return "file"
	}
	return "other"
}

func (e *Executor) readMultipleFiles(raw json.RawMessage) (any, error) {
	var a struct {
		Paths           []string `json:"paths"`
		MaxBytesPerFile int      `json:"max_bytes_per_file,omitempty"`
	}
	if err := decodeArgs(raw, &a); err != nil {
		return nil, err
	}
	if len(a.Paths) == 0 || len(a.Paths) > 32 {
		return nil, errors.New("paths must contain 1 to 32 files")
	}
	max := a.MaxBytesPerFile
	if max <= 0 || max > maxReadBytes {
		max = maxReadBytes
	}
	files := make([]map[string]any, 0, len(a.Paths))
	total := 0
	for _, requested := range a.Paths {
		entry := map[string]any{"path": requested}
		path, err := e.paths.ResolveExisting(requested)
		if err != nil {
			entry["error"] = err.Error()
			files = append(files, entry)
			continue
		}
		st, err := os.Stat(path)
		if err != nil || st.IsDir() {
			if err == nil {
				err = errors.New("path is a directory")
			}
			entry["error"] = err.Error()
			files = append(files, entry)
			continue
		}
		f, err := os.Open(path)
		if err != nil {
			entry["error"] = err.Error()
			files = append(files, entry)
			continue
		}
		b, readErr := io.ReadAll(io.LimitReader(f, int64(max)+1))
		_ = f.Close()
		if readErr != nil {
			entry["error"] = readErr.Error()
			files = append(files, entry)
			continue
		}
		truncated := len(b) > max
		if truncated {
			b = b[:max]
		}
		total += len(b)
		if total > 16<<20 {
			return nil, errors.New("combined read exceeds 16MiB")
		}
		entry["path"] = path
		entry["truncated"] = truncated
		if utf8.Valid(b) {
			entry["encoding"] = "utf-8"
			entry["content"] = string(b)
		} else {
			entry["encoding"] = "base64"
			entry["content"] = base64.StdEncoding.EncodeToString(b)
		}
		files = append(files, entry)
	}
	return map[string]any{"files": files}, nil
}

func (e *Executor) editBlock(raw json.RawMessage) (any, error) {
	var a struct {
		FilePath             string `json:"file_path"`
		OldString            string `json:"old_string"`
		NewString            string `json:"new_string"`
		ExpectedReplacements int    `json:"expected_replacements,omitempty"`
	}
	if err := decodeArgs(raw, &a); err != nil {
		return nil, err
	}
	if a.OldString == "" {
		return nil, errors.New("old_string cannot be empty")
	}
	if a.ExpectedReplacements == 0 {
		a.ExpectedReplacements = 1
	}
	if a.ExpectedReplacements < 1 || a.ExpectedReplacements > 1000 {
		return nil, errors.New("expected_replacements must be between 1 and 1000")
	}
	path, err := e.paths.ResolveExisting(a.FilePath)
	if err != nil {
		return nil, err
	}
	st, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if st.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("refusing to edit a symlink")
	}
	if st.IsDir() {
		return nil, errors.New("path is a directory")
	}
	if st.Size() > maxWriteBytes {
		return nil, errors.New("file exceeds 4MiB edit limit")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if !utf8.Valid(data) {
		return nil, errors.New("edit_block supports UTF-8 text files only")
	}
	count := bytes.Count(data, []byte(a.OldString))
	if count != a.ExpectedReplacements {
		return nil, fmt.Errorf("replacement count mismatch: found %d, expected %d", count, a.ExpectedReplacements)
	}
	newData := bytes.ReplaceAll(data, []byte(a.OldString), []byte(a.NewString))
	if len(newData) > maxWriteBytes {
		return nil, errors.New("edited file would exceed 4MiB")
	}
	if err := atomicWrite(path, newData, st.Mode().Perm()); err != nil {
		return nil, err
	}
	return map[string]any{"path": path, "replacements": count, "bytes_written": len(newData)}, nil
}

func atomicWrite(path string, data []byte, mode os.FileMode) error {
	parent := filepath.Dir(path)
	tmp, err := os.CreateTemp(parent, ".orc-edit-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	ok := false
	defer func() {
		if !ok {
			_ = os.Remove(name)
		}
	}()
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(name, path); err != nil {
		return err
	}
	ok = true
	return nil
}

func (e *Executor) startSearch(raw json.RawMessage) (any, error) {
	var a struct {
		Path          string `json:"path"`
		Pattern       string `json:"pattern"`
		SearchType    string `json:"search_type,omitempty"`
		Regex         bool   `json:"regex,omitempty"`
		CaseSensitive bool   `json:"case_sensitive,omitempty"`
		IncludeHidden bool   `json:"include_hidden,omitempty"`
		MaxResults    int    `json:"max_results,omitempty"`
		MaxFileBytes  int64  `json:"max_file_bytes,omitempty"`
	}
	if err := decodeArgs(raw, &a); err != nil {
		return nil, err
	}
	root, err := e.paths.ResolveExisting(a.Path)
	if err != nil {
		return nil, err
	}
	st, err := os.Stat(root)
	if err != nil {
		return nil, err
	}
	if !st.IsDir() {
		return nil, errors.New("search path must be a directory")
	}
	id, err := e.search.Start(searchmgr.Query{Path: root, Pattern: a.Pattern, SearchType: a.SearchType, Regex: a.Regex, CaseSensitive: a.CaseSensitive, IncludeHidden: a.IncludeHidden, MaxResults: a.MaxResults, MaxFileBytes: a.MaxFileBytes})
	if err != nil {
		return nil, err
	}
	return map[string]any{"session_id": id}, nil
}
func (e *Executor) getMoreSearch(raw json.RawMessage) (any, error) {
	var a struct {
		SessionID  string `json:"session_id"`
		MaxResults int    `json:"max_results,omitempty"`
	}
	if err := decodeArgs(raw, &a); err != nil {
		return nil, err
	}
	return e.search.GetMore(a.SessionID, a.MaxResults)
}
func (e *Executor) stopSearch(raw json.RawMessage) (any, error) {
	var a struct {
		SessionID string `json:"session_id"`
	}
	if err := decodeArgs(raw, &a); err != nil {
		return nil, err
	}
	if err := e.search.Stop(a.SessionID); err != nil {
		return nil, err
	}
	return map[string]any{"stopped": true}, nil
}
func (e *Executor) listProcesses(ctx context.Context, raw json.RawMessage) (any, error) {
	var a struct {
		MaxResults int `json:"max_results,omitempty"`
	}
	if err := decodeArgs(raw, &a); err != nil {
		return nil, err
	}
	p, err := osproc.List(ctx, a.MaxResults)
	if err != nil {
		return nil, err
	}
	return map[string]any{"processes": p}, nil
}
func (e *Executor) killProcess(raw json.RawMessage) (any, error) {
	if !e.allowKillProcess {
		return nil, errors.New("kill_process is disabled by agent policy")
	}
	var a struct {
		PID int `json:"pid"`
	}
	if err := decodeArgs(raw, &a); err != nil {
		return nil, err
	}
	if err := osproc.Kill(a.PID); err != nil {
		return nil, err
	}
	return map[string]any{"pid": a.PID, "signal": "terminate"}, nil
}
func (e *Executor) getConfig() map[string]any {
	return map[string]any{"allowed_directories": e.paths.Roots(), "system_info": map[string]any{"os": runtime.GOOS, "arch": runtime.GOARCH}, "process": e.procs.Config(), "kill_process_enabled": e.allowKillProcess, "search": map[string]any{"max_sessions": 8, "max_results": 5000}}
}
