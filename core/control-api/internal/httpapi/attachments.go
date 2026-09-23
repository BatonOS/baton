// SPDX-License-Identifier: Apache-2.0

package httpapi














import (
	"context"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/batonos/baton/core/control-api/internal/blob"
	"github.com/batonos/baton/core/control-api/internal/channel"
	"github.com/batonos/baton/core/pkg/spi/console"
	spi "github.com/batonos/baton/core/pkg/spi/store"
)

const (
	CodeAttachmentTooLarge = "ATTACHMENT_TOO_LARGE"





	CodeAttachmentBytesGone = "ATTACHMENT_BYTES_GONE"
	CodeBlobsUnavailable    = "BLOBS_UNAVAILABLE"
)




func (a *API) blobsReady(w http.ResponseWriter, r *http.Request) bool {
	if a.Blobs != nil {
		return true
	}
	a.fail(w, r, http.StatusServiceUnavailable, CodeBlobsUnavailable,
		"this control plane is not configured to store attachment bytes",
		"Attachments need a blob directory beside the database. This is a deployment setting, not something the sender did.", nil)
	return false
}





func (a *API) putAttachment(w http.ResponseWriter, r *http.Request) {
	if !a.blobsReady(w, r) {
		return
	}
	digest, size, err := a.Blobs.Put(r.Body, MaxAttachmentBytesLocal)
	if err != nil {
		if strings.Contains(err.Error(), "exceeds the") {



			a.fail(w, r, http.StatusRequestEntityTooLarge, CodeAttachmentTooLarge,
				"that attachment is larger than an attachment may be",
				"The limit here is the widest one; a message addressed to another network is held to a smaller one, and that refusal comes when the message is sent.",
				map[string]any{"limit_bytes": MaxAttachmentBytesLocal, "got_bytes": size})
			return
		}
		a.failInternal(w, r, err)
		return
	}
	a.writeJSON(w, http.StatusCreated, map[string]any{"sha256": digest, "size": size})
}



func (a *API) getAttachment(w http.ResponseWriter, r *http.Request, messageID string, idx int) {
	if !a.blobsReady(w, r) {
		return
	}
	items, err := a.Store.Attachments().List(r.Context(), messageID)
	if err != nil {
		a.failStore(w, r, err)
		return
	}
	if idx < 0 || idx >= len(items) {
		a.fail(w, r, http.StatusNotFound, CodeNotFound,
			"that message has no attachment at that position",
			"`baton inbox` lists a message's attachments and their positions.", nil)
		return
	}
	item := items[idx]

	rc, size, err := a.Blobs.Open(item.SHA256)
	if errors.Is(err, blob.ErrNotFound) {




		a.fail(w, r, http.StatusGone, CodeAttachmentBytesGone,
			"the bytes for "+item.Name+" are not on this control plane",
			"Attachments are deliberately not carried in a standby snapshot, so a control plane that was transferred keeps the list and loses the files. Ask the sender to send it again.",
			map[string]any{"name": item.Name, "sha256": item.SHA256})
		return
	}
	if err != nil {
		a.failInternal(w, r, err)
		return
	}
	defer rc.Close()



	w.Header().Set("Content-Type", item.ContentType)
	w.Header().Set("Content-Length", strconv.FormatInt(size, 10))


	w.Header().Set("Content-Disposition", `attachment; filename="`+sanitiseFilename(item.Name)+`"`)
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, rc)
}



func (a *API) handleAttachmentUpload(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireRole(w, r, console.RoleOperator); !ok {
		return
	}
	a.putAttachment(w, r)
}

func (a *API) handleAttachmentDownload(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireRole(w, r, console.RoleViewer); !ok {
		return
	}
	a.getAttachment(w, r, r.PathValue("message_id"), attachmentIndex(r))
}



func (a *API) handleAgentAttachmentUpload(w http.ResponseWriter, r *http.Request) {
	node, ok := a.requireNode(w, r)
	if !ok {
		return
	}
	_ = node
	a.putAttachment(w, r)
}

func (a *API) handleAgentAttachmentDownload(w http.ResponseWriter, r *http.Request) {
	node, ok := a.requireNode(w, r)
	if !ok {
		return
	}



	messageID := r.PathValue("message_id")
	msg, err := a.Store.Messages().Get(r.Context(), messageID)
	if err != nil {
		a.failStore(w, r, err)
		return
	}




	ident, ierr := a.Store.Identities().Resolve(r.Context(), spi.DefaultTenant, msg.DestinationAgent)
	if ierr != nil || ident == nil || ident.NodeID != node.NodeID {
		a.fail(w, r, http.StatusForbidden, CodeForbidden,
			"that message is not addressed to an agent on this node",
			"A node reads its own mail. Attachments follow the message, not the network.", nil)
		return
	}
	a.getAttachment(w, r, messageID, attachmentIndex(r))
}




func sanitiseFilename(name string) string {
	return strings.NewReplacer(`"`, "", "\\", "", "\r", "", "\n", "").Replace(name)
}





func attachmentIndex(r *http.Request) int {
	n, err := strconv.Atoi(r.PathValue("idx"))
	if err != nil {
		return -1
	}
	return n
}



type manifestError struct {
	status  int
	code    string
	message string
	remedy  string
	details map[string]any
}

func (e *manifestError) write(a *API, w http.ResponseWriter, r *http.Request) {
	a.fail(w, r, e.status, e.code, e.message, e.remedy, e.details)
}
















func (a *API) resolveManifest(ctx context.Context, refs []attachmentRef, destNetwork string) ([]spi.Attachment, *manifestError) {
	if len(refs) == 0 {
		return nil, nil
	}
	if a.Blobs == nil {
		return nil, &manifestError{http.StatusServiceUnavailable, CodeBlobsUnavailable,
			"this control plane is not configured to store attachment bytes",
			"Attachments need a blob directory beside the database. This is a deployment setting, not something the sender did.", nil}
	}
	local := a.localNetworkID(ctx)
	limit := attachmentLimit(local, destNetwork)
	crossNetwork := limit != MaxAttachmentBytesLocal

	out := make([]spi.Attachment, 0, len(refs))
	for i, ref := range refs {
		if ref.SHA256 == "" || ref.Name == "" {
			return nil, &manifestError{http.StatusBadRequest, CodeInvalidRequest,
				"an attachment needs a name and a sha256",
				"Upload the bytes first (POST /messages/attachments); it answers with the digest.", nil}
		}
		rc, size, err := a.Blobs.Open(ref.SHA256)
		if errors.Is(err, blob.ErrNotFound) {
			return nil, &manifestError{http.StatusBadRequest, CodeInvalidRequest,
				"no attachment bytes are stored for " + ref.Name,
				"Upload it first (POST /messages/attachments). A message may only name bytes that are already here — otherwise its recipient sees an attachment that can never be fetched.",
				map[string]any{"name": ref.Name, "sha256": ref.SHA256}}
		}
		if err != nil {
			return nil, &manifestError{http.StatusInternalServerError, CodeInternal,
				"could not read the stored attachment", "Check the control plane logs.", nil}
		}
		rc.Close()

		if size > limit {
			return nil, &manifestError{http.StatusRequestEntityTooLarge, CodeAttachmentTooLarge,
				attachmentRefusal(ref.Name, size, limit, crossNetwork),
				refusalRemedy(crossNetwork),
				map[string]any{"name": ref.Name, "size_bytes": size, "limit_bytes": limit,
					"cross_network": crossNetwork, "destination_network": destNetwork}}
		}
		ct := ref.ContentType
		if ct == "" {



			ct = "application/octet-stream"
		}
		out = append(out, spi.Attachment{
			Index: i, Name: ref.Name, ContentType: ct, Size: size, SHA256: ref.SHA256,
		})
	}
	return out, nil
}


func refusalRemedy(crossNetwork bool) string {
	if crossNetwork {
		return "That limit is for messages leaving this network. The same attachment is within the limit for a recipient here — so this is about who it is addressed to, not about the file."
	}
	return "Send a smaller file, or put it in the workspace and send a reference."
}









func attachmentRefs(ctx context.Context, a *API, messageID string) []channel.AttachmentRef {
	items, err := a.Store.Attachments().List(ctx, messageID)
	if err != nil || len(items) == 0 {
		return nil
	}
	out := make([]channel.AttachmentRef, 0, len(items))
	for _, it := range items {
		out = append(out, channel.AttachmentRef{
			Name: it.Name, ContentType: it.ContentType, Size: it.Size, SHA256: it.SHA256,
		})
	}
	return out
}





func nodeRefs(in []channel.AttachmentRef) []attachmentRef {
	if len(in) == 0 {
		return nil
	}
	out := make([]attachmentRef, 0, len(in))
	for _, a := range in {
		out = append(out, attachmentRef{Name: a.Name, ContentType: a.ContentType, SHA256: a.SHA256})
	}
	return out
}
