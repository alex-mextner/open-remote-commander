// Package v1 contains the stable JSON types shared by Open Remote Commander
// clients, the hosted gateway, and the local device agent.
package v1

import "time"

type DeviceID string
type Subject string
type ToolName string
type ProcessID string

type Device struct {
	ID           DeviceID           `json:"id"`
	Name         string             `json:"name"`
	Online       bool               `json:"online"`
	LastSeenAt   *time.Time         `json:"last_seen_at,omitempty"`
	CreatedAt    time.Time          `json:"created_at"`
	Capabilities DeviceCapabilities `json:"capabilities,omitempty"`
}

type DeviceCapabilities struct {
	Filesystem bool `json:"filesystem"`
	Processes  bool `json:"processes"`
	Shell      bool `json:"shell"`
	Search     bool `json:"search"`
}

type PairingStartRequest struct {
	DeviceName string `json:"device_name"`
}

type PairingStartResponse struct {
	DeviceCode              string `json:"device_code"`
	UserCode                string `json:"user_code"`
	VerificationURI         string `json:"verification_uri"`
	VerificationURIComplete string `json:"verification_uri_complete"`
	ExpiresIn               int    `json:"expires_in"`
	Interval                int    `json:"interval"`
}

type PairingApproveRequest struct {
	UserCode string `json:"user_code"`
}

type PairingTokenRequest struct {
	DeviceCode string `json:"device_code"`
}

type PairingTokenResponse struct {
	DeviceID    DeviceID `json:"device_id"`
	AccessToken string   `json:"access_token"`
	TokenType   string   `json:"token_type"`
}

type RemoteCall struct {
	CallID    string   `json:"call_id"`
	DeviceID  DeviceID `json:"device_id"`
	Tool      ToolName `json:"tool"`
	Arguments any      `json:"arguments"`
}

type RemoteResult struct {
	CallID string       `json:"call_id"`
	OK     bool         `json:"ok"`
	Result any          `json:"result,omitempty"`
	Error  *RemoteError `json:"error,omitempty"`
}

type RemoteError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

const (
	ErrorInvalidArguments = "invalid_arguments"
	ErrorNotFound         = "not_found"
	ErrorForbidden        = "forbidden"
	ErrorOffline          = "device_offline"
	ErrorTimeout          = "timeout"
	ErrorExecution        = "execution_error"
	ErrorInternal         = "internal_error"
)

type ListDevicesArgs struct{}

type DeviceArgs struct {
	DeviceID DeviceID `json:"device_id" jsonschema:"paired device identifier"`
}

type PathArgs struct {
	DeviceID DeviceID `json:"device_id" jsonschema:"paired device identifier"`
	Path     string   `json:"path" jsonschema:"path on the paired device"`
}

type ReadFileArgs struct {
	DeviceID DeviceID `json:"device_id" jsonschema:"paired device identifier"`
	Path     string   `json:"path" jsonschema:"path on the paired device"`
	Offset   int64    `json:"offset,omitempty" jsonschema:"byte offset to start reading from"`
	MaxBytes int      `json:"max_bytes,omitempty" jsonschema:"maximum bytes to return; capped by the agent"`
}

type ReadFileResult struct {
	Path       string `json:"path"`
	Encoding   string `json:"encoding"`
	Content    string `json:"content"`
	Offset     int64  `json:"offset"`
	NextOffset int64  `json:"next_offset"`
	Truncated  bool   `json:"truncated"`
}

type WriteFileArgs struct {
	DeviceID DeviceID `json:"device_id" jsonschema:"paired device identifier"`
	Path     string   `json:"path" jsonschema:"destination path"`
	Content  string   `json:"content" jsonschema:"file content"`
	Encoding string   `json:"encoding,omitempty" jsonschema:"utf-8 or base64"`
	Mode     uint32   `json:"mode,omitempty" jsonschema:"optional POSIX permission bits"`
}

type WriteFileResult struct {
	Path         string `json:"path"`
	BytesWritten int    `json:"bytes_written"`
}

type DirectoryEntry struct {
	Name       string    `json:"name"`
	Type       string    `json:"type"`
	Size       int64     `json:"size"`
	ModifiedAt time.Time `json:"modified_at"`
}

type ListDirectoryResult struct {
	Path      string           `json:"path"`
	Entries   []DirectoryEntry `json:"entries"`
	Truncated bool             `json:"truncated"`
}

type CreateDirectoryArgs struct {
	DeviceID DeviceID `json:"device_id"`
	Path     string   `json:"path"`
	Mode     uint32   `json:"mode,omitempty"`
}

type MoveFileArgs struct {
	DeviceID    DeviceID `json:"device_id"`
	Source      string   `json:"source"`
	Destination string   `json:"destination"`
}

type FileInfoResult struct {
	Path       string    `json:"path"`
	Name       string    `json:"name"`
	Size       int64     `json:"size"`
	Mode       string    `json:"mode"`
	ModifiedAt time.Time `json:"modified_at"`
	IsDir      bool      `json:"is_dir"`
}

type StartProcessArgs struct {
	DeviceID DeviceID          `json:"device_id"`
	Argv     []string          `json:"argv,omitempty" jsonschema:"preferred executable and argument vector"`
	Command  string            `json:"command,omitempty" jsonschema:"shell command, only if shell is enabled on the agent"`
	Cwd      string            `json:"cwd,omitempty"`
	Env      map[string]string `json:"env,omitempty"`
}

type StartProcessResult struct {
	ProcessID ProcessID `json:"process_id"`
	PID       int       `json:"pid"`
}

type ReadProcessOutputArgs struct {
	DeviceID  DeviceID  `json:"device_id"`
	ProcessID ProcessID `json:"process_id"`
	Cursor    int64     `json:"cursor,omitempty"`
	MaxBytes  int       `json:"max_bytes,omitempty"`
}

type ReadProcessOutputResult struct {
	Data       string `json:"data"`
	NextCursor int64  `json:"next_cursor"`
	Truncated  bool   `json:"truncated"`
	Exited     bool   `json:"exited"`
	ExitCode   *int   `json:"exit_code,omitempty"`
}

type InteractWithProcessArgs struct {
	DeviceID  DeviceID  `json:"device_id"`
	ProcessID ProcessID `json:"process_id"`
	Data      string    `json:"data"`
	Encoding  string    `json:"encoding,omitempty"`
}

type ForceTerminateArgs struct {
	DeviceID  DeviceID  `json:"device_id"`
	ProcessID ProcessID `json:"process_id"`
}

type ReadMultipleFilesArgs struct {
	DeviceID        DeviceID `json:"device_id"`
	Paths           []string `json:"paths" jsonschema:"files to read; maximum 32"`
	MaxBytesPerFile int      `json:"max_bytes_per_file,omitempty"`
}

type MultiFileRead struct {
	Path      string `json:"path"`
	Encoding  string `json:"encoding,omitempty"`
	Content   string `json:"content,omitempty"`
	Truncated bool   `json:"truncated,omitempty"`
	Error     string `json:"error,omitempty"`
}

type ReadMultipleFilesResult struct {
	Files []MultiFileRead `json:"files"`
}

type EditBlockArgs struct {
	DeviceID             DeviceID `json:"device_id"`
	FilePath             string   `json:"file_path"`
	OldString            string   `json:"old_string"`
	NewString            string   `json:"new_string"`
	ExpectedReplacements int      `json:"expected_replacements,omitempty" jsonschema:"exact expected replacement count; default 1"`
}

type EditBlockResult struct {
	Path         string `json:"path"`
	Replacements int    `json:"replacements"`
	BytesWritten int    `json:"bytes_written"`
}

type StartSearchArgs struct {
	DeviceID      DeviceID `json:"device_id"`
	Path          string   `json:"path"`
	Pattern       string   `json:"pattern"`
	SearchType    string   `json:"search_type,omitempty" jsonschema:"content or files"`
	Regex         bool     `json:"regex,omitempty"`
	CaseSensitive bool     `json:"case_sensitive,omitempty"`
	IncludeHidden bool     `json:"include_hidden,omitempty"`
	MaxResults    int      `json:"max_results,omitempty"`
	MaxFileBytes  int64    `json:"max_file_bytes,omitempty"`
}

type StartSearchResult struct {
	SessionID string `json:"session_id"`
}

type SearchMatch struct {
	Path    string `json:"path"`
	Line    int    `json:"line,omitempty"`
	Column  int    `json:"column,omitempty"`
	Preview string `json:"preview,omitempty"`
	Kind    string `json:"kind"`
}

type GetMoreSearchResultsArgs struct {
	DeviceID   DeviceID `json:"device_id"`
	SessionID  string   `json:"session_id"`
	MaxResults int      `json:"max_results,omitempty"`
}

type SearchResults struct {
	Results    []SearchMatch `json:"results"`
	Done       bool          `json:"done"`
	Error      string        `json:"error,omitempty"`
	TotalFound int           `json:"total_found"`
}

type StopSearchArgs struct {
	DeviceID  DeviceID `json:"device_id"`
	SessionID string   `json:"session_id"`
}
type ListSearchesArgs struct {
	DeviceID DeviceID `json:"device_id"`
}

type SearchSummary struct {
	SessionID  string `json:"session_id"`
	Done       bool   `json:"done"`
	Buffered   int    `json:"buffered"`
	Returned   int    `json:"returned"`
	TotalFound int    `json:"total_found"`
}

type ListSessionsArgs struct {
	DeviceID DeviceID `json:"device_id"`
}
type ListProcessesArgs struct {
	DeviceID   DeviceID `json:"device_id"`
	MaxResults int      `json:"max_results,omitempty"`
}
type KillProcessArgs struct {
	DeviceID DeviceID `json:"device_id"`
	PID      int      `json:"pid"`
}
type GetConfigArgs struct {
	DeviceID DeviceID `json:"device_id"`
}
