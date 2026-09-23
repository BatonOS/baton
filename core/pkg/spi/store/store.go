// SPDX-License-Identifier: Apache-2.0

package store

import (
	"context"
	"errors"
	"io/fs"
	"time"
)



var (
	ErrNotFound = errors.New("store: not found")
	ErrConflict = errors.New("store: conflict")





	ErrInvalid = errors.New("store: invalid value")



	ErrReadOnly = errors.New("store: read-only replica")


	ErrReplayed = errors.New("store: nonce already consumed")
)






type Store interface {
	Nodes() NodeStore
	Tokens() TokenStore
	Certs() CertStore
	Capabilities() CapabilityStore
	OperatorCertificates() OperatorCertificateStore
	Calls() CallStore
	Events() EventStore
	Snapshots() SnapshotStore
	Locks() LockStore
	Identities() IdentityStore
	Networks() NetworkStore
	AccessNonces() AccessNonceStore
	ReceivedGrants() ReceivedGrantStore
	Integrations() IntegrationStore
	Messages() MessageStore
	Attachments() AttachmentStore
	Skills() SkillStore
	Resources() ResourceStore
	JoinRequests() JoinRequestStore
	Grants() GrantStore

	Transactions() TransactionStore
	Interactions() InteractionStore




	WithTx(ctx context.Context, fn func(Store) error) error


	Migrate(ctx context.Context, migrations fs.FS) error



	Driver() string


	ReadOnly() bool

	Close() error
}


type NodeStore interface {
	Create(ctx context.Context, n *Node) error
	Get(ctx context.Context, nodeID string) (*Node, error)
	GetByName(ctx context.Context, tenantID, displayName string) (*Node, error)
	List(ctx context.Context, f NodeFilter) (nodes []Node, nextCursor string, err error)



	UpdateHeartbeat(ctx context.Context, nodeID string, hb Heartbeat) error














	NoteInstructionBlock(ctx context.Context, nodeID, pluginID, path, sha256 string) (first bool, err error)









	ForgetInstructionBlock(ctx context.Context, nodeID, pluginID, path, sha256 string) error

	SetStatus(ctx context.Context, nodeID string, s NodeStatus, reason string) error








	SetSuspended(ctx context.Context, nodeID string, suspended bool, reason, by string) error




	SetMasterState(ctx context.Context, nodeID string, state MasterState) error




	SetRuntimeDecl(ctx context.Context, nodeID string, rt RuntimeInfo) error



	SetNodeAvatar(ctx context.Context, nodeID, contentType string, full, thumb []byte) error

	NodeAvatar(ctx context.Context, nodeID string) (contentType string, full, thumb []byte, err error)


	NodeThumbs(ctx context.Context, tenantID string) (map[string]NodeThumb, error)



	SetLabel(ctx context.Context, nodeID, label string) error




	SetHostAddress(ctx context.Context, nodeID, address string) error





	Touch(ctx context.Context, displayName string, at time.Time) error








	MarkStaleOffline(ctx context.Context, deadline, mirrorDeadline int64) ([]string, error)
}






type LockStore interface {







	Acquire(ctx context.Context, nodeID, holder, sessionID string, ttl time.Duration, preempt bool) (*TakeoverLock, error)




	Renew(ctx context.Context, nodeID, sessionID string, ttl time.Duration) (*TakeoverLock, error)




	Release(ctx context.Context, nodeID, sessionID string) error




	Get(ctx context.Context, nodeID string) (*TakeoverLock, error)
}







type IdentityStore interface {



	Bind(ctx context.Context, tenantID, name, nodeID string) (*Identity, error)













	Ensure(ctx context.Context, tenantID, name string) (*Identity, error)



	Resolve(ctx context.Context, tenantID, name string) (*Identity, error)
	List(ctx context.Context, tenantID string) ([]Identity, error)











	SetInboxPolicy(ctx context.Context, tenantID, name string, p InboxPolicy) (*Identity, error)
}








type NetworkStore interface {





	EnsureIdentity(ctx context.Context, tenantID, displayName string) (*Network, error)



	Get(ctx context.Context, tenantID string) (*Network, error)


	Sign(ctx context.Context, tenantID string, payload []byte) ([]byte, error)




	SetAvatar(ctx context.Context, tenantID, contentType string, data []byte) error

	GetAvatar(ctx context.Context, tenantID string) (contentType string, data []byte, err error)



	Rename(ctx context.Context, tenantID, displayName string) error






	SetAdmission(ctx context.Context, tenantID string, admission NetworkAdmission, by string) error
	PutAddress(ctx context.Context, networkID, address, source string) error
	Addresses(ctx context.Context, networkID string) ([]NetworkAddress, error)
	PutEndpoint(ctx context.Context, networkID string, ep NetworkEndpoint) error



	ReplaceEndpoint(ctx context.Context, networkID string, ep NetworkEndpoint) error
	Endpoints(ctx context.Context, networkID string) ([]NetworkEndpoint, error)
}








type IntegrationStore interface {
	Create(ctx context.Context, in Integration) (*Integration, error)
	Get(ctx context.Context, tenantID, integrationID string) (*Integration, error)




	ByFingerprint(ctx context.Context, tenantID, fingerprint string) (*Integration, error)
	List(ctx context.Context, tenantID string) ([]Integration, error)
	SetEnabled(ctx context.Context, tenantID, integrationID string, enabled bool) error



	Delete(ctx context.Context, tenantID, integrationID string) error




	ClaimNonce(ctx context.Context, scope, nonce string, ttl time.Duration) (bool, error)
}








type JoinRequestStore interface {



	Create(ctx context.Context, r *JoinRequest) error
	Get(ctx context.Context, requestID string) (*JoinRequest, error)


	PendingByAgent(ctx context.Context, tenantID, agent string) (*JoinRequest, error)

	List(ctx context.Context, tenantID string, state JoinRequestState) ([]JoinRequest, error)








	CountsByState(ctx context.Context, tenantID string) (map[JoinRequestState]int, error)

	Decide(ctx context.Context, requestID string, state JoinRequestState, reason, by string) error

	SetCollected(ctx context.Context, requestID, tokenHash string) error



	ConsumeByTokenHash(ctx context.Context, tokenHash string) error

	ExpirePending(ctx context.Context, now time.Time) (int, error)
}





type ResourceStore interface {


	Put(ctx context.Context, tenantID string, r Resource) (*Resource, error)


	List(ctx context.Context, tenantID, typ, visibility string) ([]Resource, error)


	Get(ctx context.Context, tenantID, idOrName, typ string) (*Resource, error)
	Delete(ctx context.Context, tenantID, resourceID string) error



























	Decide(ctx context.Context, tenantID, resourceID, state, by, reason string) error











	CountsByReview(ctx context.Context, tenantID string) (map[string]int, error)

	Move(ctx context.Context, tenantID, resourceID, folder string) error



	SetTags(ctx context.Context, tenantID, resourceID, tags string) error
}

type SkillStore interface {



	Create(ctx context.Context, s Skill) (*Skill, error)
	Get(ctx context.Context, tenantID, skillID string) (*Skill, error)
	List(ctx context.Context, tenantID string) ([]Skill, error)



	Delete(ctx context.Context, tenantID, skillID string) error

	Install(ctx context.Context, in SkillInstall) (*SkillInstall, error)
	Uninstall(ctx context.Context, tenantID, skillID, targetKind, target string) error
	Installs(ctx context.Context, tenantID, skillID string) ([]SkillInstall, error)








	ForNode(ctx context.Context, tenantID, nodeID, template string) ([]InstalledSkill, error)





	SetCode(ctx context.Context, nodeID, skillID, codeID string) error








	CodesForNode(ctx context.Context, nodeID string) (map[string]string, error)
}















type AttachmentStore interface {



	Attach(ctx context.Context, messageID string, items []Attachment) error

	List(ctx context.Context, messageID string) ([]Attachment, error)




	Referenced(ctx context.Context, sha256 string) (bool, error)
}

type MessageStore interface {















	Enqueue(ctx context.Context, m *Message) error

	List(ctx context.Context, tenantID string, f MessageFilter) ([]Message, error)


	Get(ctx context.Context, messageID string) (*Message, error)



	MarkDelivered(ctx context.Context, messageID string) error









	MarkRead(ctx context.Context, messageID string) error







	Delete(ctx context.Context, messageID string) error









	MarkAllRead(ctx context.Context, tenantID, recipient string) (int, error)






	CountUnread(ctx context.Context, tenantID, recipient string) (int, error)



	Expire(ctx context.Context, now int64) (int, error)
}


type TokenStore interface {
	Create(ctx context.Context, t *EnrollmentToken) error
	GetByHash(ctx context.Context, tokenHash string) (*EnrollmentToken, error)
	List(ctx context.Context, tenantID string) ([]EnrollmentToken, error)









	Consume(ctx context.Context, tokenHash, purpose string) (*EnrollmentToken, error)







	ReleaseMember(ctx context.Context, tenantID, agentName, toNetwork string) error

	Revoke(ctx context.Context, tokenID string) error
}


type CertStore interface {
	Issue(ctx context.Context, c *Certificate) error
	Get(ctx context.Context, serial string) (*Certificate, error)
	ListByNode(ctx context.Context, nodeID string) ([]Certificate, error)
	Revoke(ctx context.Context, serial, reason string) error




	RevokedSerials(ctx context.Context) ([]string, error)
}

















type CapabilityStore interface {

	Upsert(ctx context.Context, c *Capability) error
	Get(ctx context.Context, id string) (*Capability, error)
	Resolve(ctx context.Context, nodeID, name, version string) (*Capability, error)
	ListByNode(ctx context.Context, nodeID string) ([]Capability, error)
	List(ctx context.Context, tenantID string) ([]Capability, error)
	SetHealth(ctx context.Context, id, health string) error
}










type OperatorCertificateStore interface {

	Issue(ctx context.Context, c *OperatorCertificate) error

	Get(ctx context.Context, serial string) (*OperatorCertificate, error)



	ListByName(ctx context.Context, name string) ([]OperatorCertificate, error)

	List(ctx context.Context, tenantID string) ([]OperatorCertificate, error)



	Revoke(ctx context.Context, serial, reason string) error
}


type CallStore interface {




	Create(ctx context.Context, c *Call) (*Call, error)
	Get(ctx context.Context, callID string) (*Call, error)
	GetByIdempotencyKey(ctx context.Context, tenantID, key string) (*Call, error)
	SetStatus(ctx context.Context, callID string, s CallStatus) error
	Complete(ctx context.Context, c *Call) error
	List(ctx context.Context, tenantID, nodeID string, limit int) ([]Call, error)
}



type EventStore interface {
	Append(ctx context.Context, e *Event) (*Event, error)
	List(ctx context.Context, f EventFilter) ([]Event, error)
	Last(ctx context.Context) (*Event, error)



	VerifyChain(ctx context.Context, fromSeq int64) (brokenAt int64, err error)

	PruneBefore(ctx context.Context, unixSec int64) (int64, error)
}


type SnapshotStore interface {
	Record(ctx context.Context, s *Snapshot) error
	Latest(ctx context.Context) (*Snapshot, error)
}








type GrantStore interface {



	Issue(ctx context.Context, g Grant) (*Grant, error)



	Get(ctx context.Context, tenantID, grantID string) (*Grant, error)


	List(ctx context.Context, tenantID, grantee, action string) ([]Grant, error)



	Revoke(ctx context.Context, tenantID, grantID, revokedBy string) error
}





type ReceivedGrantStore interface {

	Import(ctx context.Context, g *ReceivedGrant) error

	Get(ctx context.Context, tenantID, grantID string) (*ReceivedGrant, error)

	List(ctx context.Context, tenantID string) ([]ReceivedGrant, error)

	Delete(ctx context.Context, tenantID, grantID string) error
}




type AccessNonceStore interface {

	Consume(ctx context.Context, tenantID, purpose, subject, nonce string) error
}
