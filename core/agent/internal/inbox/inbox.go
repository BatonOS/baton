// SPDX-License-Identifier: Apache-2.0





























package inbox

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"syscall"
)




const DirName = "inbox"







const ReadDirName = "read"






var ErrEmpty = errors.New("inbox: nothing waiting")








type Message struct {
	MessageID   string `json:"message_id"`
	Sender      string `json:"sender"`
	Recipient   string `json:"recipient"`





	ThreadID string `json:"thread_id,omitempty"`
	ReplyTo  string `json:"reply_to,omitempty"`



	Type        string `json:"type,omitempty"`



	Via         string `json:"via,omitempty"`
	ViaIsolated *bool  `json:"via_isolated,omitempty"`



	For       string `json:"for,omitempty"`
	CreatedAt string `json:"created_at"`
	ExpiresAt   string `json:"expires_at"`
	ContentType string `json:"content_type,omitempty"`
	PayloadSize int    `json:"payload_size"`
	Payload     []byte `json:"payload,omitempty"`








	Attachments []Attachment `json:"attachments,omitempty"`
}





type Attachment struct {
	Name        string `json:"name"`
	ContentType string `json:"content_type"`
	Size        int64  `json:"size"`
	SHA256      string `json:"sha256"`
}



type Box struct {
	dir string




	out string
}


func New(dataDir string) *Box { return &Box{dir: filepath.Join(dataDir, DirName)} }


func WithOutbox(dataDir, outDir string) *Box {
	return &Box{dir: filepath.Join(dataDir, DirName), out: outDir}
}


func (b *Box) Dir() string { return b.dir }









var mailboxName = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,38}[a-z0-9])?$`)







type Mailbox struct {
	dir      string
	identity string
}



func (b *Box) Mailbox(identity string) (*Mailbox, error) {
	if !mailboxName.MatchString(identity) {
		return nil, fmt.Errorf("inbox: %q is not an identity name", identity)
	}
	return &Mailbox{dir: filepath.Join(b.dir, identity), identity: identity}, nil
}


func (m *Mailbox) Identity() string { return m.identity }





func FlatMailbox(dir string) *Mailbox { return &Mailbox{dir: dir} }


func (m *Mailbox) Dir() string { return m.dir }




func (b *Box) Mailboxes() ([]string, error) {
	entries, err := os.ReadDir(b.dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() && mailboxName.MatchString(e.Name()) {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out, nil
}






func (b *Box) Put(m Message) error {






	box, err := b.Mailbox(m.Recipient)
	if err != nil {
		return err
	}
	return box.Put(m)
}



func (box *Mailbox) Put(m Message) error {
	if err := os.MkdirAll(box.dir, 0o700); err != nil {
		return fmt.Errorf("create inbox dir: %w", err)
	}







	if _, err := os.Stat(filepath.Join(box.dir, ReadDirName, m.MessageID+".json")); err == nil {
		return nil
	}
	raw, encErr := json.MarshalIndent(m, "", "  ")
	if encErr != nil {
		return fmt.Errorf("encode message: %w", encErr)
	}
	final := filepath.Join(box.dir, m.MessageID+".json")
	tmp := final + ".partial"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return fmt.Errorf("write message: %w", err)
	}




	if err := ownLikeDir(tmp, box.dir); err != nil {
		return fmt.Errorf("own message: %w", err)
	}
	if err := os.Rename(tmp, final); err != nil {
		return fmt.Errorf("publish message: %w", err)
	}
	return nil
}















func ownLikeDir(path, dir string) error {
	if os.Geteuid() != 0 {
		return nil
	}
	st, err := os.Stat(dir)
	if err != nil {
		return err
	}
	sys, ok := st.Sys().(*syscall.Stat_t)
	if !ok {
		return nil
	}
	return os.Chown(path, int(sys.Uid), int(sys.Gid))
}

func (mb *Mailbox) List() ([]Message, error) {
	entries, err := os.ReadDir(mb.dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	out := make([]Message, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		m, err := mb.read(filepath.Join(mb.dir, e.Name()))
		if errors.Is(err, os.ErrPermission) {



			return nil, fmt.Errorf("read %s: %w", e.Name(), err)
		}
		if err != nil {
			continue
		}
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt == out[j].CreatedAt {
			return out[i].MessageID < out[j].MessageID
		}
		return out[i].CreatedAt < out[j].CreatedAt
	})
	return out, nil
}







func (mb *Mailbox) Take() (Message, error) {
	for {
		waiting, err := mb.List()
		if err != nil {
			return Message{}, err
		}
		if len(waiting) == 0 {
			return Message{}, ErrEmpty
		}

		m := waiting[0]
		readDir := filepath.Join(mb.dir, ReadDirName)
		if err := os.MkdirAll(readDir, 0o700); err != nil {
			return Message{}, fmt.Errorf("create read dir: %w", err)
		}
		name := m.MessageID + ".json"
		err = os.Rename(filepath.Join(mb.dir, name), filepath.Join(readDir, name))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return Message{}, fmt.Errorf("claim message: %w", err)
		}
		return m, nil
	}
}


func (mb *Mailbox) Peek() (Message, error) {
	waiting, err := mb.List()
	if err != nil {
		return Message{}, err
	}
	if len(waiting) == 0 {
		return Message{}, ErrEmpty
	}
	return waiting[0], nil
}

func (mb *Mailbox) read(path string) (Message, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Message{}, err
	}
	var m Message
	if err := json.Unmarshal(raw, &m); err != nil {
		return Message{}, err
	}
	if m.MessageID == "" {
		return Message{}, errors.New("inbox: message has no id")
	}
	return m, nil
}
















const BoundFileName = "identities.json"

func (b *Box) boundPath() string { return filepath.Join(filepath.Dir(b.dir), BoundFileName) }



func (b *Box) SaveBound(names []string) error {
	raw, err := json.MarshalIndent(names, "", "  ")
	if err != nil {
		return fmt.Errorf("encode identities: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(b.boundPath()), 0o700); err != nil {
		return fmt.Errorf("create data dir: %w", err)
	}
	tmp := b.boundPath() + ".partial"



	if err := os.WriteFile(tmp, raw, 0o644); err != nil {
		return fmt.Errorf("write identities: %w", err)
	}
	if err := os.Rename(tmp, b.boundPath()); err != nil {
		return fmt.Errorf("publish identities: %w", err)
	}
	return nil
}







func (b *Box) Bound() ([]string, bool, error) {
	raw, err := os.ReadFile(b.boundPath())
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	var names []string
	if err := json.Unmarshal(raw, &names); err != nil {
		return nil, false, fmt.Errorf("identities file is not a list: %w", err)
	}
	sort.Strings(names)
	return names, true, nil
}




























const OutDirName = "outbox"


const SentDirName = "sent"







type Outgoing struct {
	To          string `json:"to"`
	ContentType string `json:"content_type,omitempty"`
	Payload     []byte `json:"payload,omitempty"`





	ReplyTo string `json:"reply_to,omitempty"`


	For string `json:"for,omitempty"`







	Identity string `json:"identity,omitempty"`


	Type string `json:"type,omitempty"`






	AttachPaths []string `json:"attach_paths,omitempty"`




	Attachments []Attachment `json:"attachments,omitempty"`



	LocalID string `json:"local_id"`












	MessageID string `json:"message_id,omitempty"`
}


func (b *Box) OutDir() string {
	if b.out != "" {
		return b.out
	}
	return filepath.Join(filepath.Dir(b.dir), OutDirName)
}


func (b *Box) Post(m Outgoing) error {
	dir := b.OutDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create outbox dir: %w", err)
	}
	raw, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("encode message: %w", err)
	}
	final := filepath.Join(dir, m.LocalID+".json")
	tmp := final + ".partial"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return fmt.Errorf("write message: %w", err)
	}
	if err := os.Rename(tmp, final); err != nil {
		return fmt.Errorf("publish message: %w", err)
	}
	return nil
}


func (b *Box) Outgoing() ([]Outgoing, error) {
	entries, err := os.ReadDir(b.OutDir())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() && filepath.Ext(e.Name()) == ".json" {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)

	out := make([]Outgoing, 0, len(names))
	for _, n := range names {
		raw, err := os.ReadFile(filepath.Join(b.OutDir(), n))
		if err != nil {
			continue
		}
		var m Outgoing
		if err := json.Unmarshal(raw, &m); err != nil || m.To == "" || m.LocalID == "" {


			continue
		}
		out = append(out, m)
	}
	return out, nil
}


















func (b *Box) RecordAttachments(localID string, items []Attachment) error {
	final := filepath.Join(b.OutDir(), localID+".json")
	raw, err := os.ReadFile(final)
	if err != nil {
		return err
	}
	var m Outgoing
	if err := json.Unmarshal(raw, &m); err != nil {
		return err
	}
	m.Attachments = items
	out, err := json.Marshal(m)
	if err != nil {
		return err
	}
	tmp := final + ".partial"
	if err := os.WriteFile(tmp, out, 0o600); err != nil {
		return err
	}
	if err := ownLikeDir(tmp, b.OutDir()); err != nil {
		return err
	}
	return os.Rename(tmp, final)
}

func (b *Box) AssignMessageID(localID, messageID string) error {
	final := filepath.Join(b.OutDir(), localID+".json")
	raw, err := os.ReadFile(final)
	if err != nil {
		return err
	}
	var m Outgoing
	if err := json.Unmarshal(raw, &m); err != nil {
		return err
	}
	m.MessageID = messageID
	out, err := json.Marshal(m)
	if err != nil {
		return err
	}
	tmp := final + ".partial"
	if err := os.WriteFile(tmp, out, 0o600); err != nil {
		return err
	}
	if err := ownLikeDir(tmp, b.OutDir()); err != nil {
		return err
	}
	return os.Rename(tmp, final)
}





func (b *Box) MarkSent(localID, messageID string) error {
	sentDir := filepath.Join(b.OutDir(), SentDirName)
	if err := os.MkdirAll(sentDir, 0o700); err != nil {
		return err
	}
	if err := ownLikeDir(sentDir, b.OutDir()); err != nil {
		return err
	}
	name := localID + ".json"
	if messageID != "" {
		name = localID + "." + messageID + ".json"
	}
	return os.Rename(filepath.Join(b.OutDir(), localID+".json"), filepath.Join(sentDir, name))
}







func (b *Box) NoteRefusal(localID, reason string) error {
	sentDir := filepath.Join(b.OutDir(), SentDirName)
	if err := os.MkdirAll(sentDir, 0o700); err != nil {
		return err
	}
	if err := ownLikeDir(sentDir, b.OutDir()); err != nil {
		return err
	}
	p := filepath.Join(sentDir, localID+".refused")
	if err := os.WriteFile(p, []byte(reason+"\n"), 0o600); err != nil {
		return err
	}
	return ownLikeDir(p, b.OutDir())
}
