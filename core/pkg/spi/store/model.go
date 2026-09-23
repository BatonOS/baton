// SPDX-License-Identifier: Apache-2.0











package store

import "time"




const DefaultTenant = "default"
















const (
	ReviewPending  = "pending"
	ReviewAdmitted = "admitted"
	ReviewDenied   = "denied"
)

type Resource struct {
	ResourceID string
	TenantID   string
	NetworkID  string
	Type       string
	Name       string
	Version    string
	Folder     string
	Visibility string







	Review       string
	ReviewedBy   string
	ReviewedAt   string
	ReviewReason string

	Publisher string
	Hash      string




	Source    string
	Signature string










	Origin  string
	License string



	Compatibility       string
	PermissionsRequired string
	Tags                string
	Detail              string

	Body      string
	CreatedAt string
	UpdatedAt string
}


type NodeThumb struct {
	ContentType string
	Data        []byte
}





const DefaultNetworkName = "local-master"







type Role string

const (
	RoleMaster Role = "master"
	RoleAgent  Role = "agent"
)


func (r Role) Valid() bool {
	switch r {
	case RoleMaster, RoleAgent:
		return true
	}
	return false
}








type Roles []Role

func (rs Roles) Has(r Role) bool {
	for _, x := range rs {
		if x == r {
			return true
		}
	}
	return false
}



func (rs Roles) Valid() bool {
	if len(rs) == 0 {
		return false
	}
	seen := map[Role]bool{}
	for _, r := range rs {
		if !r.Valid() || seen[r] {
			return false
		}
		seen[r] = true
	}
	return true
}








type MasterState string

const (
	MasterActive  MasterState = "active"
	MasterStandby MasterState = "standby"
)



func (m MasterState) Valid() bool {
	switch m {
	case "", MasterActive, MasterStandby:
		return true
	}
	return false
}





type Deployment string

const (
	DeploymentLocal      Deployment = "local"
	DeploymentSelfHosted Deployment = "self_hosted"
	DeploymentBatonCloud Deployment = "baton_cloud"
)

func (d Deployment) Valid() bool {
	switch d {
	case "", DeploymentLocal, DeploymentSelfHosted, DeploymentBatonCloud:
		return true
	}
	return false
}







type NodeStatus string

const (
	NodeStatusPending NodeStatus = "pending"
	NodeStatusActive  NodeStatus = "active"
	NodeStatusOffline NodeStatus = "offline"
	NodeStatusRevoked NodeStatus = "revoked"
)


type Trust string

const (
	TrustOfficial   Trust = "official"
	TrustSelfBuilt  Trust = "self_built"
	TrustUnverified Trust = "unverified"
)


type Node struct {
	NodeID       string
	TenantID     string
	DisplayName  string
	Roles        Roles
	MasterState  MasterState
	Deployment   Deployment
	Status       NodeStatus
	Trust        Trust
	Platform     string
	Arch         string
	AgentVersion string
	Labels       map[string]string

	EnrolledAt       time.Time
	LastSeenAt       *time.Time
	LastHeartbeatSeq int64





	HostAddress string







	RemoteShell *bool






	Label string
















	OwnerIdentityID string




	Runtime RuntimeInfo




	Inbox *InboxInfo



	InflightCalls int
	UptimeSec     int64

	RevokedAt    *time.Time
	RevokeReason string











	SuspendedAt   *time.Time
	SuspendReason string
	SuspendedBy   string

	CreatedAt time.Time
	UpdatedAt time.Time
}






type ProbeResult string

const (
	ProbeUnreported  ProbeResult = ""
	ProbeNotDeclared ProbeResult = "not-declared"
	ProbePending     ProbeResult = "pending"
	ProbePassing     ProbeResult = "passing"
	ProbeFailing     ProbeResult = "failing"
)



func (r ProbeResult) Valid() bool {
	switch r {
	case ProbeUnreported, ProbeNotDeclared, ProbePending, ProbePassing, ProbeFailing:
		return true
	}
	return false
}














type RuntimeStatus string

const (
	StatusNotDeclared RuntimeStatus = "not-declared"
	StatusUnknown     RuntimeStatus = "unknown"
	StatusIdle        RuntimeStatus = "idle"
	StatusWorking     RuntimeStatus = "working"
	StatusNeedsInput  RuntimeStatus = "needs-input"
	StatusDone        RuntimeStatus = "done"



	StatusErrored RuntimeStatus = "errored"
)



func (s RuntimeStatus) Valid() bool {
	switch s {
	case StatusNotDeclared, StatusUnknown, StatusIdle, StatusWorking,
		StatusNeedsInput, StatusDone, StatusErrored:
		return true
	}
	return false
}







type RuntimeInfo struct {
	Name  string
	Type  string
	Image string




	Template string

	Enterable bool








	SkillsMountPath string



	State         string
	RuntimeStatus RuntimeStatus

	RestartCount int
	StartedAt    *time.Time
	LastExitCode *int
	OOMKilled    bool
	LastError    string






	Probe ProbeResult `json:"probe,omitempty"`











	Memory string `json:"memory,omitempty"`
}


func (r RuntimeInfo) Supervised() bool { return r.State != "" || r.Name != "" }







type TakeoverLock struct {
	NodeID     string
	Holder     string
	SessionID  string
	AcquiredAt time.Time
	ExpiresAt  time.Time



	PreemptedFrom string
}


func (l TakeoverLock) Held(now time.Time) bool { return now.Before(l.ExpiresAt) }


type Heartbeat struct {
	Seq           int64
	At            time.Time
	UptimeSec     int64
	InflightCalls int
	CertNotAfter  time.Time


	RemoteShell bool



	Runtime *RuntimeInfo



	Inbox *InboxInfo
}








type InboxInfo struct {
	Waiting int









	OldestWaitingAt time.Time
}


type NodeFilter struct {
	TenantID string


	Role   Role
	Status NodeStatus
	Limit  int
	Cursor string
}










const (
	TokenPurposeNode     = "node"
	TokenPurposeOperator = "operator"
)

type EnrollmentToken struct {
	TokenID   string
	TenantID  string
	TokenHash string








	Purpose string



	Roles       Roles
	NamePattern string
	MaxUses     int
	UsedCount   int







	Invitee     string
	FromNetwork string




	FromNetworkKey string
	ApprovedAt     time.Time



	BoundKeyFingerprint string
	ExpiresAt           time.Time
	RevokedAt           *time.Time
	CreatedBy           string
	CreatedAt           time.Time
}




type JoinRequest struct {
	RequestID string
	TenantID  string
	Agent     string


	NodePublicKeyPEM string
	Fingerprint      string
	FromIP           string
	State            JoinRequestState
	Reason           string

	TokenHash string
	CreatedAt time.Time
	ExpiresAt time.Time
	DecidedAt time.Time
	DecidedBy string
}






type JoinRequestState string

const (
	JoinPending   JoinRequestState = "pending"
	JoinAdmitted  JoinRequestState = "admitted"
	JoinCollected JoinRequestState = "collected"
	JoinConsumed  JoinRequestState = "consumed"
	JoinDenied    JoinRequestState = "denied"
	JoinExpired   JoinRequestState = "expired"
)


func (t *EnrollmentToken) Usable(now time.Time) bool {
	return t.RevokedAt == nil && t.UsedCount < t.MaxUses && now.Before(t.ExpiresAt)
}



type Certificate struct {
	Serial            string
	NodeID            string
	FingerprintSHA256 string
	SubjectCN         string










	SANURI       string
	NotBefore    time.Time
	NotAfter     time.Time
	IssuedAt     time.Time
	RevokedAt    *time.Time
	RevokeReason string
	SupersededBy string
}


type Risk string

const (
	RiskLow     Risk = "low"
	RiskConfirm Risk = "confirm"
	RiskBlocked Risk = "blocked"
)


type Capability struct {
	ID           string
	NodeID       string
	Name         string
	Version      string
	Risk         Risk
	InputSchema  []byte
	OutputSchema []byte
	AllowFrom    []string
	Health       string
	RegisteredAt time.Time
	LastCallAt   *time.Time
}











type OperatorCertificate struct {
	Serial   string
	TenantID string



	OperatorName string
	Fingerprint  string
	SubjectCN    string
	SANURI       string
	NotBefore    time.Time
	NotAfter     time.Time
	IssuedAt     time.Time



	IssuedBy     string
	FromIP       string
	RevokedAt    *time.Time
	RevokeReason string
}


type CallStatus string

const (
	CallQueued     CallStatus = "queued"
	CallDispatched CallStatus = "dispatched"
	CallSucceeded  CallStatus = "succeeded"
	CallFailed     CallStatus = "failed"
	CallExpired    CallStatus = "expired"
)


func (s CallStatus) Terminal() bool {
	return s == CallSucceeded || s == CallFailed || s == CallExpired
}







type Call struct {
	CallID            string
	TenantID          string
	NodeID            string
	CapabilityName    string
	CapabilityVersion string
	IdempotencyKey    string
	Requester         string
	Status            CallStatus

	Input        []byte
	Output       []byte
	ErrorCode    string
	ErrorMessage string

	CreatedAt    time.Time
	DispatchedAt *time.Time
	CompletedAt  *time.Time
	DurationMS   int64

	RequestID string
	TraceID   string
}


type EventCategory string

const (
	CategoryAudit  EventCategory = "audit"
	CategorySystem EventCategory = "system"
)







type Event struct {
	Seq       int64
	EventID   string
	TS        time.Time
	Category  EventCategory
	Event     string
	Actor     string
	ActorType string
	ActingFor string
	Action    string
	Target    string
	Result    string
	SourceIP  string
	NodeID    string
	RequestID string
	TraceID   string



	LeaderEpoch int64
	Detail      []byte
	PrevHash    string
	Hash        string
}


type EventFilter struct {
	TenantID string
	SinceSeq int64
	Category EventCategory
	Event    string
	NodeID   string
	Limit    int
}



type Snapshot struct {
	ID        string
	TakenAt   time.Time
	SizeBytes int64
	SHA256    string
	AppliedAt *time.Time
}






type Identity struct {
	IdentityID string
	TenantID   string


	Name string


	NodeID    string
	CreatedAt time.Time
	BoundAt   time.Time






	InboxPolicy InboxPolicy
}






















type MessageState string

const (
	MessageUnread MessageState = "unread"
	MessageRead   MessageState = "read"
)








type Message struct {
	MessageID string
	TenantID  string










	SourceAgent        string
	SourceNetwork      string
	DestinationAgent   string
	DestinationNetwork string










	SourceAddress string







	ThreadID string
	ReplyTo  string




	Via         string
	ViaIsolated bool


	For string

	CreatedAt time.Time
	State     MessageState









	DeliveredAt time.Time









	DeletedAt time.Time


	ExpiresAt time.Time






	Type        string
	ContentType string
	PayloadSize int



	Payload []byte
}



type MessageFilter struct {

	Recipient string
















	Source *MessageSourceFilter


	State MessageState













	UnexpiredAt time.Time



	Undelivered bool













	NewestFirst bool


	Deleted bool



	ThreadID string
	Limit    int


	WithPayload bool
}





















type MessageSourceFilter struct {
	Networks     []string
	LocalNetwork string
	LocalSenders []string
}







type InboxPolicy struct {




	ActOn InboxActOn





	AllowSenders []string

	AllowNetworks []string





	Channels InboxChannels








	Set bool
}


type InboxActOn string




type InboxChannels string

const (
	InboxChannelsAllow InboxChannels = "allow"
	InboxChannelsHold  InboxChannels = "hold"
)



func (p InboxPolicy) HoldsChannel(via string) bool {
	return via != "" && p.Channels == InboxChannelsHold
}

const (





	InboxActOnEveryone  InboxActOn = "everyone"
	InboxActOnAllowlist InboxActOn = "allowlist"


	InboxActOnNobody InboxActOn = "nobody"
)






func (p InboxPolicy) Allows(sourceNetwork, sourceAgent, localNetwork string) bool {
	switch p.ActOn {
	case InboxActOnNobody:
		return false
	case InboxActOnAllowlist:
		for _, n := range p.AllowNetworks {
			if n != "" && n == sourceNetwork {
				return true
			}
		}
		if sourceNetwork == localNetwork && localNetwork != "" {
			for _, s := range p.AllowSenders {
				if s != "" && s == sourceAgent {
					return true
				}
			}
		}
		return false
	default:




		return true
	}
}





func (p InboxPolicy) SourceFilter(localNetwork string) *MessageSourceFilter {
	if p.ActOn != InboxActOnAllowlist {
		return nil
	}
	return &MessageSourceFilter{
		Networks:     p.AllowNetworks,
		LocalNetwork: localNetwork,
		LocalSenders: p.AllowSenders,
	}
}





type Network struct {
	NetworkID   string
	TenantID    string
	DisplayName string


	PublicKeyPEM string



	Fingerprint string





	Admission          NetworkAdmission
	AdmissionChangedAt time.Time
	AdmissionChangedBy string

	CreatedAt time.Time
}







type Attachment struct {
	MessageID   string
	Index       int
	Name        string
	ContentType string
	Size        int64



	SHA256 string
}




type NetworkAdmission string

const (
	AdmissionOpen   NetworkAdmission = "open"
	AdmissionClosed NetworkAdmission = "closed"
)




type NetworkAddress struct {
	Address   string
	Source    string
	CreatedAt time.Time
}









type ReceivedGrant struct {
	TenantID  string
	GrantID   string
	Issuer    string
	IssuerKey string
	Subject   string
	Scope     string
	IssuedAt  time.Time
	ExpiryAt  time.Time
	Endpoint  string






	PeerResolver string



	PeerDomain    string
	CAPEM         string
	CAFingerprint string
	Payload       string
	Signature     string
	ImportedAt    time.Time
}

type NetworkEndpoint struct {
	Address   string
	Port      int
	Protocol  string
	Priority  int
	UpdatedAt time.Time
}








type Integration struct {
	IntegrationID string
	TenantID      string



	Provider string

	PublicKeyPEM string


	Fingerprint string



	Scopes []string



	TargetNetwork string




	Enabled bool

	CreatedAt   time.Time
	ConfirmedAt time.Time
}







type Skill struct {
	SkillID  string
	TenantID string

	Name    string
	Version string








	SHA256 string




	SourceURL string
	SizeBytes int64

	CreatedAt time.Time
}



const (
	SkillTargetNode     = "node"
	SkillTargetTemplate = "template"
)





type SkillInstall struct {
	InstallID string
	TenantID  string
	SkillID   string

	TargetKind string

	Target string

	CreatedAt time.Time
}






type InstalledSkill struct {
	Skill
	Via string
}











type Grant struct {
	TenantID string
	GrantID  string


	Grantor string
	Grantee string


	Action string

	Object string

	Scope string



	Effect string

	Constraints string
	ValidFrom   time.Time

	ValidUntil time.Time

















	Proof     []byte
	CreatedAt time.Time


	RevokedAt *time.Time
	RevokedBy string
}


const (
	GrantAllow = "allow"
	GrantDeny  = "deny"

	GrantActive  = "ACTIVE"
	GrantExpired = "EXPIRED"
	GrantRevoked = "REVOKED"
)







const ActionCapabilityInvoke = "baton.capability.invoke"















const ActionActionInvoke = "baton.action.invoke"


const GrantGranteeEveryone = "*"






const ActionResourceFetch = "baton.resource.fetch"











const ActionMessageDeliver = "baton.message.deliver"




func (g Grant) StatusAt(now time.Time) string {
	if g.RevokedAt != nil {
		return GrantRevoked
	}
	if !g.ValidUntil.IsZero() && now.After(g.ValidUntil) {
		return GrantExpired
	}
	return GrantActive
}
