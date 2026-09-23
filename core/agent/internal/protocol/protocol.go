// SPDX-License-Identifier: Apache-2.0








package protocol

import (
	"github.com/google/uuid"

	"context"
	"bytes"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/coder/websocket"
)


const EnvelopeVersion = 1


const MaxFrameBytes = 1 << 20


const (
	TypeHello              = "hello"
	TypeHelloAck           = "hello_ack"
	TypeHeartbeat          = "heartbeat"
	TypeHeartbeatAck       = "heartbeat_ack"
	TypeCallRequest        = "call_request"
	TypeCallResult         = "call_result"
	TypeConsoleRequest     = "console_request"
	TypeConsoleResponse    = "console_response"
	TypeCertRotateRequired = "cert_rotate_required"
	TypeRevoked            = "revoked"
	TypeGoodbye            = "goodbye"
	TypeMessageDeliver     = "message_deliver"
	TypeMessageAck         = "message_ack"
	TypeMessageSend        = "message_send"
	TypeMessageSendAck     = "message_send_ack"




	TypePtyOpen            = "pty_open"
	TypePtyData            = "pty_data"
	TypePtyResize          = "pty_resize"
	TypePtyClose           = "pty_close"








	TypeChanged = "changed"
)


const (
	ErrCapabilityNotFound = "CAPABILITY_NOT_FOUND"






	ErrCapabilityNotGranted = "CAPABILITY_NOT_GRANTED"
	ErrCapabilityPaused   = "CAPABILITY_PAUSED"
	ErrCapabilityTimeout  = "CAPABILITY_TIMEOUT"
	ErrInputInvalid       = "INPUT_INVALID"
	ErrOutputTooLarge     = "OUTPUT_TOO_LARGE"
	ErrExecutionFailed    = "EXECUTION_FAILED"
)


type Frame struct {
	V       int             `json:"v"`
	Type    string          `json:"type"`
	MsgID   string          `json:"msg_id,omitempty"`
	Seq     int64           `json:"seq,omitempty"`
	TS      time.Time       `json:"ts"`
	Payload json.RawMessage `json:"payload,omitempty"`
}










func (f Frame) Decode(v any) error {
	if len(f.Payload) == 0 {
		return nil
	}
	dec := json.NewDecoder(bytes.NewReader(f.Payload))
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}



type CapabilityDecl struct {
	Name         string          `json:"name"`
	Version      string          `json:"version"`
	Risk         string          `json:"risk"`
	InputSchema  json.RawMessage `json:"input_schema,omitempty"`
	OutputSchema json.RawMessage `json:"output_schema,omitempty"`
	AllowFrom    []string        `json:"allow_from,omitempty"`
}






type WorkspaceDecl struct {
	Name    string `json:"name"`
	Image   string `json:"image,omitempty"`
	Type    string `json:"type,omitempty"`








	Template string `json:"template,omitempty"`
	Session string `json:"session"`

	Enterable bool `json:"enterable"`









	SkillsMountPath string `json:"skills_mount_path"`
}


































type InstructionBlock struct {

	Plugin string `json:"plugin"`

	Path string `json:"path"`

	SHA256 string `json:"sha256"`
}

type InboxState struct {


	Waiting int `json:"waiting"`









	OldestWaitingCreatedAt string `json:"oldest_waiting_created_at,omitempty"`
}

type WorkspaceState struct {
	State         string    `json:"state"`
	RuntimeStatus string    `json:"runtime_status"`
	RestartCount int        `json:"restart_count"`
	StartedAt    *time.Time `json:"started_at,omitempty"`
	LastExitCode *int       `json:"last_exit_code,omitempty"`
	OOMKilled    bool       `json:"oom_killed,omitempty"`
	LastError    string     `json:"last_error,omitempty"`







	Probe string `json:"probe,omitempty"`







	Memory string `json:"memory,omitempty"`
}

type Hello struct {
	AgentVersion  string           `json:"agent_version"`
	Platform      string           `json:"platform"`
	Arch          string           `json:"arch"`
	ResumeFromSeq int64            `json:"resume_from_seq"`












	LeaderEpoch  int64            `json:"leader_epoch"`
	Capabilities []CapabilityDecl `json:"capabilities"`





	RemoteShell bool `json:"remote_shell"`


	Workspace *WorkspaceDecl `json:"workspace,omitempty"`
}

type RejectedCapability struct {
	Name   string `json:"name"`
	Reason string `json:"reason"`
}

type HelloAck struct {
	HeartbeatIntervalSec int                  `json:"heartbeat_interval_sec"`
	LeaderEpoch          int64                `json:"leader_epoch"`
	AcceptedCapabilities []string             `json:"accepted_capabilities"`
	Rejected             []RejectedCapability `json:"rejected,omitempty"`














	Identities []string `json:"identities,omitempty"`
}



type Changed struct {
	Kind     string `json:"kind"`
	Endpoint string `json:"endpoint,omitempty"`
}

type Heartbeat struct {
	UptimeSec     int64     `json:"uptime_sec"`
	InflightCalls int       `json:"inflight_calls"`
	CertNotAfter  time.Time `json:"cert_not_after"`



	RemoteShell bool `json:"remote_shell"`

	Workspace *WorkspaceState `json:"workspace,omitempty"`










	Inbox *InboxState `json:"inbox,omitempty"`











	InstructionBlocks []InstructionBlock `json:"instruction_blocks,omitempty"`
}

type HeartbeatAck struct {
	ServerTS        time.Time `json:"server_ts"`
	NextExpectedSeq int64     `json:"next_expected_seq"`
}

type CallRequest struct {
	CallID     string          `json:"call_id"`
	Capability string          `json:"capability"`
	Version    string          `json:"version,omitempty"`
	Input      json.RawMessage `json:"input,omitempty"`
	DeadlineMS int64           `json:"deadline_ms"`
	TraceID    string          `json:"trace_id,omitempty"`
}

type CallResult struct {
	CallID       string          `json:"call_id"`
	Status       string          `json:"status"`
	Output       json.RawMessage `json:"output,omitempty"`
	ErrorCode    string          `json:"error_code,omitempty"`
	ErrorMessage string          `json:"error_message,omitempty"`
	DurationMS   int64           `json:"duration_ms"`
}

type ConsoleRequest struct {
	RequestID string         `json:"request_id"`
	Command   string         `json:"command"`
	Args      map[string]any `json:"args,omitempty"`
}

type ConsoleResponse struct {
	RequestID string         `json:"request_id"`
	Result    string         `json:"result"`
	Output    string         `json:"output,omitempty"`
	Data      map[string]any `json:"data,omitempty"`
}

type CertRotateRequired struct {
	Reason   string `json:"reason"`
	GraceSec int    `json:"grace_sec"`
}

type Revoked struct {
	Reason string `json:"reason"`
}

type Goodbye struct {
	Reason string `json:"reason"`
}


type Conn struct {
	ws *websocket.Conn


	writeMu sync.Mutex
}


func NewConn(ws *websocket.Conn) *Conn { return &Conn{ws: ws} }


func (c *Conn) Send(ctx context.Context, typ string, seq int64, payload any) error {
	return c.write(ctx, Frame{
		V: EnvelopeVersion, Type: typ, Seq: seq, TS: time.Now().UTC(),
	}, payload)
}




func (c *Conn) Reply(ctx context.Context, typ, msgID string, payload any) error {
	return c.write(ctx, Frame{
		V: EnvelopeVersion, Type: typ, MsgID: msgID, TS: time.Now().UTC(),
	}, payload)
}







func (c *Conn) SendWithID(ctx context.Context, typ, msgID string, seq int64, payload any) error {
	return c.write(ctx, Frame{
		V: EnvelopeVersion, Type: typ, MsgID: msgID, Seq: seq, TS: time.Now().UTC(),
	}, payload)
}

func (c *Conn) write(ctx context.Context, f Frame, payload any) error {
	if payload != nil {
		raw, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		f.Payload = raw
	}
	encoded, err := json.Marshal(f)
	if err != nil {
		return err
	}
	if len(encoded) > MaxFrameBytes {
		return fmt.Errorf("protocol: frame is %d bytes, over the %d limit",
			len(encoded), MaxFrameBytes)
	}

	c.writeMu.Lock()
	defer c.writeMu.Unlock()



	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return c.ws.Write(ctx, websocket.MessageText, encoded)
}


func (c *Conn) Read(ctx context.Context) (Frame, error) {
	typ, raw, err := c.ws.Read(ctx)
	if err != nil {
		return Frame{}, err
	}
	if typ != websocket.MessageText {
		return Frame{}, fmt.Errorf("protocol: expected a text frame, got %v", typ)
	}
	var f Frame
	if err := json.Unmarshal(raw, &f); err != nil {
		return Frame{}, fmt.Errorf("protocol: malformed frame: %w", err)
	}
	if f.V != EnvelopeVersion {
		return Frame{}, fmt.Errorf("protocol: control plane speaks envelope version %d, this agent speaks %d",
			f.V, EnvelopeVersion)
	}
	return f, nil
}







type MessageDeliver struct {
	MessageID string `json:"message_id"`








	SourceAgent        string `json:"source_agent"`
	SourceNetwork      string `json:"source_network"`












	SourceAddress string `json:"source_address,omitempty"`
	DestinationAgent   string `json:"destination_agent"`
	DestinationNetwork string `json:"destination_network"`



	ThreadID string `json:"thread_id"`
	ReplyTo  string `json:"reply_to,omitempty"`


	Type        string `json:"type,omitempty"`
	CreatedAt   string `json:"created_at"`
	ExpiresAt   string `json:"expires_at"`
	ContentType string `json:"content_type,omitempty"`
	PayloadSize int    `json:"payload_size"`
	Payload     []byte `json:"payload,omitempty"`




	Attachments []AttachmentRef `json:"attachments,omitempty"`



	Via         string `json:"via,omitempty"`
	ViaIsolated *bool  `json:"via_isolated,omitempty"`







	For string `json:"for,omitempty"`
}











type AttachmentRef struct {
	Name        string `json:"name"`
	ContentType string `json:"content_type"`
	Size        int64  `json:"size"`
	SHA256      string `json:"sha256"`
}




type MessageAck struct {
	MessageID string `json:"message_id"`
	Reason    string `json:"reason,omitempty"`
}








type MessageSend struct {

















	To          string `json:"to"`
	ContentType string `json:"content_type,omitempty"`




	Type    string `json:"type,omitempty"`
	Payload []byte `json:"payload,omitempty"`








	MessageID string `json:"message_id,omitempty"`




	ReplyTo string `json:"reply_to,omitempty"`












	Identity string `json:"identity,omitempty"`

	Attachments []AttachmentRef `json:"attachments,omitempty"`






	Via string `json:"via,omitempty"`





	ViaIsolated bool `json:"via_isolated,omitempty"`







	For string `json:"for,omitempty"`
}






type MessageSendAck struct {
	MessageID string `json:"message_id,omitempty"`
	Error     string `json:"error,omitempty"`
	Reason    string `json:"reason,omitempty"`
}











func NewMessageID() string {
	return "msg_" + uuid.Must(uuid.NewV7()).String()
}


type PtyOpen struct {
	Session string `json:"session"`
	Cols    int    `json:"cols"`
	Rows    int    `json:"rows"`
}


type PtyData struct {
	Session string `json:"session"`
	Data    string `json:"data"`
}

type PtyResize struct {
	Session string `json:"session"`
	Cols    int    `json:"cols"`
	Rows    int    `json:"rows"`
}

type PtyClose struct {
	Session string `json:"session"`
	Reason  string `json:"reason,omitempty"`
}
