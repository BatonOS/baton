// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/batonos/baton/core/agent/internal/inbox"
	"github.com/batonos/baton/core/agent/internal/protocol"
)







const exitEmpty = 4






func held(bound []string, identity string) bool {
	for _, b := range bound {
		if b == identity {
			return true
		}
	}
	return false
}
















func runInbox(args []string) int {
	if len(args) == 0 {
		inboxUsage()
		return exitConfigError
	}

	sub := args[0]
	fs := flag.NewFlagSet("inbox "+sub, flag.ContinueOnError)
	dataDir := fs.String("data-dir", envOr("BATON_DATA_DIR", "/var/lib/baton"), "node data directory")
	asJSON := fs.Bool("json", false, "print the whole message as JSON instead of the payload")
	wait := fs.Duration("wait", 0, "keep polling for this long before giving up")
	_ = fs.String("to", "", "send: the identity to address")
	_ = fs.String("text", "", "send: the message body")
	_ = fs.String("file", "", "send: read the body from a file, or - for stdin")
	_ = fs.String("reply-to", "", "send: the message id this one answers; keeps the reply in its thread")
	_ = fs.String("for", "", "send: the facility in the recipient's office this is for (e.g. baton.slack); a reply copies the original's `via` here by itself")
	_ = fs.String("type", "", "send: a label for what kind of message this is; carried, never interpreted")
	identity := fs.String("identity", envOr("BATON_IDENTITY", ""), "whose mailbox to open; mailboxes are kept per identity, and a bare request is refused when more than one is bound")


	var attach repeatable
	fs.Var(&attach, "attach", "send: a file to send with the message; repeat for more than one")
	if err := fs.Parse(args[1:]); err != nil {
		return exitConfigError
	}

	box := inbox.New(*dataDir)


	if out := os.Getenv("BATON_OUTBOX_DIR"); out != "" {
		box = inbox.WithOutbox(*dataDir, out)
	}
















	openMailbox := func() (*inbox.Mailbox, int) {



		if dir := os.Getenv("BATON_INBOX_DIR"); dir != "" {
			return inbox.FlatMailbox(dir), 0
		}
		if *identity != "" {
			mb, err := box.Mailbox(*identity)
			if err != nil {
				fmt.Fprintf(os.Stderr, "baton-agent: %v\n", err)
				return nil, exitConfigError
			}





















			bound, told, berr := box.Bound()
			if berr != nil {
				fmt.Fprintf(os.Stderr, "baton-agent: read identities: %v\n", berr)
				return nil, exitInternal
			}
			if !told {
















				seen, merr := box.Mailboxes()
				switch {
				case merr != nil:





					fmt.Fprintf(os.Stderr,
						"baton-agent: this node has not been told which identities it holds, "+
							"and the mailbox list could not be read either (%v), "+
							"so %q could not be checked\n", merr, *identity)
				case !held(seen, *identity):
					holds := "no mailboxes exist here yet"
					if len(seen) > 0 {
						holds = "the mailboxes here are " + strings.Join(seen, ", ")
					}
					fmt.Fprintf(os.Stderr,
						"baton-agent: this node has not been told which identities it holds, "+
							"so %q could not be checked — %s\n", *identity, holds)
				}
			}
			if told && !held(bound, *identity) {



				holds := "no identities"
				if len(bound) > 0 {
					holds = strings.Join(bound, ", ")
				}
				fmt.Fprintf(os.Stderr,
					"baton-agent: this node does not hold %q — it holds %s\n", *identity, holds)
				return nil, exitConfigError
			}
			return mb, exitOK
		}













		bound, told, err := box.Bound()
		if err != nil {
			fmt.Fprintf(os.Stderr, "baton-agent: read identities: %v\n", err)
			return nil, exitInternal
		}
		names := bound
		if !told {





			if names, err = box.Mailboxes(); err != nil {
				fmt.Fprintf(os.Stderr, "baton-agent: read inbox: %v\n", err)
				return nil, exitInternal
			}
		}

		switch len(names) {
		case 1:
			mb, err := box.Mailbox(names[0])
			if err != nil {
				return nil, exitInternal
			}
			return mb, exitOK
		case 0:



			mb, err := box.Mailbox("none")
			if err != nil {
				return nil, exitInternal
			}
			return mb, exitOK
		default:
			fmt.Fprintf(os.Stderr,
				"baton-agent: this node holds %d identities (%s) — say which one with --identity\n",
				len(names), strings.Join(names, ", "))
			return nil, exitConfigError
		}
	}

	switch sub {
	case "list":
		mb, code := openMailbox()
		if mb == nil {
			return code
		}
		waiting, err := mb.List()
		if err != nil {
			fmt.Fprintf(os.Stderr, "baton-agent: read inbox: %v\n", err)
			return exitInternal
		}
		if *asJSON {


			out := make([]map[string]any, 0, len(waiting))
			for _, m := range waiting {







				row := map[string]any{
					"message_id": m.MessageID, "sender": m.Sender,
					"recipient": m.Recipient, "created_at": m.CreatedAt,
					"content_type": m.ContentType, "payload_size": m.PayloadSize,
				}
				if m.Type != "" {
					row["type"] = m.Type
				}
				if len(m.Attachments) > 0 {
					row["attachments"] = m.Attachments
				}
				out = append(out, row)
			}
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			_ = enc.Encode(out)











			if len(waiting) == 0 {
				return exitEmpty
			}
			return exitOK
		}
		for _, m := range waiting {
			fmt.Printf("%s\t%s\t%d\n", m.MessageID, m.Sender, m.PayloadSize)
		}
		if len(waiting) == 0 {
			return exitEmpty
		}
		return exitOK

	case "next", "peek":
		deadline := time.Now().Add(*wait)
		for {












			mb, code := openMailbox()
			if mb == nil {
				return code
			}
			var m inbox.Message
			var err error
			if sub == "peek" {
				m, err = mb.Peek()
			} else {
				m, err = mb.Take()
			}
			if err == nil {
				return printMessage(m, *asJSON)
			}
			if !errors.Is(err, inbox.ErrEmpty) {
				fmt.Fprintf(os.Stderr, "baton-agent: %v\n", err)
				return exitInternal
			}



			if !time.Now().Before(deadline) {
				return exitEmpty
			}
			time.Sleep(500 * time.Millisecond)
		}

	case "fetch":



		return runInboxFetch(fs.Args(), *dataDir)

	case "send":
		return runInboxSend(fs, box, *dataDir, *asJSON)

	case "contacts":
		return runInboxContacts(*asJSON)

	case "contract":
		return printContract(box, *identity)

	default:
		fmt.Fprintf(os.Stderr, "baton-agent inbox: unknown subcommand %q\n\n", sub)
		inboxUsage()
		return exitConfigError
	}
}








type repeatable []string

func (r *repeatable) String() string { return strings.Join(*r, ",") }
func (r *repeatable) Set(v string) error {
	*r = append(*r, v)
	return nil
}












func runInboxSend(fs *flag.FlagSet, box *inbox.Box, dataDir string, asJSONOut bool) int {
	to := fs.Lookup("to").Value.String()
	if to == "" {
		fmt.Fprint(os.Stderr, "baton-agent inbox send: --to <identity> is required\n")
		return exitConfigError
	}
	body, err := readBody(fs.Lookup("text").Value.String(), fs.Lookup("file").Value.String())
	if err != nil {
		fmt.Fprintf(os.Stderr, "baton-agent: %v\n", err)
		return exitConfigError
	}




	local := fmt.Sprintf("out-%d", time.Now().UTC().UnixNano())
	if err := box.Post(inbox.Outgoing{
		To: strings.TrimPrefix(to, "@"), ContentType: "text/plain",
		Payload: body, LocalID: local,




		ReplyTo: fs.Lookup("reply-to").Value.String(),
		For:     forFacility(box, fs.Lookup("for").Value.String(), fs.Lookup("reply-to").Value.String()),
		Identity: fs.Lookup("identity").Value.String(),
		Type:    fs.Lookup("type").Value.String(),




		AttachPaths: attachPaths(fs),
	}); err != nil {
		fmt.Fprintf(os.Stderr, "baton-agent: queue message: %v\n", err)
		return exitInternal
	}










	if asJSONOut {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(map[string]string{
			"local_id": local,
			"to":       strings.TrimPrefix(to, "@"),
		}); err != nil {
			return exitInternal
		}
		return exitOK
	}
	fmt.Fprintf(os.Stderr, "queued %s for @%s\n", local, strings.TrimPrefix(to, "@"))
	return exitOK
}









func attachPaths(fs *flag.FlagSet) []string {
	f := fs.Lookup("attach")
	if f == nil {
		return nil
	}
	got, ok := f.Value.(*repeatable)
	if !ok || len(*got) == 0 {
		return nil
	}
	out := make([]string, 0, len(*got))
	for _, p := range *got {
		abs, err := filepath.Abs(p)
		if err != nil {
			out = append(out, p)
			continue
		}
		out = append(out, abs)
	}
	return out
}







func readBody(text, file string) ([]byte, error) {
	switch {
	case text != "" && file != "":
		return nil, errors.New("give --text or --file, not both")
	case text != "":
		return []byte(text), nil
	case file == "-":
		return io.ReadAll(os.Stdin)
	case file != "":
		return os.ReadFile(file)
	default:
		return nil, errors.New("a message needs a body: --text \"…\" or --file ./report.md")
	}
}






func printMessage(m inbox.Message, asJSON bool) int {
	if asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(m); err != nil {
			return exitInternal
		}
		return exitOK
	}
	fmt.Fprintf(os.Stderr, "from %s  id %s  %d bytes\n", m.Sender, m.MessageID, m.PayloadSize)
	if _, err := os.Stdout.Write(m.Payload); err != nil {
		return exitInternal
	}
	return exitOK
}

func inboxUsage() {
	fmt.Fprint(os.Stderr, `baton-agent inbox - take this node's mail

  list                envelopes waiting, oldest first
  next [--wait 30s]   take the oldest message: payload to stdout, envelope to stderr
  peek                the same, without taking it
  fetch <id> <idx>    one attachment's bytes to stdout, by its position in the
                      message's manifest: fetch msg_01a0-… 0 > report.pdf
  send --to <name>    queue a message; --text "…" or --file ./report.md (- for stdin)
                      --reply-to <id> answers that message and stays in its thread
                      --type <label>  says what kind of message; carried, never interpreted
                      --attach <path> send a file with it; repeat for several
  contacts            who exists, and what you hold: the network's members with
                      their grant references (verification per grant:
                      verified | signature-invalid | unknown). A member with no
                      grant naming it prints no_grant_reference — a state, not
                      an error. Reads the workspace's address book: the network's
                      member projection alongside the signed grants held for
                      this node's identity.
  contract            what this version offers a runtime — ask every round, do
                      not remember it: a rebuilt team may be running on a newer
                      BATON than the one it was created from

  --identity <name>   whose mailbox, and whose name sends. Mailboxes are kept
                      per identity; the control plane binds at most one to a
                      node. With one there is nothing to say and nothing to
                      type; if this node's disk holds more than one, every read
                      verb refuses until you say which. $BATON_IDENTITY is the
                      default; this wins.
                      "contract" prints the mailboxes and which one it opens.
  --json              print the whole message as JSON instead of the payload
  --data-dir <path>   node data directory (BATON_DATA_DIR)

Exit codes: 0 a message, 4 nothing waiting, 2 usage, 10 internal.

A message carries a manifest, not its attachments' bytes. list --json and
next --json print what arrived; fetch is what obtains it. Asking for an index
the manifest does not have is a usage error (2); bytes the control plane no
longer holds is not your mistake and exits 10.

Taking a message tells the control plane nothing. It knows the bytes arrived;
that they were read is not something BATON can claim — which is why "send"
exists: the loop reports progress by the agent saying so, and that is a
statement by something that knows what it did.

"send" queues and returns. The daemon carries it when the channel is up, so a
report survives the control plane being restarted underneath it. There is no
--from: the sender is filled in from this node's identity, so an agent can
address a message but cannot sign one as somebody else.
`)
}
















func whoAmI(box *inbox.Box, flag string) (bound, mailboxes []string, resolved, source string) {
	mailboxes, _ = box.Mailboxes()
	bound, told, berr := box.Bound()
	names := bound
	if !told {
		names = mailboxes
	}
	switch {
	case flag == "" && berr != nil:




		return bound, mailboxes, "", "identities.json could not be read: " + berr.Error()
	case flag != "" && os.Getenv("BATON_IDENTITY") == flag:
		return bound, mailboxes, flag, "env"
	case flag != "":
		return bound, mailboxes, flag, "flag"
	case len(names) == 1 && told:
		return bound, mailboxes, names[0], "the only identity bound to this node"
	case len(names) == 1:
		return bound, mailboxes, names[0], "the only mailbox here (this node has not been told its identities)"
	case len(names) == 0:
		return bound, mailboxes, "", "no identity is bound to this node"
	default:
		return bound, mailboxes, "", "ambiguous — this node holds several, say --identity"
	}
}

func printContract(box *inbox.Box, flag string) int {
	bound, mailboxes, resolved, source := whoAmI(box, flag)
	if mailboxes == nil {
		mailboxes = []string{}
	}
	if bound == nil {
		bound = []string{}
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(map[string]any{






		"contract":         "baton-runtime/2",
		"agent_version":    version,
		"protocol_version": protocol.EnvelopeVersion,
		"inbox_dir": inboxDirOf(box),






		"identities":      bound,
		"mailboxes":       mailboxes,
		"identity":        resolved,
		"identity_source": source,
		"outbox_dir":       box.OutDir(),































		"tools": map[string][]string{
			"baton-inbox": {"list", "next", "peek", "fetch", "send", "contacts", "contract"},






			"baton-resource": {"adopt", "publish", "contract", "skills"},






			"baton-plugin": {"list", "adopt", "disable", "enable", "remove", "contract"},
		},










		"exit_codes": map[string]int{
			"ok": exitOK, "empty": exitEmpty, "usage": exitConfigError,
			"refused": exitPrecondition, "internal": exitInternal,
		},



		"notes": []string{
			"taking a message tells the control plane nothing; report progress with send",






			"send --json gives you a local_id; the daemon files that send's outcome under it in outbox_dir/sent/ — <local_id>.<message_id>.json if it was accepted, <local_id>.refused if it was not",
			"a refusal is final and the file says why: do not re-send it, say so in a message instead",
			"send --attach takes a PATH: the daemon uploads it and the recipient fetches it by digest",
			"the sender of a sent message is filled in by the control plane, not by this node",









			"a sender containing @ came from ANOTHER network (agent@network); a bare name is from this one. An identity name can never contain @, so the marker cannot be forged from inside",
		},
	})
	return exitOK
}



func inboxDirOf(box *inbox.Box) string {
	if dir := os.Getenv("BATON_INBOX_DIR"); dir != "" {
		return dir
	}
	return box.Dir()
}






func forFacility(box *inbox.Box, explicit, replyTo string) string {
	if explicit != "" || replyTo == "" {
		return explicit
	}
	dirs := []string{}
	if d := os.Getenv("BATON_INBOX_DIR"); d != "" {
		dirs = append(dirs, d)
	}
	if names, err := box.Mailboxes(); err == nil {
		for _, n := range names {
			dirs = append(dirs, filepath.Join(box.Dir(), n))
		}
	}
	for _, d := range dirs {
		for _, p := range []string{filepath.Join(d, replyTo+".json"), filepath.Join(d, inbox.ReadDirName, replyTo+".json")} {
			raw, err := os.ReadFile(p)
			if err != nil {
				continue
			}
			var m inbox.Message
			if json.Unmarshal(raw, &m) == nil {
				return m.Via
			}
		}
	}
	return ""
}
